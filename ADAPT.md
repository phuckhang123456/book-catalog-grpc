# ADAPT: the CheckAvailability RPC

> **Status.** The contract change and the code are done. The regeneration step
> and the tests have **not been run** by the author of this file (no `protoc` /
> Go in that environment). Fill in the "Observed" lines after you run them.

## New requirement

Another service must ask whether a requested quantity can be fulfilled **without
modifying inventory**:

```
CheckAvailability(book_id, quantity) -> available (bool), current_stock (int32)
```

## 1. What changed in `proto/catalog.proto`

Only additions. Nothing that existed was edited, renumbered or removed.

```diff
+// ----- CheckAvailability (ADAPT) -----
+message CheckAvailabilityRequest {
+  int32 book_id = 1;
+  int32 quantity = 2;
+}
+
+message CheckAvailabilityResponse {
+  bool available = 1;
+  int32 current_stock = 2;
+}

 service BookCatalog {
   rpc GetBook(GetBookRequest) returns (GetBookResponse);
   rpc ListBooks(ListBooksRequest) returns (ListBooksResponse);
   rpc CreateBook(CreateBookRequest) returns (CreateBookResponse);
+  rpc CheckAvailability(CheckAvailabilityRequest) returns (CheckAvailabilityResponse);
 }
```

The numbers 1 and 2 in the new messages are new because the messages are new:
numbering is per message. The existing messages (`Book`, the three request/response
pairs) keep exactly their numbers (`verification/sanity_check.py` checks this).

## 2. Regeneration

```powershell
.\generate.ps1       # re-runs protoc: rewrites catalog.pb.go and catalog_grpc.pb.go
go build ./...
```

Both generated files are rewritten completely. The new files contain the two new
message types, `CheckAvailability` on the client stub (`BookCatalogClient`) and on the
server interface (`BookCatalogServer`).

Observed: _(fill in)_

## 3. What changed in the handwritten code

| File | Change |
|------|--------|
| `cmd/server/service.go` | New method `CheckAvailability`. Reuses `Store.GetBook`; no new SQL. |
| `cmd/client/main.go` | New step 5 (quantities 3, 5, 6 against stock 5) and two error demos. |
| `cmd/server/server_test.go` | `TestCheckAvailabilityBoundaries`, `TestCheckAvailabilityInvalidAndMissing`. |
| `cmd/server/store.go`, `main.go` | **Unchanged.** |

## 4. Rules implemented

| Request | Result |
|---------|--------|
| `book_id <= 0` | `InvalidArgument` |
| `quantity <= 0` | `InvalidArgument` |
| unknown book | `NotFound` |
| `quantity <= stock` | `OK`, `available = true`, `current_stock = stock` |
| `quantity > stock` | `OK`, `available = false`, `current_stock = stock` (a valid request, **not** a transport error) |

`CheckAvailability` is read-only: its only database access is a `SELECT` through
`Store.GetBook`. The tests prove it by reading the stock and the row count straight from
SQLite after every call.

## 5. Boundary tests (stock = 5)

| Quantity | Expected | Test | Observed |
|----------|----------|------|----------|
| 3 (below) | available = true, current_stock = 5 | `TestCheckAvailabilityBoundaries` | _(fill in)_ |
| 5 (equal) | available = true, current_stock = 5 | same | _(fill in)_ |
| 6 (greater) | available = false, current_stock = 5 | same | _(fill in)_ |
| 0 / negative; id 0 / negative; unknown id | `InvalidArgument` / `InvalidArgument` / `NotFound` | `TestCheckAvailabilityInvalidAndMissing` | _(fill in)_ |

## 6. Reflection

**Which part of the system changed after the contract changed?**
First the `.proto` file (the source of truth). Then the **generated** code, which is
rewritten by `protoc` (not by hand). Then the **server** (must implement the new method;
until it does, the embedded `UnimplementedBookCatalogServer` would answer
`Unimplemented`) and the **client** (to call it). The database schema and the other three
RPCs did not change.

**Would an older client still be able to use the existing RPCs?**
Yes. We only added a new method and new messages. The three old methods have the same
names and the same messages with the same field numbers, so an old client's bytes are
read exactly as before. An old client simply does not know `CheckAvailability` and never
calls it. (The reverse is not true: a *new* client calling a server that was *not* updated
gets `Unimplemented` for `CheckAvailability`.)

**Why must published protobuf field numbers be stable?**
Protobuf sends the field *number*, not the name. If number 4 in `Book` changed meaning
(or was reused for another field), old clients would read the new data as the old field
and produce wrong values without any error. So published numbers are never changed or
reused; new data gets new numbers (and a removed field's number should be reserved).

**Trade-off of putting the check in its own RPC.** Between `CheckAvailability` and a later
order, another request may change the stock, so `available = true` is a statement about
*now*, not a reservation. The order step must still verify (or atomically reduce) the stock.
