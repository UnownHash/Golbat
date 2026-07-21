#!/bin/bash
# Regenerates protobench/pogo from vbase.proto at API_HYBRID with lazy
# annotations. One command; rerun whenever vbase.proto updates.
set -euo pipefail
cd "$(dirname "$0")/.."

PROTO_SRC="${PROTO_SRC:-$HOME/dev/ProtoMirror/vbase.proto}"
GOLBAT_ROOT="$(cd .. && pwd)"

command -v protoc >/dev/null || { echo "protoc not found (brew install protobuf)" >&2; exit 1; }
[ -f "$PROTO_SRC" ] || { echo "proto source not found: $PROTO_SRC (set PROTO_SRC)" >&2; exit 1; }

mkdir -p bin build pogo
GOBIN="$(pwd)/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11

cp "$PROTO_SRC" build/vbase.proto
python3 scripts/add_lazy_proto.py --proto build/vbase.proto --go-src "$GOLBAT_ROOT"
echo "lazy annotations: $(grep -c 'lazy = true' build/vbase.proto)"

PATH="$(pwd)/bin:$PATH" protoc -I build \
  --go_out=pogo --go_opt=paths=source_relative \
  --go_opt=default_api_level=API_HYBRID \
  --go_opt=Mvbase.proto=protobench/pogo \
  vbase.proto

go mod tidy
echo "generated: $(ls -la pogo/)"
