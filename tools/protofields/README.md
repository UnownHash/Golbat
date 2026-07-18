# protofields

Type-precise static analysis of which `golbat/pogo` message fields Golbat's
code actually accesses. Drives **schema thinning** — regenerating `pogo` with
only the accessed fields so protobuf-go skips the rest as unknown fields
(with `DiscardUnknown`) instead of allocating their subtrees. It's the static,
dependency-free equivalent of lazy decoding.

## What it does

Loads the Golbat module with full type information (`go/packages` + `go/types`
— the same infra `go vet`/staticcheck use) and, for every field or getter
access on a `pogo` message type, records the exact `(message, field)` pair.
Unlike a getter-name grep it is type-precise (no `GetFortId`-on-two-messages
collisions) and catches both `x.GetFortId()` and direct `x.FortId`.

It also flags **reflective escape hatches** — `ProtoReflect()`, `.String()` on
a message, and `proto.Marshal/Clone/Merge/Equal` on a pogo value — anything the
type-walk can't see and that would break (or subtly change) under thinning.

## Usage

```
cd tools/protofields
go run . ../..                 # analyze the parent Golbat module (non-test code)
JSON=/tmp/fields.json go run . ../..   # also emit the used-field set as JSON
INCLUDE_TESTS=1 go run . ../..         # count _test.go accesses too
```

## Current result (production + test code)

- 146 message types accessed, 410 fields.
- Trimming the full descriptor set keeps **417 of 15807 fields (removes 97%)**
  and empties 3402 unread messages. Kept fields retain their field numbers, so
  removed fields decode as skipped unknowns (with `DiscardUnknown`). The thin
  `vbase.thin.pb.go` is ~271k lines vs ~484k full (44% smaller).
- **1 escape hatch**, cosmetic: a `.String()` on `RouteSubmissionStatus` inside
  a `log.Warnf` on the (cold) routes path. No `ProtoReflect`, no re-marshaling
  of client protos anywhere. **Golbat is clean for static thinning.**

## Thinning pipeline (operational)

The `.proto` is not shipped (license), so thinning is a **maintainer-side** step
and both generated variants ship in the repo. `../../scripts/thin.sh` runs it
end to end (needs the `.proto` via `PROTO_SRC` and `protoc`):

1. `protofields` (INCLUDE_TESTS=1) → the `(message, field)` set Golbat accesses,
   as JSON. Tests are included so the full suite compiles + runs under
   `-tags thin` — that suite *is* the full-vs-thin differential.
2. `prototrim` → trims the full descriptor set (`FileDescriptorSet`) to the
   used set, preserving field numbers and oneof structure, → thin descriptor.
   A real oneof accessed via a type switch (`switch x.Type.(type)`) keeps all
   its members, since the code references the per-member wrapper types.
3. `protoc --descriptor_set_in` → `pogo/vbase.thin.pb.go`, `//go:build thin`.
4. The full `pogo/vbase.pb.go` carries `//go:build !thin`. Same package, same Go
   types, mutually exclusive build tags — no proto-registry clash.

`make golbat` / the Dockerfile build `-tags thin` (end users get the win); plain
`go build` and gopls stay full (contributor field discovery). `make golbat-full`
forces the full schema.

### CI gate

Run all four and require them green:

```
go build ./...            go test ./...            # full
go build -tags thin ./...  go test -tags thin ./... # thin (the differential)
```

A contributor who reads a not-yet-thinned field gets a clean `-tags thin`
compile error in CI naming the exact field (`unknown field X in struct literal
of type pogo.Y`); a maintainer re-runs `thin.sh` to regenerate the thin variant.
It is a compile-safe, never-silent step.
