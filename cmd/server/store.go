
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	// Enable the application to work with SQLite databases.
	_ "github.com/mattn/go-sqlite3"

	// Book information uses the same structure defined in catalog.proto,
	// so the Store can return book details directly to the service.
	pb "book-catalog-grpc/gen/catalogpb"
)

// These errors describe common problems that can happen when working
// with the book database.
// service.go uses them to decide which error message should be returned
// to the client.
var (
	ErrNotFound      = errors.New("book not found")
	ErrDuplicateISBN = errors.New("duplicate isbn")
)

// Define the structure of the books table.
// Each column stores one piece of information about a book.
//
// The database also enforces several rules:
//   - Each book receives its own automatically generated ID.
//   - A book must have a title, author and ISBN.
//   - Every ISBN must be unique.
//   - The price must be greater than zero.
//   - The stock cannot be negative.
const createBooksTable = `
CREATE TABLE IF NOT EXISTS books (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	title  TEXT    NOT NULL,
	author TEXT    NOT NULL,
	isbn   TEXT    NOT NULL UNIQUE,
	price  REAL    NOT NULL CHECK (price > 0),
	stock  INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0)
);`

// Open the book database and prepare it for the application.
//
// This function is called when the server starts. It creates catalog.db
// if the file does not exist, prepares the books table and adds the three
// sample books when the table is empty.
//
// Existing books are kept when the server starts again.
func OpenDatabase(path string) (*sql.DB, error) {
	// Open the database file. If the file does not exist yet, SQLite creates it.
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Use one database connection at a time to avoid conflicts when
	// several requests try to access the SQLite database.
	db.SetMaxOpenConns(1)

	// Prepare the database during server startup, before any client requests
	// have been received.
	ctx := context.Background()

	// Check that the database can actually be accessed.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	// Create the books table if it has not already been created.
	// Existing tables and their records are left untouched.
	if _, err := db.ExecContext(ctx, createBooksTable); err != nil {
		db.Close()
		return nil, fmt.Errorf("create books table: %w", err)
	}

	// Add the sample books if this is an empty catalog.
	if err := seedBooks(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("insert sample books: %w", err)
	}

	return db, nil
}

// Add three sample books to the catalog the first time the database is used.
//
// Before inserting anything, check whether the books table already contains
// records. If it does, leave the existing data alone.
// This prevents the same sample books from being added again every time
// the server restarts.
func seedBooks(ctx context.Context, db *sql.DB) error {
	// Count how many books are currently stored in the database.
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM books").Scan(&count); err != nil {
		return err
	}

	// The catalog already contains books, so there is no need to add samples.
	if count > 0 {
		return nil
	}

	// The sample information used to prepare the initial book catalog.
	samples := []struct {
		title  string
		author string
		isbn   string
		price  float64
		stock  int
	}{
		{"The Go Programming Language", "Alan Donovan and Brian Kernighan", "978-0134190440", 39.99, 10},
		{"Learning Go", "Jon Bodner", "978-1492077213", 44.50, 5},
		{"Concurrency in Go", "Katherine Cox-Buday", "978-1491941195", 35.00, 3},
	}

	// Add each sample book to the database.
	// The book details are supplied separately from the SQL statement,
	// so their contents are treated as data rather than SQL instructions.
	for _, s := range samples {
		_, err := db.ExecContext(ctx,
			"INSERT INTO books (title, author, isbn, price, stock) VALUES (?, ?, ?, ?, ?)",
			s.title, s.author, s.isbn, s.price, s.stock)
		if err != nil {
			return err
		}
	}
	return nil
}

// Store provides the functions used by the server to access book information.
// Instead of writing database queries directly in service.go,
// the server calls the appropriate function from this Store.
type Store struct {
	DB *sql.DB
}

// Check whether a database error was caused by someone trying to use
// an ISBN that already belongs to another book.
// Other database problems are not treated as duplicate ISBN errors.
func isDuplicateISBN(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: books.isbn")
}

// Find a book in the database using its ID.
//
// If a matching book exists, return its information.
// If no book has that ID, return ErrNotFound so the service can tell
// the client that the requested book does not exist.
// Other database problems are returned separately.
func (s *Store) GetBook(ctx context.Context, id int32) (*pb.Book, error) {
	// Search for the book with the requested ID and retrieve its information.
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, title, author, isbn, price, stock FROM books WHERE id = ?", id)

	// Prepare a Book to hold the information retrieved from the database.
	book := &pb.Book{}
	err := row.Scan(&book.Id, &book.Title, &book.Author, &book.Isbn, &book.Price, &book.Stock)

	// No matching book was found.
	// Return a specific error so the service can report NotFound to the client.
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	// Return any other database problem without treating it as a missing book.
	if err != nil {
		return nil, err
	}

	// Return the complete book information.
	return book, nil
}

// Retrieve a page of books from the catalog and return the total number
// of books currently stored in the database.
//
// For example, if there are 10 books and the client requests page 2
// with a page size of 3, this function returns books 4, 5 and 6.
// The total count is still 10, even though only three books are returned.
//
// Books are always arranged by ID so that their order remains consistent.
// A request for a page beyond the available books simply returns an empty list.
func (s *Store) ListBooks(ctx context.Context, page, pageSize int32) ([]*pb.Book, int32, error) {
	// Count all books in the catalog, regardless of which page is requested.
	var total int32
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM books").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Calculate how many books should be skipped before retrieving this page.
	// Page 1 skips no books; Page 2 skips the first page; Page 3 skips
	// the first two pages, and so on.
	offset := (int64(page) - 1) * int64(pageSize)

	// Retrieve only the books belonging to the requested page.
	// ORDER BY id keeps the books in a consistent order.
	// LIMIT controls how many books are returned, while OFFSET determines
	// how many earlier books are skipped.
	rows, err := s.DB.QueryContext(ctx,
		"SELECT id, title, author, isbn, price, stock FROM books ORDER BY id LIMIT ? OFFSET ?",
		pageSize, offset)
	if err != nil {
		return nil, 0, err
	}

	// Release the database results after all requested books have been read.
	defer rows.Close()

	// Prepare an empty list to hold the books returned for this page.
	books := []*pb.Book{}

	// Read each book from the database and add it to the result list.
	for rows.Next() {
		book := &pb.Book{}
		err := rows.Scan(&book.Id, &book.Title, &book.Author, &book.Isbn, &book.Price, &book.Stock)
		if err != nil {
			return nil, 0, err
		}
		books = append(books, book)
	}

	// Check whether a problem occurred while reading the list of books.
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Return the books on the requested page and the total number
	// of books in the entire catalog.
	return books, total, nil
}

// Save a new book in the database and return its complete information.
//
// The book details have already been checked by CreateBook in service.go.
// This function handles saving the information and retrieving the
// automatically generated ID.
func (s *Store) CreateBook(ctx context.Context, title, author, isbn string, price float64, stock int32) (*pb.Book, error) {
	// Save the new book using the information received from the service.
	// The database automatically generates an ID for the new record.
	result, err := s.DB.ExecContext(ctx,
		"INSERT INTO books (title, author, isbn, price, stock) VALUES (?, ?, ?, ?, ?)",
		title, author, isbn, price, stock)
	if err != nil {
		// Check whether the problem is caused by an ISBN that already exists.
		// Return ErrDuplicateISBN so the service can report AlreadyExists.
		if isDuplicateISBN(err) {
			return nil, ErrDuplicateISBN
		}
		return nil, err
	}

	// Get the ID assigned to the book that was just inserted.
	newID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	// Retrieve the saved book using its new ID.
	// This allows the server to return the complete information
	// exactly as it is stored in the database.
	return s.GetBook(ctx, int32(newID))
}
