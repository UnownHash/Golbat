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

The lazy-annotation scan (`scripts/add_lazy_proto.py`) unions two getter
scans: Golbat's own sources (excluding `protobench/`) and
`protobench/readers/` only. The readers are hand-built from Golbat's decode
paths, so they encode the direct field reads (`cell.Fort`, `wild.Pokemon`,
…) that a getter grep over unmigrated Golbat code cannot see — without
them, hot subtrees get annotated lazy and configuration (b) measures a
pathological setup (lazy overhead paid on every access) instead of a
realistic one. Unused message-typed fields are annotated whether singular
or repeated — protobuf-go v1.36's opaque API supports lazy decoding on
repeated message fields too. Current run: 70 fields marked `[lazy = true]`.

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

## Engines beyond the opaque/lazy gate

The volume runner's `-engine` flag selects alternative decode engines
(`std`, `vt`, `vtpool`, `hyperpb`, `hypershim`); `-ingest`, `-discardunknown`
and `GOGC` explore engine-independent knobs. Regenerate with
`scripts/genvt.sh` (vtprotobuf, pruned closure) and `scripts/genshim.sh`
(typed hyperpb accessors). Cross-engine correctness is enforced by
`TestEnginesAgree*` — every engine must produce identical Sink deltas on
every payload. Findings and the Golbat recommendation live in RESULTS.md.
