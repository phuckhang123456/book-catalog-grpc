# generate.ps1
#
# Generates the two Go files in gen/catalogpb/ from proto/catalog.proto:
#   catalog.pb.go       - the message structs (Book, GetBookRequest, ...)
#   catalog_grpc.pb.go  - the client stub and the server interface
#
# Run it from the project root, in PowerShell:   .\generate.ps1
# Run it again whenever proto/catalog.proto changes. Never edit the output.
#
# One-time tool setup (also in README.md):
#   - protoc itself (https://github.com/protocolbuffers/protobuf/releases)
#   - go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   - go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#   - the Go "bin" folder (usually %USERPROFILE%\go\bin) must be on PATH, because
#     protoc finds the two plugins by their names.

$ErrorActionPreference = "Stop"

foreach ($tool in "protoc", "protoc-gen-go", "protoc-gen-go-grpc") {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        Write-Error "'$tool' was not found on PATH. See the tool setup in README.md."
    }
}

protoc --version

# -I proto             : where to look for imported .proto files
# --go_out             : output folder for catalog.pb.go (messages)
# --go-grpc_out        : output folder for catalog_grpc.pb.go (client + server code)
# paths=source_relative: write the files directly into the output folder
protoc -I proto `
    --go_out=gen/catalogpb --go_opt=paths=source_relative `
    --go-grpc_out=gen/catalogpb --go-grpc_opt=paths=source_relative `
    proto/catalog.proto

Get-ChildItem gen/catalogpb/*.pb.go
