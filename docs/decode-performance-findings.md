# Decode-path performance findings

Consolidated learnings from the proto-decode optimization exploration
(PRs #378 hyperpb harness, #381 hyperpb migration). **hyperpb itself is being
left behind** — v0.1.x was fast but too immature (three silent correctness
bugs found in the first week of testing) to justify the maintenance for the
~10% CPU it bought over the base branch. This branch implements the
engine-independent wins that survive that decision.

All numbers are from the `protobench` harness (standalone decode-at-volume
rig) against a size-stratified, frozen corpus of real production payloads.
Harness figures are **relative**; absolute wins were confirmed on a prod
canary. GMO = GetMapObjects, the dominant method.

## Implemented on this branch

### 1. `DiscardUnknown` on client-proto unmarshals (the universal win)

**Measured: +3.7% decode rate, −5.5% allocated objects, −3% bytes.** Production
payloads carry fields newer than our `vbase` schema vintage; protobuf-go
otherwise retains them in a per-message unknown-fields buffer. Golbat **never
re-serializes** client protos, so discarding is free.

Applied via a shared `unmarshalClientProto` helper at every inbound client-proto
decode site (`decode.go`, `decode_nebula.go`, `decode_push_gateway.go`).
**This wins on both ingest paths** — HTTP `/raw` and gRPC — because both funnel
their per-packet decode through the same `decode()` unmarshal calls. This is the
win that reaches everyone.

### 2. Ingest buffer pooling — HTTP path only

**Measured: −14% bytes/decode on the HTTP path**, GC share 5.4%→4.9%. The HTTP
`/raw` handler base64-decodes each payload into a fresh `[]byte` that lives for
one `decode()` call; a `sync.Pool` (`raw_bufpool.go`) recycles those buffers.
Verified 0 allocs/op for the decode; safe because standard protobuf-go copies
bytes out during Unmarshal, so the buffer is free the moment `decode()` returns.

**Scope caveat (important):** this only helps the HTTP path. **Most deployments
use the gRPC ingest path**, where payloads arrive as raw `[]byte` fields of the
gRPC request — there is no base64 buffer of ours to pool. Those payload bytes
are allocated by protobuf-go unmarshaling the `RawProtoRequest`, and grpc-go
(v1.81) already pools its transport receive buffers by default. So there is no
equivalent buffer-pooling change to make for gRPC users — the framework already
does it, and their decode-allocation win comes from `DiscardUnknown` above.

### 3. `make pgo-capture` / `pgo-status` — refresh tooling for compiler PGO

The base branch committed a `default.pgo` (Go compiler PGO — auto-applied to
every build, ~profile-guided inlining/devirtualization) but *not* the tooling
to refresh it, leaving the profile un-maintainable when game updates shift hot
paths. This branch ports the targets and extends them to **auto-detect the port
and `api_secret` from `config.toml`** so a local operator can just run
`make pgo-capture` (env vars still override; `make pgo-config` shows what it
will use). Requires `profile_routes = true` in config to expose `/debug/pprof`.

## Already on the base branch (don't redo)

- **Go compiler PGO** — `default.pgo` committed and auto-applied (this branch
  adds the refresh tooling above).
- **Runtime GC tuning** — `gogc_percent` / `go_mem_limit_mib` config. Measured:
  on a large live heap (Golbat's caches/R-trees), **GOGC 300–400 reclaims 10%+
  of GC CPU**, trading heap headroom for fewer collections. Set
  `go_mem_limit_mib` below available RAM as a backstop when raising GOGC.

## Reusable infrastructure (parked on PR #378/#381, resurrectable)

- **`protobench` harness** — standalone decode-at-volume rig reporting
  decodes/s, B/op, allocs/op, GC CPU share, pause distribution, with a
  pointer-dense heap ballast to reproduce Golbat's big-heap regime. A/B any
  future engine/knob here first.
- **Payload capture hook** — debug-gated, size-stratified capture of real raw
  payloads for building corpora.
- **Shadow verification pattern** — decode a sampled fraction of live packets
  with *both* the old and new path and compare a field-level digest. Caught a
  silent data-corruption bug in a third-party parser at 1% sampling within
  seconds. Reusable for any risky decode change — a proto version bump, an
  opaque-API trial, vtprotobuf, etc.
- **Getter-style call sites** — the ~31-file conversion to `GetX()` access
  (required by the Opaque API; the prerequisite for any future opaque attempt).
  Retargeting from the hyperpb `pogoshim` wrappers to native `pogo` getters is
  mechanical (≈760 scalar calls transfer unchanged; ≈150 repeated/presence
  idioms need translation). See the "park the rest" analysis on PR #381.

## Documented dead-ends (do not re-explore without new evidence)

- **Opaque API + lazy decoding** — measured to *hurt* on GMO-shaped data (many
  tiny unread subtrees; lazy bookkeeping outweighs deferral). Opaque without
  lazy is roughly neutral. The presence-bitfield win doesn't apply — Golbat's
  hot messages are pure proto3 implicit-presence with no pointer-boxed scalars.
- **hyperpb** — genuinely fast (arena decode; parse ~3.5× cheaper, decode
  allocation 47%→19% of total, GC mark 26.7%→22.3% on prod, all complementary
  to the base's cache work), but v0.1.x maturity was the dealbreaker (Recompile
  corruption #39, 64-bit-varint-oneof data loss #42, UTF-8 validation #41, all
  in one week). If allocation reduction is ever wanted without hyperpb's exotic
  dependency, **`vtprotobuf` + message pooling** was the measured runner-up
  (55.7k decodes/s, −68% bytes vs std) with a 1.0-stable generator, at the cost
  of more per-decode object churn and pooled-graph lifecycle discipline.

## Measurement methodology worth reusing

- **Size-stratified corpus over 24h+** — complex shapes (raid-heavy GMOs,
  invasions, multi-reward quests) live in the size tail; a small uniform sample
  misses them.
- **Freeze the corpus** before an A/B — it grows during capture and shifts
  absolute numbers between runs.
- **Large heap ballast (~2 GB) to reveal GC wins** — on an idle box the
  decode-allocation → GC relationship is invisible; it only shows when mark
  cost scales against a big resident heap (which prod has).
- **Load-normalize prod profiles** — the raw-ingest handler's CPU share is a
  usable load proxy (cost ÷ ingest-share beats raw percentages).
- **Relative from the harness, absolute from the canary** — the harness proves
  methodology gain; prod GC shares the heap with caches, R-trees, and
  write-behind queues, so absolute numbers only come from a canary.
