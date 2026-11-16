This folder must contain the two files that protoc generates:

    catalog.pb.go
    catalog_grpc.pb.go
    
Create them from the project root with:    .\generate.ps1
Then run:                                  go mod tidy
