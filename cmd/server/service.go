
package main

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "book-catalog-grpc/gen/catalogpb"
)

// This file contains the four operations that the Book Catalog server
// provides to clients:
//   - GetBook: find one book using its ID.
//   - ListBooks: display books in pages.
//   - CreateBook: add a new book to the catalog.
//   - CheckAvailability: check whether enough copies are in stock.
//
// Each operation checks the received information, asks the Store to
// retrieve or save the required data, and returns the result to the client.
// The Store handles the actual database operations.

// Limit the number of books that can be requested on one page.
// Even if a client requests more than 50 books, the server returns at most 50.
const maxPageSize = 50

// This is the server-side part of the Book Catalog.
// It handles incoming requests and uses Store to access the book database.
type catalogServer struct {
	// Keep the generated base server required to register this implementation.
	// The actual book operations are handled by the methods below.
	pb.UnimplementedBookCatalogServer
	store *Store
}

// Convert problems from the Store into error messages that the client
// can understand.
//
//	ErrNotFound      -> the requested book does not exist.
//	ErrDuplicateISBN -> another book already uses this ISBN.
//	Deadline exceeded -> the request took too long.
//	Cancelled        -> the request was cancelled.
//	Other errors     -> an unexpected problem occurred.
//
// Unexpected details are recorded in the server log, while the client
// receives a general error message.
func toStatusError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "book not found")
	case errors.Is(err, ErrDuplicateISBN):
		return status.Error(codes.AlreadyExists, "a book with this isbn already exists")
	case errors.Is(err, context.DeadlineExceeded):
		// Inform the client when the request takes longer than its allowed time.
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	case errors.Is(err, context.Canceled):
		// Inform the client when its request has been cancelled.
		return status.Error(codes.Canceled, "request cancelled")
	default:
		// Record unexpected problems so they can be investigated later.
		// Do not expose internal database details to the client.
		log.Printf("unexpected error: %v", err)
		return status.Error(codes.Internal, "internal server error")
	}
}

// GetBook finds and returns one book using its ID.
//
// The server first checks that the ID is valid, then asks the Store
// to find the matching book in the database.
// If the ID is invalid or the book does not exist, an appropriate
// error is returned instead of a book.
func (s *catalogServer) GetBook(ctx context.Context, req *pb.GetBookRequest) (*pb.GetBookResponse, error) {
	// A valid book ID must be greater than zero.
	if req.GetBookId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "book_id must be greater than 0")
	}

	// Find the requested book in the database.
	book, err := s.store.GetBook(ctx, req.GetBookId())
	if err != nil {
		return nil, toStatusError(err)
	}

	// Return the book found by the Store to the client.
	return &pb.GetBookResponse{Book: book}, nil
}

// ListBooks returns a selected page of books together with the total
// number of books in the catalog.
//
// Page numbering starts at 1. PageSize controls how many books are
// returned on each page, with a maximum of 50 books per page.
// Invalid page numbers or page sizes are rejected.
func (s *catalogServer) ListBooks(ctx context.Context, req *pb.ListBooksRequest) (*pb.ListBooksResponse, error) {
	// Page numbers must start from 1.
	if req.GetPage() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "page must be greater than 0")
	}

	// The number of books requested on a page must also be positive.
	if req.GetPageSize() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be greater than 0")
	}

	// Limit each page to 50 books to prevent clients from requesting
	// an unnecessarily large number of records at once.
	pageSize := req.GetPageSize()
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	// Ask the Store to retrieve the requested page and count all books
	// currently in the catalog.
	books, total, err := s.store.ListBooks(ctx, req.GetPage(), pageSize)
	if err != nil {
		return nil, toStatusError(err)
	}

	// Return the selected books and the total catalog count to the client.
	return &pb.ListBooksResponse{Books: books, Total: total}, nil
}

// CreateBook checks the new book's information, saves it in the database,
// and returns the saved book with its automatically generated ID.
//
// A book must have a title, author and ISBN. Its price must be greater
// than zero, and its stock cannot be negative.
// An ISBN that is already used by another book is rejected.
func (s *catalogServer) CreateBook(ctx context.Context, req *pb.CreateBookRequest) (*pb.CreateBookResponse, error) {
	// Remove unnecessary spaces around the title, author and ISBN.
	// This also allows the server to recognize text containing only spaces
	// as empty information.
	title := strings.TrimSpace(req.GetTitle())
	author := strings.TrimSpace(req.GetAuthor())
	isbn := strings.TrimSpace(req.GetIsbn())

	// A book cannot be saved without a title.
	if title == "" {
		return nil, status.Error(codes.InvalidArgument, "title must not be blank")
	}

	// A book must have an author.
	if author == "" {
		return nil, status.Error(codes.InvalidArgument, "author must not be blank")
	}

	// Every book must have an ISBN so that it can be identified
	// and checked against other books in the catalog.
	if isbn == "" {
		return nil, status.Error(codes.InvalidArgument, "isbn must not be blank")
	}

	// Reject prices that are zero, negative or otherwise invalid.
	// A valid book price must be a finite number greater than zero.
	price := req.GetPrice()
	if !(price > 0) || math.IsInf(price, 0) {
		return nil, status.Error(codes.InvalidArgument, "price must be a finite number greater than 0")
	}

	// The number of copies in stock cannot be negative.
	if req.GetStock() < 0 {
		return nil, status.Error(codes.InvalidArgument, "stock must not be negative")
	}

	// All information is valid. Ask the Store to save the new book.
	// The database generates its ID and returns the saved book information.
	book, err := s.store.CreateBook(ctx, title, author, isbn, price, req.GetStock())
	if err != nil {
		return nil, toStatusError(err)
	}

	// Return the newly created book, including its generated ID.
	return &pb.CreateBookResponse{Book: book}, nil
}

// CheckAvailability checks whether the catalog has enough copies
// of a particular book to fulfill a requested quantity.
//
// This operation only checks the current stock. It does not reserve,
// deduct or otherwise modify any copies in the database.
//
// If enough copies are available, the response contains available = true.
// If there are not enough copies, it returns available = false.
// Not having enough stock is a normal result, not an error.
func (s *catalogServer) CheckAvailability(ctx context.Context, req *pb.CheckAvailabilityRequest) (*pb.CheckAvailabilityResponse, error) {
	// The requested book must have a valid, positive ID.
	if req.GetBookId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "book_id must be greater than 0")
	}

	// The requested quantity must be greater than zero.
	if req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be greater than 0")
	}

	// Find the book so we can check how many copies are currently in stock.
	// This only retrieves information; it does not change the stock.
	book, err := s.store.GetBook(ctx, req.GetBookId())
	if err != nil {
		return nil, toStatusError(err)
	}

	// Compare the requested quantity with the current stock.
	// Return the availability result together with the current stock count.
	return &pb.CheckAvailabilityResponse{
		Available:    book.GetStock() >= req.GetQuantity(),
		CurrentStock: book.GetStock(),
	}, nil
}
