#!/bin/bash
# Regenerate the thinned pogo variant (pogo/vbase.thin.pb.go) from the full
# schema, keeping only the fields Golbat's code accesses. MAINTAINER-ONLY: needs
# the vbase.proto source (not shipped — license) and protoc. End users never run
# this; they compile the committed pogo/vbase.thin.pb.go via `-tags thin`.
#
# Pipeline:
#   1. ensure the full pogo/vbase.pb.go carries //go:build !thin
#   2. protoc -> full descriptor set
#   3. tools/protofields  -> the (message,field) set Golbat accesses (incl. tests,
#                            so the suite compiles+runs under -tags thin)
#   4. tools/protofields/prototrim -> thinned descriptor set (field numbers kept)
#   5. protoc --descriptor_set_in -> pogo/vbase.thin.pb.go (//go:build thin)
#
# After running, verify:  go build ./... && go build -tags thin ./... &&
#                         go test ./... && go test -tags thin ./...
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

PROTO_SRC="${PROTO_SRC:-$HOME/dev/ProtoMirror/vbase.proto}"
[ -f "$PROTO_SRC" ] || { echo "vbase.proto not found: $PROTO_SRC (set PROTO_SRC)" >&2; exit 1; }
command -v protoc >/dev/null || { echo "protoc not found" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
PROTO_DIR="$(dirname "$PROTO_SRC")"
PROTO_FILE="$(basename "$PROTO_SRC")"

echo ">> pinning protoc-gen-go"
GOBIN="$WORK/bin" go -C tools/protofields install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11

# 1. build tag on the full variant (idempotent)
if ! head -1 pogo/vbase.pb.go | grep -q '//go:build !thin'; then
	echo ">> adding //go:build !thin to pogo/vbase.pb.go"
	{ echo "//go:build !thin"; echo; cat pogo/vbase.pb.go; } > "$WORK/full.tagged"
	mv "$WORK/full.tagged" pogo/vbase.pb.go
fi

echo ">> [2/5] full descriptor set"
protoc -I "$PROTO_DIR" --descriptor_set_out="$WORK/full.desc" "$PROTO_SRC"

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
