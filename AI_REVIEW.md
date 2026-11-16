# AI_REVIEW: AI interaction log and code review

> **Status: partly filled by the AI, partly for you.** Sections 1-3 record what
> really happened while this project was produced. Section 4 is yours: the lab
> requires that *your pair* verifies one finding and documents one suggestion
> that you modified or rejected. Do not submit this as your own work until you
> have done that and can explain each entry.

## 1. Interaction log

| # | What we asked the AI | What the AI suggested / did | Accepted, changed or rejected | How it was checked |
|---|----------------------|-----------------------------|-------------------------------|--------------------|
| 1 | Implement the Lab 06 gRPC Book Catalog (proto, server, client, tests, docs) from the lab PDF and a list of requirements (simple structure, SQLite, status codes, 2 s client deadlines, page-size cap 50, read-only `CheckAvailability`). | A `.proto` with 4 RPCs, a server split into `main.go` / `service.go` / `store.go`, a client, tests that start a real gRPC server on a random port, and the documents. | Accepted as a starting point; **you** must read and be able to explain every part. | The `.proto` structure and the SQL were checked with `verification/sanity_check.py` (61 checks passed). **The Go code was never compiled or run** (no Go toolchain in that environment). You must run `go vet` and `go test ./...`. |
| 2 | Review the server for "incorrect protobuf assumptions, missing status mappings, DB error handling or context misuse" (the lab's review prompt), without rewriting the project. | The findings in section 2 below. | One finding fixed (F1); the others documented, not changed. | F1: reasoning plus a SQLite experiment (below) and two new test cases. |
| 3 | The lab requires generated code from `protoc`, but no `protoc` or Go was available. Should the generated files be written by hand? | **No.** Generated files must come from the real tools; a hand-written file pretending to be generated would break the "do not edit generated code" rule and could silently differ from real output. Instead: `generate.ps1` plus clear instructions. | Accepted. | `gen/catalogpb/` contains only a README note; `README.md` explains that `.\generate.ps1` must be run first. |

## 2. Review findings (interaction 2)

Checked and found **correct**:

- **Protobuf default values.** A field that the client does not set arrives as
  `0` / `""` / `false`. The server treats `book_id <= 0`, `quantity <= 0`, `page <= 0`
  and blank text as invalid, so "missing" and "zero" are both rejected.
- **No `id` in `CreateBookRequest`**, so a client cannot choose an id.
- **Errors are all checked**: every `Scan`, `Exec`, `Query`, `LastInsertId` and
  `rows.Err()`; `rows.Close()` is deferred.
- **Context use**: every database call uses a `*Context` method with the RPC's
  `ctx`; the client creates a new `context.WithTimeout` for each call.
- **Status mapping**: `sql.ErrNoRows` -> `NotFound`; only the exact
  `UNIQUE constraint failed: books.isbn` text -> `AlreadyExists`; everything else ->
  `Internal` with the real message only in the server log.
- **`CheckAvailability` is read-only**: it only calls `Store.GetBook` (a `SELECT`).

Findings:

| ID | Finding | Action |
|----|---------|--------|
| F1 | **`+Infinity` passed the price check.** `!(price > 0)` rejects 0, negatives and NaN, but `Inf > 0` is true, and a protobuf `double` can carry Inf. SQLite's `CHECK (price > 0)` also accepts it (experiment: inserting `inf` into a `REAL ... CHECK (price > 0)` column succeeded), so a bad value would have been stored. (NaN, by contrast, would have been stored as NULL and failed `NOT NULL`, giving a confusing `Internal`.) | **Fixed** in `CreateBook` (`math.IsInf`); tests "NaN price" and "infinite price" added. |
| F2 | `ListBooks` runs `COUNT(*)` and the page `SELECT` as two separate statements, not in one transaction. If a book is inserted in between, `total` and the page can disagree by one. | Not changed: acceptable for this lab; a transaction would fix it. |
| F3 | If the client's deadline ends *while* SQLite is running a query, the error that the driver returns may not be exactly `context.DeadlineExceeded`. Then it would be reported as `Internal`. In practice the client already sees `DeadlineExceeded` from its own side. | Not changed; not tested (needs a deliberately slow handler). |
| F4 | Duplicate ISBN is detected by matching the error **text**. It works with the current SQLite, but a code-based check (`sqlite3.Error.ExtendedCode == sqlite3.ErrConstraintUnique`) would be more precise. | Not changed: simpler to explain. |
| F5 | `price` is a `double` (floating point). Fine for the lab; real systems store money as integer cents. | Not changed. |
| F6 | No maximum length for `title`, `author`, `isbn`. | Not changed. |

## 3. Design alternatives considered

- Returning an error (`FailedPrecondition`) when the stock is too low in
  `CheckAvailability`: **rejected**, because the lab says an insufficient quantity is a valid
  request answered with `available=false`, not a failure.
- Clamping `page_size` above 50 to 50 versus rejecting it: **clamping** was chosen
  ("limit max 50"); the response has no `page_size` field, so the client cannot see the
  cap other than by counting the books.

## 4. Your part (fill in)

- **A finding I verified myself** (for example F1: run `go test -run TestCreateBookInvalidInput -v ./cmd/server`
  and delete `|| math.IsInf(price, 0)` from `service.go` to see the "infinite price" case fail):

  _what I did and what I saw:_

- **A suggestion I modified or rejected, and why:**

  _your entry:_
