This folder must contain the two files that protoc generates:

    catalog.pb.go
    catalog_grpc.pb.go

They are NOT in this ZIP: the environment that produced this project had no
protoc / Go toolchain, and generated files must come from the real tools (they
must never be written or edited by hand).

Create them from the project root with:    .\generate.ps1
Then run:                                  go mod tidy
