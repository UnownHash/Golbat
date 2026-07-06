# Decode-engine experiment results (2026-07-06)

**Rig:** Apple M-series laptop (12 threads used), Go 1.26, frozen corpus
snapshot `corpus-frozen/` (800 GET_MAP_OBJECTS ≈ 15 MB + 200 ENCOUNTER
payloads captured from prod). Volume runner: 12 workers × 25 s.
**Correctness gate:** all five engines produce identical Sink deltas over
every corpus payload (`TestEnginesAgreeCorpus`).

Relative numbers are what matter; absolute numbers are laptop-specific and
mix corpus composition where noted. Prod (linux/amd64) canary remains the
final word.

## Engines

| Engine | What it is |
|---|---|
| `std` | `proto.Unmarshal` into open-API structs (current Golbat) |
| opaque (b)/(c) | Opaque API ± lazy decoding (Phase 0 gate — failed, see spec) |
| `vt` | vtprotobuf generated `UnmarshalVT`, same structs |
| `vtpool` | `vt` + vtproto message pools, whole-graph return after read |
| `hyperpb` | buf.build/go/hyperpb: compiled parser, `Shared` arena reuse, PGO; hand-rolled protoreflect walk |
| `hypershim` | hyperpb behind **generated typed shims** (`cmd/hyperpbgen`) — the Golbat-migration ergonomics |

## GMO, no ballast (per-decode costs)

| engine | decodes/s | B/decode | objects/decode | GC CPU share |
|---|---|---|---|---|
| std | 34,427 | 222,382 | 1,357 | 5.1% |
| opaque+lazy (b) | 31,215 | 221,080 | 2,043 | 4.8% |
| opaque−lazy (c) | 31,436 | 208,034 | 1,486 | 4.5% |
| vt | 39,039 | 222,190 | 1,350 | 4.0% |
| vtpool | 55,740 | 70,626 | 646 | 4.9% |
| hyperpb (+PGO) | **58,629** | **49,741** | **255** | **1.4%** |
| hypershim (+PGO) | 57,632 | 49,756 | 255 | 1.5% |

- Opaque/lazy loses on GMO-shaped data: the unread subtrees are swarms of
  tiny messages, and lazy's per-field bookkeeping outweighs deferral.
- The typed-shim layer costs ~1.7% vs hand-rolled reflection.
- hyperpb PGO recompilation: ~+4% throughput vs non-PGO.

## GMO+Encounter, 2 GB pointer-dense ballast (prod-shaped live heap)

| engine | GOGC | decodes/s | B/decode | objects/decode | GC CPU share |
|---|---|---|---|---|---|
| std | 100 | 11,616 | 178,342 | 1,089 | 7.5% |
| std | 400 | 19,953 | 178,312 | 1,089 | 5.3% |
| vtpool | 100 | 37,456 | 56,220 | 517 | 7.6% |
| hypershim | 100 | 37,157 | 39,610 | 204 | 5.1% |
| hypershim | 200 | **41,363** | 39,756 | 205 | 3.7% |
| hypershim | 400 | 40,739 | 39,756 | 205 | 2.4% |

Under a large live heap the gap widens to **3.2–3.6×**: allocation-heavy
decode forces constant re-marking of the resident heap (caches/R-trees in
prod). This is the regime Golbat actually runs in. Pause p99 rises with
arenas + high GOGC (2–7 ms here) — irrelevant at Golbat's latency targets.

## Cheap wins independent of engine (std, GMO, no ballast)

| knob | effect |
|---|---|
| `DiscardUnknown: true` | −3% bytes, −5.5% objects, +3.7% decode rate (prod payloads carry unknown fields vs our vbase vintage) |
| ingest buffer pooling | −14% bytes/decode vs per-packet alloc (the base64 buffer), GC share 5.4→4.9% |
| GOGC 400 (with ballast) | std +72% decodes/s; trades heap headroom for GC CPU |

## Recommendation for Golbat

1. **Now, engine-independent:** `DiscardUnknown` on all client-proto
   unmarshals (Golbat never re-serializes them), pool the base64 payload
   buffers on the raw path, and evaluate `GOGC=200–400` (or GOMEMLIMIT
   equivalents) on prod boxes with RAM headroom.
2. **Engine:** hyperpb behind `hypershim`-generated typed accessors is the
   measured optimum — biggest wins exactly where prod hurts (large live
   heap). Migration shape: regenerate shims against `golbat/pogo`
   descriptors, adopt per-method starting with GMO, and audit retention
   boundaries — hyperpb messages must not outlive their `Shared` arena, so
   retained protos (weather consensus, Raw batch structs) must either copy
   out at the boundary (Golbat's entity decode already copies fields) or
   parse without arena reuse. hyperpb is read-only (no mutation) and
   amd64/arm64 only; both fit Golbat's decode path.
3. **Conservative fallback:** `vtpool` matches hypershim's throughput at
   GOGC=100 with familiar generated structs and near-zero call-site changes,
   at ~40% more bytes and 2.5× more objects per decode; it carries the same
   lifecycle-discipline burden (pooled graphs must be returned exactly once).

## Reproduce

```
scripts/gen.sh       # pogo (hybrid + lazy annotations)
scripts/genvt.sh     # pogovt (pruned closure + vtproto unmarshal/pool)
scripts/genshim.sh   # hypershim typed accessors
PROTOBENCH_CORPUS=../corpus-frozen go test ./readers/ -run TestEnginesAgree  # correctness
go run ./cmd/bench -corpus ../corpus-frozen -workers 12 -duration 25s -engine <engine> [-ballast-mb 2048]
```
