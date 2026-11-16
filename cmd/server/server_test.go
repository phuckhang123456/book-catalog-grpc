// cmd/server/server_test.go
//
// Automated tests. Run them from the project root with:  go test ./...
//
// How the tests work:
//   - Every test creates its OWN temporary SQLite file (t.TempDir()), so the
//     real catalog.db is never touched. Go deletes the temp folder afterwards.
//   - Every test starts a REAL gRPC server on 127.0.0.1 with a random free port
//     (port 0 = "pick any free port") and talks to it through the generated
//     client stub. So each call really goes: stub -> TCP/HTTP2 -> server ->
//     service method -> Store -> SQLite, and the same way back.
//   - Many tests also run their own SELECT to check what is really stored in
//     the database, not only what the response says.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "book-catalog-grpc/gen/catalogpb"
)

// ---------------------------------------------------------------------------
// Helper functions used by many tests
// ---------------------------------------------------------------------------

// testServer bundles a running server with a client that is connected to it.
type testServer struct {
	client pb.BookCatalogClient // The generated client stub: use it to make RPCs.
	db     *sql.DB              // Direct access to the database, to check stored data.
	stop   func()               // Shuts down the client, the server and the database.
}

// startTestServer opens the database file at dbPath (creating and seeding it if
// needed), starts a gRPC server on a random local port and returns a connected
// client. The restart test calls it twice with the same dbPath.
func startTestServer(t *testing.T, dbPath string) *testServer {
	t.Helper() // Failures are reported at the line of the calling test.

	db, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatalf("OpenDatabase failed: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}

	// The same registration as in main.go.
	grpcServer := grpc.NewServer()
	pb.RegisterBookCatalogServer(grpcServer, &catalogServer{store: &Store{DB: db}})
	go grpcServer.Serve(listener) // Serve runs in the background (a goroutine).

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("cannot create client: %v", err)
	}

	ts := &testServer{db: db, client: pb.NewBookCatalogClient(conn)}
	ts.stop = func() {
		conn.Close()
		grpcServer.Stop()
		db.Close()
	}
	// t.Cleanup runs when the test ends. Calling stop twice is harmless.
	t.Cleanup(ts.stop)
	return ts
}

// newTestServer starts a server with a fresh temporary database that contains
// the 3 sample books (ids 1, 2, 3).
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return startTestServer(t, filepath.Join(t.TempDir(), "test_catalog.db"))
}

// rpcContext returns a fresh context with a 2-second deadline, exactly like the
// real client does for every call.
func rpcContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// expectCode fails the test if err does not carry the gRPC status code `want`.
// status.Code(nil) is codes.OK, so a call that unexpectedly succeeds fails too.
func expectCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("expected gRPC code %v but got %v (error: %v)", want, got, err)
	}
}

// createTestBook creates a book through the real CreateBook RPC and returns it.
func createTestBook(t *testing.T, ts *testServer, isbn string, stock int32) *pb.Book {
	t.Helper()
	res, err := ts.client.CreateBook(rpcContext(t), &pb.CreateBookRequest{
		Title: "Test Book", Author: "Test Author", Isbn: isbn, Price: 10.5, Stock: stock,
	})
	if err != nil {
		t.Fatalf("CreateBook failed: %v", err)
	}
	return res.GetBook()
}

// countBooks returns the number of rows in the books table (checked directly in SQLite).
func countBooks(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM books").Scan(&n); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	return n
}

// stockInDB reads the stock of one book directly from SQLite.
func stockInDB(t *testing.T, db *sql.DB, id int32) int {
	t.Helper()
	var stock int
	if err := db.QueryRow("SELECT stock FROM books WHERE id = ?", id).Scan(&stock); err != nil {
		t.Fatalf("stock query failed: %v", err)
	}
	return stock
}

// ---------------------------------------------------------------------------
// Section 6 - required verification cases
// ---------------------------------------------------------------------------

// Case 1: GetBook for a seeded id returns the correct book.
func TestGetBookSeeded(t *testing.T) {
	ts := newTestServer(t)

	res, err := ts.client.GetBook(rpcContext(t), &pb.GetBookRequest{BookId: 1})
	if err != nil {
		t.Fatalf("GetBook(1) failed: %v", err)
	}
	b := res.GetBook()
	if b.GetId() != 1 || b.GetTitle() != "The Go Programming Language" ||
		b.GetIsbn() != "978-0134190440" || b.GetPrice() != 39.99 || b.GetStock() != 10 {
		t.Errorf("unexpected book: %v", b)
	}
}

// Case 2: ListBooks page 1 with size 2 gives 2 books, the right total and order.
func TestListBooksPagination(t *testing.T) {
	ts := newTestServer(t)

	res, err := ts.client.ListBooks(rpcContext(t), &pb.ListBooksRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("ListBooks failed: %v", err)
	}
	if len(res.GetBooks()) != 2 || res.GetTotal() != 3 {
		t.Fatalf("expected 2 books and total 3, got %d books and total %d", len(res.GetBooks()), res.GetTotal())
	}
	if res.GetBooks()[0].GetId() != 1 || res.GetBooks()[1].GetId() != 2 {
		t.Errorf("books are not ordered by id: %v", res.GetBooks())
	}

	// Page 2 holds the remaining book (id 3); the total stays 3.
	res, err = ts.client.ListBooks(rpcContext(t), &pb.ListBooksRequest{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("ListBooks page 2 failed: %v", err)
	}
	if len(res.GetBooks()) != 1 || res.GetBooks()[0].GetId() != 3 || res.GetTotal() != 3 {
		t.Errorf("unexpected page 2: %v total=%d", res.GetBooks(), res.GetTotal())
	}

	// A page after the last one is not an error: it is simply empty.
	res, err = ts.client.ListBooks(rpcContext(t), &pb.ListBooksRequest{Page: 10, PageSize: 2})
	if err != nil || len(res.GetBooks()) != 0 || res.GetTotal() != 3 {
		t.Errorf("page past the end should be empty with total 3: err=%v books=%v", err, res.GetBooks())
	}
}

// ListBooks rejects page <= 0 and page_size <= 0 with InvalidArgument.
func TestListBooksInvalidArguments(t *testing.T) {
	ts := newTestServer(t)

	bad := []*pb.ListBooksRequest{
		{Page: 0, PageSize: 2},
		{Page: -1, PageSize: 2},
		{Page: 1, PageSize: 0},
		{Page: 1, PageSize: -5},
	}
	for _, req := range bad {
		_, err := ts.client.ListBooks(rpcContext(t), req)
		expectCode(t, err, codes.InvalidArgument)
	}
}

// A page_size above 50 is capped at 50 (not an error).
func TestListBooksPageSizeCap(t *testing.T) {
	ts := newTestServer(t)

	// Insert 60 more rows directly (63 in total) inside one transaction.
	tx, err := ts.db.Begin()
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	for i := 0; i < 60; i++ {
		_, err := tx.Exec("INSERT INTO books (title, author, isbn, price, stock) VALUES ('B', 'A', ?, 1, 1)",
			fmt.Sprintf("BULK-%d", i))
		if err != nil {
			t.Fatalf("bulk insert failed: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	res, err := ts.client.ListBooks(rpcContext(t), &pb.ListBooksRequest{Page: 1, PageSize: 1000})
	if err != nil {
		t.Fatalf("ListBooks failed: %v", err)
	}
	if len(res.GetBooks()) != 50 || res.GetTotal() != 63 {
		t.Errorf("expected 50 books (cap) and total 63, got %d and %d", len(res.GetBooks()), res.GetTotal())
	}
}

// Case 3: CreateBook with valid data, then GetBook with the returned id.
func TestCreateBookThenGet(t *testing.T) {
	ts := newTestServer(t)

	created := createTestBook(t, ts, "LAB06-001", 4)
	if created.GetId() <= 3 {
		t.Errorf("expected a generated id greater than the 3 sample ids, got %d", created.GetId())
	}
	if created.GetTitle() != "Test Book" || created.GetStock() != 4 || created.GetPrice() != 10.5 {
		t.Errorf("unexpected created book: %v", created)
	}

	// The book can be fetched through the API ...
	res, err := ts.client.GetBook(rpcContext(t), &pb.GetBookRequest{BookId: created.GetId()})
	if err != nil {
		t.Fatalf("GetBook of the new book failed: %v", err)
	}
	if res.GetBook().GetIsbn() != "LAB06-001" {
		t.Errorf("GetBook returned different data: %v", res.GetBook())
	}

	// ... and it really is in the database.
	var isbn string
	err = ts.db.QueryRow("SELECT isbn FROM books WHERE id = ?", created.GetId()).Scan(&isbn)
	if err != nil || isbn != "LAB06-001" {
		t.Errorf("row not found in database: isbn=%q err=%v", isbn, err)
	}
	if countBooks(t, ts.db) != 4 {
		t.Errorf("expected 4 rows after one insert")
	}
}

// Case 4: GetBook for id 0 (and negative) -> InvalidArgument; unknown id -> NotFound.
func TestGetBookInvalidAndMissing(t *testing.T) {
	ts := newTestServer(t)

	for _, id := range []int32{0, -1} {
		_, err := ts.client.GetBook(rpcContext(t), &pb.GetBookRequest{BookId: id})
		expectCode(t, err, codes.InvalidArgument)
	}

	_, err := ts.client.GetBook(rpcContext(t), &pb.GetBookRequest{BookId: 9999})
	expectCode(t, err, codes.NotFound)
}

// Case 5: CreateBook with an empty title, price 0 (or NaN / infinite) or negative stock ->
// InvalidArgument, and nothing is inserted.
func TestCreateBookInvalidInput(t *testing.T) {
	ts := newTestServer(t)

	cases := []struct {
		name string
		req  *pb.CreateBookRequest
	}{
		{"empty title", &pb.CreateBookRequest{Title: "", Author: "A", Isbn: "BAD-1", Price: 5, Stock: 1}},
		{"blank title", &pb.CreateBookRequest{Title: "   ", Author: "A", Isbn: "BAD-2", Price: 5, Stock: 1}},
		{"empty author", &pb.CreateBookRequest{Title: "T", Author: "", Isbn: "BAD-3", Price: 5, Stock: 1}},
		{"empty isbn", &pb.CreateBookRequest{Title: "T", Author: "A", Isbn: "", Price: 5, Stock: 1}},
		{"price zero", &pb.CreateBookRequest{Title: "T", Author: "A", Isbn: "BAD-4", Price: 0, Stock: 1}},
		{"negative price", &pb.CreateBookRequest{Title: "T", Author: "A", Isbn: "BAD-5", Price: -3, Stock: 1}},
		{"NaN price", &pb.CreateBookRequest{Title: "T", Author: "A", Isbn: "BAD-8", Price: math.NaN(), Stock: 1}},
		{"infinite price", &pb.CreateBookRequest{Title: "T", Author: "A", Isbn: "BAD-9", Price: math.Inf(1), Stock: 1}},
		{"negative stock", &pb.CreateBookRequest{Title: "T", Author: "A", Isbn: "BAD-6", Price: 5, Stock: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ts.client.CreateBook(rpcContext(t), tc.req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}

	if countBooks(t, ts.db) != 3 {
		t.Errorf("an invalid CreateBook inserted a row: expected 3 rows, got %d", countBooks(t, ts.db))
	}
}

// Case 6: CreateBook with an existing ISBN -> AlreadyExists, no duplicate row.
func TestCreateBookDuplicateISBN(t *testing.T) {
	ts := newTestServer(t)

	// 978-0134190440 is the ISBN of sample book 1.
	_, err := ts.client.CreateBook(rpcContext(t), &pb.CreateBookRequest{
		Title: "Copy", Author: "A", Isbn: "978-0134190440", Price: 5, Stock: 1,
	})
	expectCode(t, err, codes.AlreadyExists)

	var withISBN int
	err = ts.db.QueryRow("SELECT COUNT(*) FROM books WHERE isbn = ?", "978-0134190440").Scan(&withISBN)
	if err != nil || withISBN != 1 {
		t.Errorf("expected exactly 1 row with this isbn, got %d (err=%v)", withISBN, err)
	}
	if countBooks(t, ts.db) != 3 {
		t.Errorf("a duplicate CreateBook must not add a row")
	}
}

// ---------------------------------------------------------------------------
// Recommended cases: server unavailable, restart, deadline, database failure
// ---------------------------------------------------------------------------

// Case 7: no server is listening -> the client gets Unavailable (or
// DeadlineExceeded, depending on timing).
func TestServerUnavailable(t *testing.T) {
	// Find a port that is certainly free: listen on port 0, remember the
	// address the system chose, then close the listener. Nobody listens there now.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	deadAddr := listener.Addr().String()
	listener.Close()

	conn, err := grpc.NewClient(deadAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("cannot create client: %v", err)
	}
	defer conn.Close()
	client := pb.NewBookCatalogClient(conn)

	_, err = client.GetBook(rpcContext(t), &pb.GetBookRequest{BookId: 1})
	code := status.Code(err)
	if code != codes.Unavailable && code != codes.DeadlineExceeded {
		t.Fatalf("expected Unavailable or DeadlineExceeded, got %v (error: %v)", code, err)
	}
}

// Case 8: data created before a "restart" is still there afterwards.
func TestPersistenceAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persist.db")

	// First "server run": create a book, then shut everything down.
	ts1 := startTestServer(t, dbPath)
	created := createTestBook(t, ts1, "LAB06-PERSIST", 7)
	ts1.stop()

	// Second "server run": a brand-new server and database handle on the SAME file.
	ts2 := startTestServer(t, dbPath)
	res, err := ts2.client.GetBook(rpcContext(t), &pb.GetBookRequest{BookId: created.GetId()})
	if err != nil {
		t.Fatalf("GetBook after restart failed: %v", err)
	}
	if res.GetBook().GetIsbn() != "LAB06-PERSIST" || res.GetBook().GetStock() != 7 {
		t.Errorf("data changed after restart: %v", res.GetBook())
	}

	// 3 samples + 1 created: the samples were NOT inserted a second time.
	if countBooks(t, ts2.db) != 4 {
		t.Errorf("expected 4 rows after restart, got %d", countBooks(t, ts2.db))
	}
}

// A call whose deadline is already over fails with DeadlineExceeded.
func TestExpiredDeadline(t *testing.T) {
	ts := newTestServer(t)

	// A 1-nanosecond timeout has expired before the call even starts.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	_, err := ts.client.GetBook(ctx, &pb.GetBookRequest{BookId: 1})
	expectCode(t, err, codes.DeadlineExceeded)
}

// An unexpected database failure (not "not found", not a duplicate) -> Internal.
func TestDatabaseFailureReturnsInternal(t *testing.T) {
	ts := newTestServer(t)
	ts.db.Close() // Simulate a broken database.

	_, err := ts.client.GetBook(rpcContext(t), &pb.GetBookRequest{BookId: 1})
	expectCode(t, err, codes.Internal)

	// A failed INSERT that is not a duplicate must not be reported as AlreadyExists.
	_, err = ts.client.CreateBook(rpcContext(t), &pb.CreateBookRequest{
		Title: "T", Author: "A", Isbn: "NEW-1", Price: 5, Stock: 1,
	})
	expectCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// Section 7 - CheckAvailability (ADAPT)
// ---------------------------------------------------------------------------

// Quantity below, equal to and greater than the stock. Stock is 5.
// Also proves that CheckAvailability never changes the database.
func TestCheckAvailabilityBoundaries(t *testing.T) {
	ts := newTestServer(t)
	book := createTestBook(t, ts, "AVAIL-1", 5)
	rowsBefore := countBooks(t, ts.db)

	cases := []struct {
		name          string
		quantity      int32
		wantAvailable bool
	}{
		{"quantity below stock (3)", 3, true},
		{"quantity equal to stock (5)", 5, true},
		{"quantity greater than stock (6)", 6, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ts.client.CheckAvailability(rpcContext(t), &pb.CheckAvailabilityRequest{
				BookId: book.GetId(), Quantity: tc.quantity,
			})
			// Not enough stock is a normal answer, NOT an error.
			if err != nil {
				t.Fatalf("CheckAvailability returned an error: %v", err)
			}
			if res.GetAvailable() != tc.wantAvailable || res.GetCurrentStock() != 5 {
				t.Errorf("expected available=%v current_stock=5, got available=%v current_stock=%d",
					tc.wantAvailable, res.GetAvailable(), res.GetCurrentStock())
			}
			// Read-only: the stored stock is unchanged after every check.
			if got := stockInDB(t, ts.db, book.GetId()); got != 5 {
				t.Errorf("CheckAvailability changed the stock to %d", got)
			}
		})
	}

	if countBooks(t, ts.db) != rowsBefore {
		t.Errorf("CheckAvailability changed the number of rows")
	}
}

// Invalid book id, invalid quantity and a missing book.
func TestCheckAvailabilityInvalidAndMissing(t *testing.T) {
	ts := newTestServer(t)

	cases := []struct {
		name string
		req  *pb.CheckAvailabilityRequest
		want codes.Code
	}{
		{"book id 0", &pb.CheckAvailabilityRequest{BookId: 0, Quantity: 1}, codes.InvalidArgument},
		{"negative book id", &pb.CheckAvailabilityRequest{BookId: -1, Quantity: 1}, codes.InvalidArgument},
		{"quantity 0", &pb.CheckAvailabilityRequest{BookId: 1, Quantity: 0}, codes.InvalidArgument},
		{"negative quantity", &pb.CheckAvailabilityRequest{BookId: 1, Quantity: -2}, codes.InvalidArgument},
		{"unknown book", &pb.CheckAvailabilityRequest{BookId: 9999, Quantity: 1}, codes.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ts.client.CheckAvailability(rpcContext(t), tc.req)
			expectCode(t, err, tc.want)
		})
	}

	// None of these calls may have changed the stored stock of book 1 (10).
	if got := stockInDB(t, ts.db, 1); got != 10 {
		t.Errorf("stock of book 1 changed to %d", got)
	}
}
