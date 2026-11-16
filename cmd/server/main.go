// cmd/server/main.go
//
// Entry point of the gRPC server. Start it from the project root with:
//
//	go run ./cmd/server
//
// main() wires the pieces together:
//
//	SQLite file (catalog.db) <- Store (store.go) <- catalogServer (service.go)
//	<- generated registration code <- gRPC server listening on a TCP port
package main

import (
	"log"
	"net"

	"google.golang.org/grpc"

	pb "book-catalog-grpc/gen/catalogpb"
)

// Constants: values that never change while the program runs.
// catalog.db is a NEW file, separate from the database of Lab 05.
const (
	databaseFile  = "catalog.db"
	serverAddress = "127.0.0.1:50062"
)

func main() {
	// 1. Open the database: creates catalog.db and the books table if needed
	//    and inserts the sample books only if the table is empty.
	db, err := OpenDatabase(databaseFile)
	if err != nil {
		// log.Fatalf prints the message and stops the program.
		log.Fatalf("cannot open database: %v", err)
	}
	defer db.Close()

	// 2. Start listening for TCP connections on 127.0.0.1:50062.
	//    Listening on 127.0.0.1 means only programs on this computer can connect.
	listener, err := net.Listen("tcp", serverAddress)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", serverAddress, err)
	}

	// 3. Create the gRPC server and register our implementation.
	//    RegisterBookCatalogServer is GENERATED code. It tells the gRPC server:
	//    "calls to /catalog.BookCatalog/<method> go to the methods of catalogServer".
	//    We use plain (insecure) transport: acceptable only for this local lab;
	//    a production service would use TLS.
	grpcServer := grpc.NewServer()
	pb.RegisterBookCatalogServer(grpcServer, &catalogServer{store: &Store{DB: db}})

	log.Printf("Book catalog gRPC server listening on %s (database: %s)", serverAddress, databaseFile)

	// 4. Serve blocks (keeps running) and handles each incoming RPC until the
	//    server fails or the program is stopped with Ctrl+C.
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
