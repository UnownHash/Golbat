#!/bin/bash
# Regenerate the thinned pogo variant (pogo/vbase.thin.pb.go) from the full,
# already-generated pogo/vbase.pb.go, keeping only the fields Golbat's code
# reads. Run it whenever pogo/vbase.pb.go is regenerated (new schema) or the
# code's field usage changes.
#
# Self-contained: the full descriptor is read from the COMPILED pogo package
# (tools/dumpdesc), NOT from vbase.proto — so this needs no access to the
# licensed .proto text, and the thin variant is guaranteed a faithful subset of
# exactly what the full pogo/vbase.pb.go ships. Only `protoc` + Go are required.
#
# Pipeline:
#   1. ensure the full pogo/vbase.pb.go carries //go:build !thin
#   2. tools/dumpdesc            -> full descriptor set (from compiled pogo)
#   3. tools/protofields         -> the (message,field) set Golbat accesses (incl.
#                                   tests, so the suite compiles+runs under -tags thin)
#   4. tools/protofields/prototrim -> thinned descriptor set (field numbers kept)
#   5. protoc --descriptor_set_in  -> pogo/vbase.thin.pb.go (//go:build thin)
#
# After running, verify:  go build ./... && go build -tags thin ./... &&
#                         go test ./... && go test -tags thin ./...
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
PROTO_FILE="vbase.proto" # the file's path as recorded in the pogo descriptor

command -v protoc >/dev/null || { echo "protoc not found" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# protoc-gen-go pinned to the module's protobuf runtime, so generated code
# matches the runtime Golbat links.
PBVER="$(grep -oE 'google.golang.org/protobuf v[0-9.]+' go.mod | head -1 | grep -oE 'v[0-9.]+')"
echo ">> installing protoc-gen-go@$PBVER"
GOBIN="$WORK/bin" go install "google.golang.org/protobuf/cmd/protoc-gen-go@$PBVER"

# 1. build tag on the full variant (idempotent)
if ! head -1 pogo/vbase.pb.go | grep -q '//go:build !thin'; then
	echo ">> adding //go:build !thin to pogo/vbase.pb.go"
	{ echo "//go:build !thin"; echo; cat pogo/vbase.pb.go; } > "$WORK/full.tagged"
	mv "$WORK/full.tagged" pogo/vbase.pb.go
fi

echo ">> [2/5] full descriptor set (from compiled pogo)"
go run ./tools/dumpdesc > "$WORK/full.desc"

echo ">> [3/5] analyzing Golbat field usage"
JSON="$WORK/used.json" INCLUDE_TESTS=1 go -C tools/protofields run . "$ROOT" | sed 's/^/   /'

echo ">> [4/5] thinning descriptor"
go -C tools/protofields run ./prototrim "$WORK/full.desc" "$WORK/used.json" "$WORK/thin.desc" | sed 's/^/   /'

echo ">> [5/5] generating pogo/vbase.thin.pb.go"
mkdir -p "$WORK/out"
PATH="$WORK/bin:$PATH" protoc --descriptor_set_in="$WORK/thin.desc" \
	--go_out="$WORK/out" --go_opt=paths=source_relative --go_opt="M${PROTO_FILE}=golbat/pogo" \
	"$PROTO_FILE"
{ echo "//go:build thin"; echo; cat "$WORK/out/${PROTO_FILE%.proto}.pb.go"; } > pogo/vbase.thin.pb.go

echo ">> done. verify: go build ./... && go build -tags thin ./... && go test -tags thin ./..."
