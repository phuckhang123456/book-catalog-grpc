# TESTS: test record, break/fix and sample output

> **Status: NOT YET EXECUTED.** The Go tests were written in an environment
> without Go and without `protoc`, so **no Go test has been run**. The
> "Actual" and "PASS/FAIL" columns below are empty on purpose: run the tests on
> your machine and fill them in from real output. Do not copy the "Expected"
> column into them.
>
> What *was* run: `python3 verification/sanity_check.py` (the `.proto` structure
> and the SQL logic, 60+ checks, all passed). That does not exercise any Go or
> gRPC code.

## How to run and record

```powershell
.\generate.ps1
go mod tidy
go test -v ./... 2>&1 | Tee-Object -FilePath test_output.log
```

Then paste the relevant lines into the "Actual" column and the final summary at the bottom.

## Test record

All tests are in `cmd/server/server_test.go`. Each makes real gRPC calls against a
temporary database and also checks the database where it matters.

| # | Case | Test function | Expected gRPC result and data | Actual | PASS/FAIL |
|---|------|---------------|-------------------------------|--------|-----------|
| 1 | GetBook for a seeded id | `TestGetBookSeeded` | OK; book 1 is "The Go Programming Language" | | |
| 2 | ListBooks page 1, size 2 | `TestListBooksPagination` | OK; 2 books (ids 1, 2), total 3; page 2 has id 3; a page past the end is empty | | |
| 3 | CreateBook, then GetBook with the returned id | `TestCreateBookThenGet` | OK; new generated id > 3; GetBook returns it; row exists in SQLite; 4 rows | | |
| 4 | GetBook id 0 / -1 and absent id 9999 | `TestGetBookInvalidAndMissing` | `InvalidArgument` for 0 and -1; `NotFound` for 9999 | | |
| 5 | CreateBook: empty/blank title, empty author or isbn, price 0 / negative / NaN / infinite, negative stock | `TestCreateBookInvalidInput` | `InvalidArgument` each; row count stays 3 | | |
| 6 | CreateBook with an existing ISBN | `TestCreateBookDuplicateISBN` | `AlreadyExists`; still exactly 1 row with that ISBN; 3 rows | | |
| 7 | No server running | `TestServerUnavailable` | `Unavailable` (or `DeadlineExceeded` depending on timing) | | |
| 8 | Restart, then GetBook | `TestPersistenceAfterRestart` | OK; data created before the restart is still there; seeds not duplicated (4 rows) | | |
| 9 | ListBooks invalid page / page_size | `TestListBooksInvalidArguments` | `InvalidArgument` for 0 and negative values | | |
| 10 | ListBooks page_size above 50 | `TestListBooksPageSizeCap` | OK; exactly 50 books, total 63 | | |
| 11 | Deadline already expired | `TestExpiredDeadline` | `DeadlineExceeded` | | |
| 12 | Database broken (closed) | `TestDatabaseFailureReturnsInternal` | `Internal` for GetBook; `Internal` (not `AlreadyExists`) for CreateBook | | |
| 13 | CheckAvailability: quantity below / equal to / above stock 5 | `TestCheckAvailabilityBoundaries` | OK each; `available` = true, true, false; `current_stock` = 5; stock in SQLite still 5; row count unchanged | | |
| 14 | CheckAvailability: book id 0 / -1, quantity 0 / -2, unknown book | `TestCheckAvailabilityInvalidAndMissing` | `InvalidArgument` x4, `NotFound`; stock of book 1 unchanged | | |

Cases 1-6 are the six required cases of the lab (Section 6), 7-8 the recommended
ones, 9-12 are extra, 13-14 are the ADAPT cases.

## Break/fix exercise (do this yourselves, on a copy)

Goal from the lab: remove the `sql.ErrNoRows` -> `NotFound` mapping, observe the
wrong status, restore it, retest.

1. Copy the project folder (do not do this in the original).
2. In `cmd/server/store.go`, function `GetBook`, comment out the block
   `if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }` (the `errors` and
   `sql` imports are still used elsewhere, so it still compiles).
3. Run the client or `go test -run TestGetBookInvalidAndMissing -v ./cmd/server`
   and call an unknown id (9999).
4. Restore the block and rerun.

| Step | Prediction (from reading the code, **not observed**) | Observed (fill in) |
|------|------------------------------------------------------|--------------------|
| Unknown id with the mapping removed | The raw `sql.ErrNoRows` reaches `toStatusError`, which has no case for it, so the client gets `Internal` ("internal server error") instead of `NotFound`; the server log prints "unexpected error: sql: no rows in result set". | |
| After restoring the block | `NotFound` again; the test passes. | |

## Sample output (paste real output here)

`go run ./cmd/client` (with the server running):

```
(paste here after running)
```

`go test -v ./...` summary:

```
(paste here after running)
```
