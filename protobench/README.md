# protobench — decode-at-volume harness (Phase 0)

Standalone module proving the opaque/lazy proto decode methodology before any
Golbat migration. See docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md.
Separate module on purpose: a second registration of vbase.proto inside the
Golbat binary would panic the proto registry.

## Corpus

Enable capture on a production Golbat (`config.toml`):

    [raw_capture]
    enabled = true            # payloads land in capture/<METHOD>/<ts>_<size>.bin

Copy payloads here as they accumulate (the harness uses whatever exists):

    rsync -a prod:/path/to/golbat/capture/ ../capture/

Capture only covers payloads dispatched through `decode()` (HTTP `/raw` and
the gRPC raw receiver); the Nebula gRPC side-path is not captured.

## Generate the proto package (once, and after each vbase.proto update)

    PROTO_SRC=~/dev/ProtoMirror/vbase.proto scripts/gen.sh

The lazy-annotation scan (`scripts/add_lazy_proto.py`) only greps Golbat's
own sources for getter usage — it excludes `protobench/` itself, so the
harness's own field reads in `readers/readers.go` can't strip annotations
from the subtrees it's measuring. Current run: 57 fields marked `[lazy = true]`.

## The three configurations

| Configuration | Command |
|---------------|---------|
| (a) open (current Golbat) | `go run ./cmd/bench` |
| (b) opaque + lazy         | `go run -tags protoopaque ./cmd/bench` |
| (c) opaque, no lazy       | `go run -tags protoopaque ./cmd/bench -nolazy` |

Useful flags: `-corpus ../capture -workers 96 -duration 60s -ballast-mb 2048`.
Run all three back-to-back on an idle machine; compare `alloc/decode`,
`decodes/s`, and `GC cpu-share`.

Microbenchmarks (per-method ns/op, B/op, allocs/op):

    go test ./bench/ -bench=Decode -benchmem
    go test -tags protoopaque ./bench/ -bench=Decode -benchmem
    PROTOBENCH_NOLAZY=1 go test -tags protoopaque ./bench/ -bench=Decode -benchmem

## Phase 0 exit gate (from the spec)

(b) or (c) must beat (a) on allocation rate and GC CPU share at volume —
provisional target: ≥20% allocs/op reduction on GET_MAP_OBJECTS. If the gate
fails, the migration (Phase 1) does not happen.

## Extending

New method = capture dir appears automatically; add a reader in
readers/readers.go mirroring the fields Golbat's decoder reads and register
it in readers.Registry.
