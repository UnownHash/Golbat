#!/bin/sh
# Regenerate the Go bindings for the protos under grpc/.
# Requires protoc, protoc-gen-go and protoc-gen-go-grpc on PATH.
set -e

protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    grpc/raw_receiver.proto

protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    grpc/api.proto

protoc --go_out=. --go_opt=paths=source_relative \
    grpc/pokemon_internal.proto
