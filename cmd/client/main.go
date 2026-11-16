// cmd/client/main.go
//
// This program demonstrates how a user interacts with the Book Catalog system.
// It connects to the server and demonstrates how to retrieve books, view a list
// of books, add a new book, and check whether enough copies are available.
//
// It also sends some invalid requests to demonstrate how the system handles
// problems such as invalid book IDs, missing books and duplicate ISBNs.
//
// Before running this program, start the server first:
//
//     go run ./cmd/server
//
// Then run this client from the project root:
//
//     go run ./cmd/client
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "book-catalog-grpc/gen/catalogpb"
)

// Set the maximum waiting time for each request.
// If the server does not respond within 2 seconds, the client stops waiting.
const rpcTimeout = 2 * time.Second

// Give each request its own 2-second waiting period.
// A new waiting period is created for every request so that one slow request
// does not affect the time available for the next request.
//
// The returned cancel function is called after each request to stop its timer.
func newRPCContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), rpcTimeout)
}

// Display information about a failed request, including the type of error
// reported by the server and a short explanation of what went wrong.
func showError(what string, err error) {
	fmt.Printf("  %-34s -> code=%v message=%q\n", what, status.Code(err), status.Convert(err).Message())
}

// Display a book's information in a readable format.
// The information comes from the book returned by the server.
func printBook(b *pb.Book) {
	fmt.Printf("  book id=%d title=%q author=%q isbn=%s price=%.2f stock=%d\n",
		b.GetId(), b.GetTitle(), b.GetAuthor(), b.GetIsbn(), b.GetPrice(), b.GetStock())
}

func main() {
	// Set the address of the server that this client will communicate with.
	// The default address is the same one used by the Book Catalog server.
	// A different address can be provided when running the program.
	addr := flag.String("addr", "127.0.0.1:50062", "address of the gRPC server")
	flag.Parse()

	// Prepare a connection to the Book Catalog server.
	// This connection will be used to send requests and receive responses.
	// Encryption is disabled because this project is only running locally
	// for laboratory purposes.
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("cannot create client: %v", err)
	}
	defer conn.Close()

	// Prepare the client used to access the Book Catalog's available operations.
	// It provides the methods for retrieving, listing, creating and checking books.
	client := pb.NewBookCatalogClient(conn)

	// ---- 1. GetBook: retrieve one existing book ----------------------------

	fmt.Println("1. GetBook(book_id=1)")

	// Ask the server to find the book with ID 1 and return its information.
	ctx, cancel := newRPCContext()
	getRes, err := client.GetBook(ctx, &pb.GetBookRequest{BookId: 1})
	cancel()
	if err != nil {
		// Stop the demonstration if the first request fails.
		// This may happen if the server is not running or the requested book
		// cannot be found in the database.
		showError("GetBook(1)", err)
		log.Fatalf("stopping: is the server running at %s?", *addr)
	}

	// Display the information of the book returned by the server.
	printBook(getRes.GetBook())

	// ---- 2. ListBooks: page 1 with 2 books --------------------------------

	// Request the first page of the book catalog, showing up to two books.
	// Page determines which part of the book list to retrieve.
	// PageSize determines how many books to include on each page.
	//
	// For example, with a page size of 2:
	// Page 1 displays the first two books.
	// Page 2 displays the next two books.
	// Page 3 displays the remaining books.
	fmt.Println("2. ListBooks(page=1, page_size=2)")
	ctx, cancel = newRPCContext()
	listRes, err := client.ListBooks(ctx, &pb.ListBooksRequest{Page: 1, PageSize: 2})
	cancel()
	if err != nil {
		log.Fatalf("ListBooks failed: code=%v message=%v", status.Code(err), err)
	}

	// Display each book returned on the requested page.
	for _, b := range listRes.GetBooks() {
		printBook(b)
	}

	// Display the total number of books stored in the catalog.
	// This includes books on other pages that were not displayed above.
	fmt.Printf("  total books in the catalog: %d\n", listRes.GetTotal())

	// ---- 3. CreateBook: add a new book ------------------------------------

	// Generate a unique demonstration ISBN using the current time.
	// This helps prevent duplicate ISBN errors when running the client
	// multiple times and creating new books.
	isbn := fmt.Sprintf("DEMO-%d", time.Now().UnixNano())

	fmt.Println("3. CreateBook(...)")

	// Send the new book's information to the server.
	// The server checks the supplied information and saves the book
	// in the database if all the required information is valid.
	//
	// The client does not provide a book ID because the system generates
	// one automatically when a new book is created.
	ctx, cancel = newRPCContext()
	createRes, err := client.CreateBook(ctx, &pb.CreateBookRequest{
		Title: "Something new", Author: "A. Teacher", Isbn: isbn, Price: 102.5, Stock: 15,
	})
	cancel()
	if err != nil {
		log.Fatalf("CreateBook failed: code=%v message=%v", status.Code(err), err)
	}

	// Receive the newly created book and display its information.
	// Keep the returned book because its ID will be used in later requests.
	created := createRes.GetBook()
	printBook(created)

	// ---- 4. GetBook again: was it really stored? --------------------------

	fmt.Println("4. GetBook(<id returned by CreateBook>)")

	// Use the ID of the book created in the previous step to retrieve it again.
	// This checks whether the new book can be found after it has been saved.
	ctx, cancel = newRPCContext()
	getRes, err = client.GetBook(ctx, &pb.GetBookRequest{BookId: created.GetId()})
	cancel()
	if err != nil {
		log.Fatalf("GetBook of the new book failed: code=%v message=%v", status.Code(err), err)
	}

	// Display the retrieved book so its information can be compared
	// with the information returned when it was first created.
	printBook(getRes.GetBook())

	// ---- 5. CheckAvailability: below, equal to and above the stock (15) ---

	// The newly created book has 15 copies in stock.
	// Test whether the system can fulfill three different requests:
	// 3 copies  -> less than the available stock.
	// 15 copies -> exactly equal to the available stock.
	// 16 copies -> more than the available stock.
	//
	// The system should report whether the requested quantity is available
	// and the number of copies currently in stock.
	// Checking availability should not reserve or deduct any copies.
	fmt.Println("5. CheckAvailability (stock of the new book is 15)")
	for _, quantity := range []int32{3, 15, 16} {
		ctx, cancel = newRPCContext()
		availRes, err := client.CheckAvailability(ctx, &pb.CheckAvailabilityRequest{
			BookId: created.GetId(), Quantity: quantity,
		})
		cancel()
		if err != nil {
			log.Fatalf("CheckAvailability failed: code=%v message=%v", status.Code(err), err)
		}

		// Display whether the requested quantity can be fulfilled
		// and how many copies the server reports as currently available.
		// The stock should remain at 15 for all three requests.
		fmt.Printf("  quantity=%d -> available=%v current_stock=%d\n",
			quantity, availRes.GetAvailable(), availRes.GetCurrentStock())
	}

	// ---- 6. Expected errors: the server must answer with the right code ----

	// Send several requests containing invalid information or referring
	// to books that do not exist.
	// The purpose is to check whether the server rejects these requests
	// and reports the appropriate reason for each failure.
	fmt.Println("6. Expected errors")

	// Try to retrieve a book using ID 0.
	// This should be rejected because a valid book ID must be greater than 0.
	ctx, cancel = newRPCContext()
	_, err = client.GetBook(ctx, &pb.GetBookRequest{BookId: 0})
	cancel()
	showError("GetBook(0)", err) // InvalidArgument

	// Try to retrieve a book using an ID that does not exist in the catalog.
	// The request itself is valid, but the server cannot find the requested book.
	ctx, cancel = newRPCContext()
	_, err = client.GetBook(ctx, &pb.GetBookRequest{BookId: 999999})
	cancel()
	showError("GetBook(999999)", err) // NotFound

	// Try to create a book without providing a title.
	// The server should reject it because every book must have a title.
	ctx, cancel = newRPCContext()
	_, err = client.CreateBook(ctx, &pb.CreateBookRequest{Title: "", Author: "A", Isbn: "X-1", Price: 5, Stock: 1})
	cancel()
	showError("CreateBook(empty title)", err) // InvalidArgument

	// Try to create another book using an ISBN that already belongs
	// to the book created earlier.
	// The server should reject it because each book must have a unique ISBN.
	ctx, cancel = newRPCContext()
	_, err = client.CreateBook(ctx, &pb.CreateBookRequest{Title: "Copy", Author: "A", Isbn: isbn, Price: 5, Stock: 1})
	cancel()
	showError("CreateBook(duplicate isbn)", err) // AlreadyExists

	// Try to check availability using a quantity of zero.
	// The server should reject the request because the requested quantity
	// must be greater than zero.
	ctx, cancel = newRPCContext()
	_, err = client.CheckAvailability(ctx, &pb.CheckAvailabilityRequest{BookId: created.GetId(), Quantity: 0})
	cancel()
	showError("CheckAvailability(quantity=0)", err) // InvalidArgument

	// Try to check availability for a book that does not exist.
	// The server should report that the requested book cannot be found.
	ctx, cancel = newRPCContext()
	_, err = client.CheckAvailability(ctx, &pb.CheckAvailabilityRequest{BookId: 999999, Quantity: 1})
	cancel()
	showError("CheckAvailability(book 999999)", err) // NotFound

	// All four RPC methods have now been called.
	// The output above shows the returned book information and the errors
	// reported by the server for the invalid requests.
	fmt.Println("Done: all four RPCs were called.")
}