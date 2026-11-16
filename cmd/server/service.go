// cmd/server/service.go
//
// The gRPC service: the four RPC methods of the BookCatalog service.
//
// Request flow for one call:
//
//	client stub -> network (HTTP/2 + protobuf) -> generated server code
//	-> a method in THIS file -> a Store function (store.go) -> SQLite
//
// Each method does the same three things:
//  1. validate the request          -> codes.InvalidArgument if it is wrong
//  2. call the Store                -> SQL runs here
//  3. turn the result into a response, or an error into a gRPC status code
//
// gRPC does not use HTTP status codes like 404. Errors are returned as a
// "status" with a code from google.golang.org/grpc/codes.
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

// maxPageSize is the largest number of books ListBooks returns per page.
const maxPageSize = 50

// catalogServer implements the BookCatalog service.
//
// pb.UnimplementedBookCatalogServer is EMBEDDED (written without a field name).
// Embedding copies its methods into catalogServer. The generated code requires
// it: it makes catalogServer satisfy the generated pb.BookCatalogServer
// interface, and any RPC that we forget to implement answers "Unimplemented"
// instead of crashing. Our own methods below override the embedded ones.
type catalogServer struct {
	pb.UnimplementedBookCatalogServer
	store *Store
}

// toStatusError converts an error returned by the Store into a gRPC status error.
// This one function is our "error policy":
//
//	ErrNotFound         -> codes.NotFound
//	ErrDuplicateISBN    -> codes.AlreadyExists
//	deadline exceeded   -> codes.DeadlineExceeded
//	cancelled           -> codes.Canceled
//	anything else       -> codes.Internal
//
// For unexpected errors the real message is only logged on the server; the
// client gets a generic message, so internal details are not leaked.
func toStatusError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "book not found")
	case errors.Is(err, ErrDuplicateISBN):
		return status.Error(codes.AlreadyExists, "a book with this isbn already exists")
	case errors.Is(err, context.DeadlineExceeded):
		// The RPC's context ran out of time while the database was working.
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request cancelled")
	default:
		log.Printf("unexpected error: %v", err)
		return status.Error(codes.Internal, "internal server error")
	}
}

// GetBook returns one book by id.
//
// ctx: created by gRPC for this call. It carries the deadline the client set
// (context.WithTimeout on the client side) and is cancelled if the client gives up.
// req: the decoded protobuf request message. GetBookId() is a generated getter.
//
// Returns the book, or InvalidArgument (id <= 0), NotFound, or Internal.
func (s *catalogServer) GetBook(ctx context.Context, req *pb.GetBookRequest) (*pb.GetBookResponse, error) {
	// A missing field in protobuf arrives as 0, so id 0 also means "not set".
	if req.GetBookId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "book_id must be greater than 0")
	}

	book, err := s.store.GetBook(ctx, req.GetBookId())
	if err != nil {
		return nil, toStatusError(err)
	}
	return &pb.GetBookResponse{Book: book}, nil
}

// ListBooks returns one page of books and the total number of books.
//
// req.Page is 1-based. req.PageSize is capped at maxPageSize (50): a bigger
// request is not an error, the server just returns at most 50 books.
// Returns InvalidArgument if page or page_size is 0 or negative.
func (s *catalogServer) ListBooks(ctx context.Context, req *pb.ListBooksRequest) (*pb.ListBooksResponse, error) {
	if req.GetPage() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "page must be greater than 0")
	}
	if req.GetPageSize() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be greater than 0")
	}

	pageSize := req.GetPageSize()
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	books, total, err := s.store.ListBooks(ctx, req.GetPage(), pageSize)
	if err != nil {
		return nil, toStatusError(err)
	}
	// "Books" is a repeated field, so in Go it is the slice []*pb.Book.
	return &pb.ListBooksResponse{Books: books, Total: total}, nil
}

// CreateBook validates and stores a new book, then returns it with its new id.
//
// The client cannot choose the id (the request has no id field); SQLite
// generates it. Returns InvalidArgument for a blank title / author / isbn, a
// price that is not > 0 or a negative stock; AlreadyExists for a duplicate isbn.
func (s *catalogServer) CreateBook(ctx context.Context, req *pb.CreateBookRequest) (*pb.CreateBookResponse, error) {
	// TrimSpace removes spaces around the text, so "   " counts as blank.
	// We store the trimmed values.
	title := strings.TrimSpace(req.GetTitle())
	author := strings.TrimSpace(req.GetAuthor())
	isbn := strings.TrimSpace(req.GetIsbn())

	if title == "" {
		return nil, status.Error(codes.InvalidArgument, "title must not be blank")
	}
	if author == "" {
		return nil, status.Error(codes.InvalidArgument, "author must not be blank")
	}
	if isbn == "" {
		return nil, status.Error(codes.InvalidArgument, "isbn must not be blank")
	}
	// A protobuf double can also carry the special values NaN ("not a number")
	// and +/-Infinity. "!(price > 0)" rejects zero, negatives AND NaN (every
	// comparison with NaN is false); math.IsInf rejects +Infinity, which would
	// otherwise pass "price > 0".
	price := req.GetPrice()
	if !(price > 0) || math.IsInf(price, 0) {
		return nil, status.Error(codes.InvalidArgument, "price must be a finite number greater than 0")
	}
	if req.GetStock() < 0 {
		return nil, status.Error(codes.InvalidArgument, "stock must not be negative")
	}

	book, err := s.store.CreateBook(ctx, title, author, isbn, price, req.GetStock())
	if err != nil {
		return nil, toStatusError(err)
	}
	return &pb.CreateBookResponse{Book: book}, nil
}

// CheckAvailability tells whether `quantity` copies of a book could be supplied.
// It is READ-ONLY: it only runs a SELECT (through Store.GetBook) and never
// changes the stock. Another service calls it before placing an order.
//
// Returns InvalidArgument (book_id <= 0 or quantity <= 0), NotFound (no such
// book) or a response. "Not enough stock" is NOT an error: it is a normal
// answer, available = false, together with the current stock.
func (s *catalogServer) CheckAvailability(ctx context.Context, req *pb.CheckAvailabilityRequest) (*pb.CheckAvailabilityResponse, error) {
	if req.GetBookId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "book_id must be greater than 0")
	}
	if req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be greater than 0")
	}

	book, err := s.store.GetBook(ctx, req.GetBookId())
	if err != nil {
		return nil, toStatusError(err)
	}
	return &pb.CheckAvailabilityResponse{
		Available:    book.GetStock() >= req.GetQuantity(),
		CurrentStock: book.GetStock(),
	}, nil
}
