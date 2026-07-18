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

## Current result (production, non-test code)

- 146 message types accessed, 409 fields.
- ~50% of accessed messages' fields are thinnable (higher in practice — the
  denominator undercounts oneof members). The big allocators are heavily
  thinnable: `PokemonProto` 13/78, `PokemonFortProto` 23/45, `RaidInfoProto`
  6/20, `FortDetailsOutProto` 8/27.
- **1 escape hatch**, cosmetic: a `.String()` on `RouteSubmissionStatus` inside
  a `log.Warnf` on the (cold) routes path. No `ProtoReflect`, no re-marshaling
  of client protos anywhere. **Golbat is clean for static thinning.**

## Intended thinning pipeline (design)

The `.proto` is not shipped (license), so thinning is a **maintainer-side**
step and both generated variants ship in the repo:

1. `protofields` → used-field set (JSON).
2. A thinning script (maintainer-only, needs the `.proto`): used-field set →
   thinned `.proto` → regenerate `pogo/vbase.thin.pb.go` with `//go:build thin`.
3. Ship both `vbase.pb.go` (`//go:build !thin`, full — what IDEs/contributors
   see for type discovery) and `vbase.thin.pb.go` (`//go:build thin`). Same
   package, same Go types, mutually exclusive — no registry clash.
4. `make golbat` / Dockerfile build with `-tags thin` (end users get the win);
   plain `go build` / gopls stay full (contributor field discovery).
5. CI builds + tests **both** tags and runs a full-vs-thin differential decode
   test, so the thin variant can never drift out of compilability or change
   decoded values.

A contributor using a not-yet-thinned field just gets a clean `-tags thin`
compile error in CI naming the field; a maintainer regenerates the thin variant
(a compile-safe, never-silent step).
