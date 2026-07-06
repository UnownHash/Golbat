#!/bin/bash
# Regenerates pogoshim (typed accessors over protoreflect/hyperpb) from the
# committed golbat/pogo descriptors. Rerun after every vbase.pb.go update.
set -euo pipefail
cd "$(dirname "$0")/.."
go run ./cmd/pogoshimgen
gofmt -w pogoshim/pogoshim.gen.go
