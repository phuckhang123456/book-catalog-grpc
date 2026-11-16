# THINK sheet: Book Catalog gRPC service

> **Status: DRAFT to review.** The lab wants this sheet written by the pair with
> AI switched off. This draft was prepared by an AI assistant from the lab text
> so that the project is complete. Read it, change what you disagree with, and
> make sure **both** partners can explain every line before the Group Check.

## 1. The boundary: who owns what

| Layer | Owns |
|-------|------|
| `.proto` file | The shape of the data (messages, field types, field numbers) and the RPC names with their request/response types. No logic. |
| Go server | Business rules (id > 0, nonblank text, price > 0, stock >= 0, page-size cap), SQL, mapping of errors to gRPC status codes. |
| Go client | Opening the connection, creating a request, a fresh 2-second deadline per call, reading the response and inspecting `status.Code(err)`. |

One call from client to SQLite and back:

```
client main -> pb.BookCatalogClient.GetBook(ctx 2 s, GetBookRequest{book_id})
  -> protobuf bytes over HTTP/2 -> generated server code
  -> catalogServer.GetBook (validate id) -> Store.GetBook
  -> QueryRowContext("SELECT ... WHERE id = ?") -> SQLite (catalog.db)
  -> row -> pb.Book -> GetBookResponse -> protobuf bytes -> client prints it
  (on failure: status error with a code instead of a response)
```

## 2. The contract

- `Book`: `id=1, title=2, author=3, isbn=4, price=5, stock=6`.
- `GetBookRequest{book_id=1}` / `GetBookResponse{book=1}`.
- `ListBooksRequest{page=1, page_size=2}` / `ListBooksResponse{repeated Book books=1, int32 total=2}`.
- `CreateBookRequest{title=1, author=2, isbn=3, price=4, stock=5}` / `CreateBookResponse{book=1}`.
- Service `BookCatalog` with three unary RPCs.

**Why does every field have a unique number?** On the wire protobuf does not send
field names, only `(number, value)` pairs. The number is how the receiver knows
which field a value belongs to. Two fields with the same number in one message
would be ambiguous (and `protoc` refuses it).

**Why must numbers not be casually reused or changed?** An old client or server
that still knows "field 4 = isbn" would misread data that now uses 4 for
something else (or silently drop it). Adding a **new** field with a **new**
number is safe; changing or reusing a number breaks compatibility.

**What does `repeated Book` mean in Go?** A list: the generated field is a slice
of pointers, `[]*pb.Book`. An empty list is a valid, normal value.

**Which fields must the client not assign when creating a book?** The `id`. The
database generates it (`AUTOINCREMENT`), so `CreateBookRequest` has **no** `id`
field at all: the client cannot send one.

**Missing result versus internal failure.** "No such book" is a normal, expected
outcome of a valid request and the client can fix it, so it is `codes.NotFound`.
A broken database is not the client's fault and is `codes.Internal` (details only
in the server log). Both are returned as gRPC status errors, never as a fake
"empty" successful response.

## 3. Design comparison

**A: reuse the Lab 05 REST/JSON endpoints between Go services.**

| Benefits | Costs |
|----------|-------|
| Already built; works with curl, Postman and browsers (easy to debug by hand). | No machine-checked contract: a field renamed in JSON breaks the client at run time, not at compile time. |
| No code generation or extra toolchain. | Each side hand-writes JSON encoding/decoding and URL/status handling; text JSON is larger than binary protobuf. |

**B: generated gRPC stubs over HTTP/2.**

| Benefits | Costs |
|----------|-------|
| Typed contract: client and server are generated from one `.proto`, so mismatches show up as compile errors. | Needs `protoc` and two plugins, and a regeneration step after every contract change. |
| Binary protobuf messages and HTTP/2 give a compact format and a single connection for many calls. | Harder to test by hand (no curl); needs a gRPC client. |

We do **not** claim gRPC is always faster. The difference depends on the workload
(message size, call frequency, network) and has to be measured.

## 4. Predicted failures

| Situation | Expected observation |
|-----------|----------------------|
| `GetBook` with id 0 (or negative) | `InvalidArgument`, no SQL runs |
| `GetBook` with an unknown valid id (9999) | `NotFound` (from `sql.ErrNoRows`) |
| `CreateBook` with a blank title | `InvalidArgument`, nothing inserted |
| `CreateBook` with price 0 or negative stock | `InvalidArgument`, nothing inserted |
| `CreateBook` with an existing ISBN | `AlreadyExists`, no second row |
| Database unavailable (broken file / closed handle) | `Internal` |
| Server stopped, client calls | `Unavailable` (connection refused), or `DeadlineExceeded` if the attempt takes more than the 2-second deadline |
| Deadline expires (too short, or handler too slow) | `DeadlineExceeded` |
| Server regenerated from a different `.proto` than the client | Missing or misread fields (zero values) or `Unimplemented` for an unknown method: the contracts must match |
| Client reuses one expired context for a later call | The later call fails at once with `DeadlineExceeded`: that is why each RPC gets a fresh context |

## 5. Checkpoint

- [x] Client/server/DB call sketch
- [x] Book + RPC request/response designs
- [x] Field numbering and error policy
- [x] REST vs gRPC trade-offs
- [x] Four or more predicted failures
- [ ] Both partners can explain the plan (you must confirm this yourselves)
