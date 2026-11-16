# Book Catalog gRPC Service (Lab 06)

A small gRPC service written in Go. It stores books in a SQLite file
(`catalog.db`) and offers four unary RPCs: `GetBook`, `ListBooks`, `CreateBook`
and (ADAPT) `CheckAvailability`.

- Server: `127.0.0.1:50062`
- Database: `catalog.db` (a new file, separate from the Lab 05 database)

> **First-time setup note.** The two generated files in `gen/catalogpb/`
> (`catalog.pb.go` and `catalog_grpc.pb.go`) are **not in this project yet**:
> they must be produced by `protoc`, and the environment that wrote this
> project had no `protoc`. Run `.\generate.ps1` once (see "Quick start"). Until
> then nothing compiles, because the code imports those files.

## Project structure

| File | Responsibility |
|------|----------------|
| `proto/catalog.proto` | The contract: messages (`Book`, requests, responses) and the `BookCatalog` service with its 4 RPCs. The only hand-written source of the generated code. |
| `generate.ps1` | Runs `protoc` to create the two generated files. |
| `gen/catalogpb/` | The generated code goes here: `catalog.pb.go` (message structs) and `catalog_grpc.pb.go` (client stub + server interface). **Never edit these.** |
| `cmd/server/main.go` | Starts the server: opens the database, registers the service, listens on `127.0.0.1:50062`. |
| `cmd/server/service.go` | The RPC methods. Validate the request, call the Store, map errors to gRPC status codes. |
| `cmd/server/store.go` | All SQL: opens `catalog.db`, creates the table, seeds 3 books (only if empty), get / list / create queries. |
| `cmd/server/server_test.go` | Automated tests (real gRPC calls against temporary databases). |
| `cmd/client/main.go` | Demo client: calls all 4 RPCs with a fresh 2-second deadline each, then shows some expected errors. |
| `verification/sanity_check.py` | Python check of the `.proto` structure and of the SQL (does not run Go code). |
| `THINK.md`, `TESTS.md`, `AI_REVIEW.md`, `ADAPT.md` | The evidence documents required by the lab. |
| `go.mod`, `go.sum` | Module name and dependency versions (`go.sum` is created by `go mod tidy`). |

## How the files work together

```
Go client (cmd/client)
   -> generated stub  (gen/catalogpb: pb.NewBookCatalogClient)
   -> gRPC over HTTP/2, protobuf bytes
   -> generated server code (pb.RegisterBookCatalogServer)
   -> service method  (cmd/server/service.go)
   -> Store function  (cmd/server/store.go)
   -> SQL -> SQLite (catalog.db)
```

The same road leads back: SQLite row -> `pb.Book` -> protobuf bytes -> client.
Errors travel as gRPC **status codes** (`codes.NotFound`, ...), not HTTP codes.

## How protobuf code generation works

`proto/catalog.proto` describes the messages and RPCs in a language-neutral
way. `protoc` reads it, and two plugins write Go code:

- `protoc-gen-go` writes `catalog.pb.go`: a Go struct for each message
  (`pb.Book`, `pb.GetBookRequest`, ...) with getters such as `GetBookId()` and the
  code to turn them into protobuf bytes and back.
- `protoc-gen-go-grpc` writes `catalog_grpc.pb.go`: the client stub
  (`BookCatalogClient`), the server interface (`BookCatalogServer`) and
  `RegisterBookCatalogServer`.

If you change the `.proto` file you must regenerate. Every protobuf field has a
unique **number**; this number (not the name) is what is sent on the wire, so
existing numbers must never be changed or reused.

## Quick start

All commands are run from the project root, in PowerShell.

**1. One-time tools** (the Pre-lab covers this):

```powershell
protoc --version                     # protoc must be installed (github.com/protocolbuffers/protobuf/releases)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# %USERPROFILE%\go\bin must be on PATH so that protoc finds the two plugins.
# go-sqlite3 uses CGO: a C compiler (gcc, e.g. MSYS2 / MinGW-w64) must be installed.
```

**2. Generate the code, then fetch the dependencies** (in this order):

```powershell
.\generate.ps1       # creates gen/catalogpb/catalog.pb.go and catalog_grpc.pb.go
go mod tidy          # downloads grpc, protobuf, go-sqlite3; creates go.sum
gofmt -l .           # prints nothing when everything is formatted (gofmt -w . fixes it)
go vet ./...
```

(`go mod tidy` must come after `generate.ps1`, because it needs the generated package.)

**3. Run the server** (Terminal 1):

```powershell
go run ./cmd/server
```

The first start creates `catalog.db` and inserts 3 sample books. Stop with `Ctrl+C`.
Existing data is never reset when the server starts.

**4. Run the client** (Terminal 2):

```powershell
go run ./cmd/client
```

It prints each result and the status codes of some deliberate errors.
(Optional: `go run ./cmd/client -addr 127.0.0.1:50062`.)

## How each RPC works

| RPC | Request -> Response | Success | Errors (gRPC code) |
|-----|---------------------|---------|--------------------|
| `GetBook` | `book_id` -> `book` | the book | `InvalidArgument` (id <= 0), `NotFound`, `Internal` |
| `ListBooks` | `page`, `page_size` -> `books`, `total` | up to `page_size` books ordered by `id`; `total` = all books | `InvalidArgument` (page or page_size <= 0), `Internal` |
| `CreateBook` | `title`, `author`, `isbn`, `price`, `stock` -> `book` | the stored book with its **generated id** | `InvalidArgument` (blank text, price not a finite number > 0, stock < 0), `AlreadyExists` (isbn used), `Internal` |
| `CheckAvailability` | `book_id`, `quantity` -> `available`, `current_stock` | `available = (stock >= quantity)`; **never changes the stock** | `InvalidArgument` (id <= 0 or quantity <= 0), `NotFound`, `Internal` |

Details:

- `page_size` above 50 is **capped at 50** (not an error). A page after the last
  one returns an empty list with the correct `total`.
- "Not enough stock" is **not** an error: `CheckAvailability` answers
  `available = false` with status `OK`.
- `NotFound` comes from `sql.ErrNoRows`; `AlreadyExists` only from SQLite's
  "UNIQUE constraint failed: books.isbn" error. Any other database error is
  `Internal` (the details are only logged on the server).
- Every client call gets a fresh `context.WithTimeout(..., 2*time.Second)`. The
  server passes the call's context to `QueryRowContext` / `QueryContext` /
  `ExecContext`, so a database call stops when the deadline ends.
- `Unavailable` is what the client sees when no server is running.

## Run the automated tests

```powershell
go test ./...        # all tests
go test -v ./...     # verbose: lists every test and sub-test
```

Each test starts a real gRPC server on a random local port with its **own
temporary database**; your real `catalog.db` is never touched. The tests check
the gRPC status codes **and** the actual rows in SQLite. They cover the six
required cases, server unavailable, restart persistence, the page-size cap, an
expired deadline, a broken database (`Internal`), and `CheckAvailability` below /
equal to / above the stock plus invalid and missing input.

The Python check needs no Go: `python3 verification/sanity_check.py`.

## Security note

This lab uses insecure, local-only transport (`insecure.NewCredentials()`).
A production service would use TLS.
