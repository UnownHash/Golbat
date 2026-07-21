#!/bin/bash
# Regenerates hypershim (typed accessor shims over hyperpb) from the pogovt
# descriptors. Rerun after genvt.sh regenerates pogovt.
set -euo pipefail
cd "$(dirname "$0")/.."
go run ./cmd/hyperpbgen -out hypershim/hypershim.gen.go
gofmt -w hypershim/hypershim.gen.go
