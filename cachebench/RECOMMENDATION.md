# Cache Layer Recommendation

**Verdict: REPLACE — the decision rule is satisfied by two candidates
(prototype and otter); recommend the purpose-built prototype
(`cachebench/protocache/`) first, otter as the fallback. theine is
disqualified. Nothing was fixed upstream in ttlcache.**

Investigation per `docs/cache-investigation-brief.md`. All numbers from
RESULTS.md; test hardware: Apple M3 Pro (12 cores, 36GB RAM), go1.26.3
darwin/arm64. Production is 48+ cores / 360GB — relative comparisons and
structural findings transfer, absolutes do not. Per the updated brief, the
baseline is the NEUTRALIZED production configuration: ttlcache + jitter +
DisableTouchOnHit + hysteresis touch (commit 75b3df0), labeled
`ttlcache-hyst`.

## 1. Decision rule, gate by gate

| Gate | otter v2.3.0 | theine v0.6.2 | prototype |
|---|---|---|---|
| ≥2x on bench 1 (read-hot) vs neutralized baseline | **PASS** 2.8–3.3x | FAIL 1.8–2.0x | **PASS** 2.0–4.2x |
| ≥2x on bench 2 (touch path) vs hysteresis (146.5ns) | **PASS** 4.9x (30.2ns) | **FAIL** 0.30x (484.3ns — its touch emulation is 3.3x SLOWER) | **PASS** 5.4x (26.9ns) |
| — or removes shard-lock reads entirely | effectively (lossy read buffers, no lock on read path) | no (shard RLock per Get) | **yes** (xsync.Map lock-free read + atomic deadline) |
| Bench 3: bounded goroutines under 10M-entry expiry wave | **PASS** (max 39) | **PASS** (max 38) | **PASS** (max 38) |
| Bench 3: wave p99 Get < 10x steady p99 | **PASS** (0.6x) | **PASS** (0.8x) | **PASS** (0.8x) |
| Bench 4: single-winner GetOrSetFunc under -race | **PASS** (ComputeIfAbsent) | **FAIL natively** — passes only via adapter-added striped-mutex emulation | **PASS** (xsync.Map.Compute) |
| Memory ≤1.5x ttlcache at 10M entries | **PASS** 1116 B/entry (0.91x) | PASS 1148 (0.93x) | **PASS** 1113 B/entry (0.90x) |

(ttlcache reference: 1231 B/entry; plain-map floor: 1054 B/entry.)

Two candidates pass every gate, so per the brief's rule the cache layer
should be replaced. Supporting numbers that did not gate but matter:

- **Steady-state p99 Get at 10M entries under mixed load**: ttlcache
  174µs vs prototype 2.7µs / otter 3.7µs (~50–65x). ttlcache's worst case
  is not the expiry wave — it is the ordinary full-cache steady state,
  where every Set's O(log 10M) heap fix convoys Gets behind shard locks.
  (Its wave p99 is *better* than its steady p99 because the population
  shrinks.) This is the benchmark image of the ~3% CPU the production
  profiles attributed to `expirationQueue.Less` + `list.move`.
- **The goroutine bomb reproduced (bench 5b)**: with eviction callbacks
  blocked for 10s during a 200k-entry wave, ttlcache parked **200,017
  goroutines** (one per eviction — the ed50bf8 incident; in production
  each also held an entity lock). Even a merely *slow* consumer
  (100µs/event) piled up **199,448**. Prototype: **7** in both arms; otter
  18–19; theine 18. Bounded-delivery candidates instead stretch delivery
  time (~17s for the same work), with cache writers unaffected — except
  theine, below.
- **theine blocks writers**: in the chronic-slow-consumer arm, theine's
  probe `Set` stalled for **6.97 seconds** — its removal listener shares
  the maintenance goroutine that `Set` feeds with blocking channel sends.
  That is exactly the fill-drain decode-path pattern Golbat's invariants
  forbid (CLAUDE.md "Decode-Path Workers").
- **GC**: churn-forced GC cycles over the 10M-entry heap cost prototype
  2.7s / otter 3.5s / ttlcache 5.8s / theine 6.7s of GC CPU; wave-phase GC
  CPU showed the same ordering. The heap+linked-list expiry structures are
  the most expensive to scan.
- **Bench 6 (stats-worker tax)**: prototype 456ns vs baseline 530ns/op —
  only ~14% headroom; this path is dominated by entity allocation, not
  cache choice. The encounterCache's ~25k events/s ceiling will not be
  fixed by any cache swap.
- **Bench 1 sensitivity**: ttlcache at production-like 100 shards improves
  to 95.5ns at g96 — still ~2x behind otter/prototype on 12 cores.

## 2. Upstream ttlcache audit (brief facts 1–4)

Latest release **v3.4.1 (2026-06-22)**; repo pins v3.4.0. Verified against
v3.4.1 source + full tracker sweep on 2026-07-06:

| Brief fact | Upstream status |
|---|---|
| 1. Goroutine per eviction callback | **UNCHANGED** (`wg.Add(1); go fn(...)` per item, invoked under the shard write lock). PR #190 (inline delivery) closed WITHOUT merge. |
| 2. O(log n) heap fix under shard mutex per Set / touching Get | **UNCHANGED** (`container/heap` over `[]*list.Element`). |
| 3. Whole-cohort DeleteExpired under one write lock | **UNCHANGED** (and callbacks spawn while it holds the lock). |
| 4. First sweep at default TTL | **UNCHANGED** as empty-cache fallback. v3.4.1 fixes the cleaner OVERSLEEPING when a TTL is shortened (#204/PR #206) — pinned v3.4.0 has that bug. |

No issue or PR upstream addresses batched/dispatcher callback delivery,
mass-expiry sweep cost, or expiry-heap contention. If ttlcache is kept for
the small caches, bump to v3.4.1 for the TTL-shorten fix.

## 3. otter v2.3.0 API-fit audit

Fit is better than expected, with two mandatory guardrails:

- Unbounded TTL-only mode: **yes** (leave MaximumSize unset — no capacity
  eviction, no admission policy). Pointer values, full generics.
- Per-entry TTL: **yes but inverted** — no `Set(k,v,ttl)`; TTL comes from
  an `ExpiryCalculator` reading the entry (adapter stores the TTL with the
  value), plus `SetExpiresAfter` for in-place updates. Touch-on-hit =
  `ExpireAfterRead` returning the entry's TTL. Conversion would DELETE the
  hysteresis-touch code (75b3df0) in favor of calculator-driven refresh.
- Single-winner: **yes** (`ComputeIfAbsent`; `Get(ctx,key,loader)` is
  per-key singleflight). Race-verified, same-pointer property holds.
- **Guardrail 1 — Executor**: `OnDeletion`'s DEFAULT executor is
  `go fn()` — one goroutine per deletion event, the ttlcache bomb shape
  with a different spelling. Must configure a custom executor or use
  `OnAtomicDeletion` (inline on the maintenance path; what cachebench
  uses; bomb-tested bounded at 18–19 goroutines).
- **Guardrail 2 — cause filter**: otter fires `CauseReplacement` on
  overwrite of a live entry; Golbat re-Sets live entries routinely. The
  adapter must drop Replacement (and Overflow) events or
  `handlePokemonEviction`/`deferFortEviction` would enqueue bogus tree
  deletes. Pinned by `TestReplacementFiresNoEviction`.
- Expiry: Caffeine-style hierarchical timing wheel + 1s cleanup goroutine;
  no heap, no reader-blocking cohort sweep. Iteration `All()` weakly
  consistent; **no exact Len** (only `EstimatedSize`).
- Open issue to watch: **#177** — production-reported mutex convoy in
  `drainBuffers` under write-heavy Compute load (Golbat's pokemon cache is
  write-heavy).

## 4. theine v0.6.2 API-fit audit — disqualified

- **Max size MANDATORY** (no unbounded mode); capacity behavior can evict
  live entities — a correctness hazard for Golbat regardless of sizing.
- **"Set always stores" violated**: `SetWithTTL` returns bool (doorkeeper
  drops first-Sets when enabled), and W-TinyLFU admission can evict a
  just-stored entry moments after a successful Set on a full cache (open
  issue #3, by design).
- **No touch-on-hit**: reads never extend TTL; the only emulation (re-Set
  per Get) measured 484ns — 3.3x slower than the hysteresis baseline it
  must beat, and it cannot reproduce the semantics (original TTL not
  retained).
- **No per-call atomic creation** (loader fixed at build time).
- **Writer backpressure**: removal listener runs inline on the single
  maintenance goroutine; `Set`/`Delete` are blocking sends into that
  goroutine's channel. Measured: 6.97s writer stalls under a chronic slow
  consumer. Structural violation of the decode-path invariant.
- Fastest full-cache Range (386ms/10M) and a fine timing wheel — but the
  correctness-model mismatches are not adapter-fixable.

## 5. Appendix (conversion sketch) confirmations

1. **Single-winner**: verified for all four under -race incl. the
   same-pointer requirement (`TestSingleWinnerAllCandidates`). theine only
   via adapter emulation.
2. **Per-entry TTL**: native for ttlcache/theine/prototype; inverted
   (calculator) for otter — workable behind the ShardedCache seam.
3. **Deletion-cause filtering (highest risk)**: CONFIRMED handled. Only
   otter emits Replacement; the adapter filters it; ttlcache/theine/
   prototype fire nothing on overwrite. Pinned by
   `TestReplacementFiresNoEviction` (99 live overwrites → zero events;
   Delete → exactly one Deleted event) on every candidate.
4. **Callback delivery**: ttlcache unbounded-goroutine (guards must stay);
   otter safe only via non-default configuration; theine bounded but
   blocks writers; prototype single-dispatcher bounded-queue ordered by
   design. In all cases delivery stays async vs entity locks — the
   existing guards and TryEnqueue REMAIN whatever is chosen.
5. **Full-cache iteration at 10M** (shutdown-preserve path): ttlcache
   1.69s (holds each shard lock while iterating it), otter 1.52s (weakly
   consistent, skips dead), theine 0.39s (shard RLocks), prototype 0.58s
   (weakly consistent, skips expired), plain map 63ms. All visit 100% of a
   live population. None is a shutdown-path problem.

## 6. Recommendation

**Adopt the prototype (candidate D) as the replacement, staged per the
appendix (encounterCache → spawnpoint → forts → pokemon), behind the
existing `decoder/sharded_cache.go` seam.** Rationale over otter, the
other passing candidate:

- **Exact semantic match**: TTL-at-Set, per-call GetOrSetFunc, ttlcache-
  compatible eviction reasons, silent supersede on overwrite — the
  behaviors Golbat's guards assume, with no call-site inversion and no
  cause-filter to forget.
- **Safe by default**: bounded single-dispatcher callbacks are the only
  delivery mode; otter's safe delivery is opt-in away from a dangerous
  default.
- **Best measured profile**: lowest memory (1113 B/entry), lowest GC cost
  (2.7s vs otter 3.5s churn GC CPU), cheapest touch (27ns atomic store),
  7-goroutine worst case in the bomb, and it removes shard-lock reads
  entirely (the rule's alternative clause).
- **No third-party tail risk** equivalent to otter #177 on a write-heavy
  table.

The cost is ~420 lines of owned code. That is mitigated by its -race test
suite (see §7), this benchmark harness as a regression gate, and the
staged migration. **otter is the fallback** if owning cache code is
rejected — with the two §3 guardrails made non-optional in the adapter.

Honest caveats:

- Prototype read-hot throughput at 96 goroutines on this 12-core host
  degrades to 2.03x baseline (Set-path wheel filing + allocation; the
  read path itself does not degrade — 26.9ns at g96 in bench 2, and
  otter's buffered write path wins at that point, 50.0ns vs 81.5ns).
  Validate on production core/shard counts before the pokemon (most
  write-heavy) migration; batching wheel filings is available headroom if
  needed.
- Eviction callback lag is wheel-tick coarse: p99 ~1s (1s tick) vs
  ttlcache's 2ms continuous sweep. Golbat tolerates seconds of eviction
  lag by design (ghost tree points are cleaned via the lookup-cache check
  and batched deletes), but any future consumer needing sub-second expiry
  precision would need a finer tick.
- What replacement buys over the ALREADY-NEUTRALIZED baseline: 2–4x
  read-path throughput, ~65x steady-state p99 tail (174µs → 2.7µs), ~2x
  GC cost, ~10% memory, structural elimination of the goroutine bomb and
  sweep cliffs — and deletion of the workaround code (hysteresis touch,
  TTL jitter as a *load-bearing* mechanism, eviction-guard complexity
  documented as required). The stats-worker path gains almost nothing
  (bench 6) — do not justify the migration on it.
- If the team prefers NOT to replace: the workarounds are demonstrably
  load-bearing and must be documented as permanent (this investigation's
  data doubles as that documentation), and ttlcache should be bumped to
  v3.4.1.

## 7. The prototype

`cachebench/protocache/` (~420 lines + tests), NOT wired into Golbat —
integration is a separate decision. Design: xsync.Map storage (lock-free
reads), atomic per-entry deadline (touch = one atomic store), lazy expiry
on Get (never returns expired regardless of sweep timing), striped coarse
timing wheel with bucket-ownership dedupe (superseded/touched filings
become skipped ghosts instead of accumulating), single dispatcher
goroutine delivering eviction callbacks in order through a bounded queue.

Test coverage (all -race): basics (set/get/has/len); never-return-expired
with sweeps disabled; touch extends / no-touch doesn't; proactive eviction
delivers exactly one ReasonExpired per entry; Delete fires ReasonDeleted
exactly once; touched entries survive wheel drains and fire only after
touches stop; Set-supersede fires nothing and the new generation still
expires on its own deadline; single-winner GetOrSetFunc under 96-goroutine
races; expired-but-unswept entries treated as absent; UpdateTTL; Range
skips expired; callback ordering + single-goroutine delivery; 16-goroutine
mixed-op hammer. Plus the shared cross-candidate suite
(`correctness_test.go`) and all seven benchmark-matrix scenarios.
