#!/bin/sh
# Regenerate pogo/vbase.pb.go from a vbase.proto placed in the repo root
# (not committed; take it from the upstream game-proto dump).
# Requires protoc and protoc-gen-go on PATH.
set -e

protoc --go_out=pogo --go_opt=paths=source_relative --go_opt=Mvbase.proto=golbat.com/pogo vbase.proto
