# Investigation Brief: Replacing/Improving Golbat's TTL Cache Layer

## Context (read first)

Golbat (this repo) caches all game entities in `jellydator/ttlcache/v3`
instances, wrapped by a generic `decoder/sharded_cache.go` for the hot
entities (pokemon, pokestop, gym, station, spawnpoint). During a week-long
production performance investigation (branch `perf/eviction-lock-contention`,
PR #377) five properties of ttlcache's expiry model each caused or amplified
a production incident. They are all currently NEUTRALIZED by workarounds —
this investigation decides whether to replace the cache layer so the
workarounds can be deleted, using benchmarks representative of production.

## Empirical facts about ttlcache v3 (verified this week — do not re-litigate)

1. `Cache.OnEviction` runs EVERY callback on its own goroutine (`go fn(...)`).
   Mass expiry => unbounded goroutine spawn. With a blocking callback this
   produced a production "goroutine bomb": millions of parked goroutines
   each holding an entity lock (fixed by non-blocking TryEnqueue, commit
   ed50bf8).
2. Expiry is a heap (`expirationQueue`) + linked list per cache, updated
   O(log n) UNDER THE SHARD RWMutex on every Set — and on every Get unless
   `WithDisableTouchOnHit` is set. Production CPU profiles attribute ~3% of
   total CPU to `expirationQueue.Less` + `list.move`.
3. `DeleteExpired` unlinks all currently-expired entries under the shard
   write lock — sweep cost scales with expiry-cohort size. Flat TTLs formed
   synchronized cohorts after restarts (fixed by per-entry TTL jitter).
4. The first sweep after construction happens at the default TTL, creating
   a backlog cliff exactly 1h after startup (mitigated by the same jitter).
5. Callbacks are unsynchronized with sweeps, cache ops, and entity locks —
   Golbat carries guard/self-heal logic for the races (see CLAUDE.md
   "Eviction Callbacks and the Tree Writer").

## What Golbat actually needs from a cache (the full API surface)

From `decoder/sharded_cache.go` + direct ttlcache uses (encounter_cache,
incident/weather/route/tappable/s2cell/player caches):
- Get(key) -> *V (pointer storage; entities have their own mutexes)
- Set(key, value, per-entry TTL)
- GetOrSetFunc(key, factory, ttl) — ATOMIC single-winner creation (this is
  load-bearing for the locking model; see CLAUDE.md "Atomic Cache Population")
- Delete(key), Has(key), Len()
- Range/iterate (preload, PreservePokemonToDatabase shutdown path)
- Eviction notification distinguishing expiry vs explicit delete
- Per-entry TTL update (UpdateTTL / re-Set with new TTL)
- Optional touch-on-hit per cache (pokemon cache disables it)
- Keys: uint64, int64, string (generic)

## Production workload parameters (size the benchmarks with these)

- Pokemon cache: ~10M live entries at evening peak; ~3–6k evictions/sec
  steady; expiry waves 10k+/sec (overnight population drain, restart
  cohorts). Values are pointers to ~1KB structs.
- Spawnpoint cache: millions of entries, EXTREMELY read-hot: one Get per
  wild pokemon sighting (thousands/sec) after commits f5c2b29/3d25017 —
  currently pays ttlcache Get+touch (shard lock + heap fix) per read.
- Fort caches: ~1M entries, 25h jittered TTLs, read-hot during GMO fort
  batches.
- encounterCache (unsharded ttlcache, uint64 keys, values contain a map):
  2–4 ops per stats event on a single worker goroutine whose throughput
  saturated at ~25k events/sec — cache op cost is part of that budget.
- Shard count: config `cache_shards`, production uses up to ~100.
- Host: many-core (48+), 360GB RAM. GC pressure matters: the S2 lookup +
  caches already dominate the heap.

## Candidates to evaluate

A. Baseline: current ttlcache v3.4+ via ShardedCache (as configured in this
   repo — jitter, DisableTouchOnHit for pokemon, eviction workarounds).
   Also audit ttlcache's LATEST release + open issues: have any of facts
   1–4 been fixed upstream? (If a newer version delivers callbacks
   differently, that changes the calculus.)
B. otter (github.com/maypok86/otter, v2): Caffeine-style. Audit: pointer
   values, per-entry TTL, atomic loader (single-winner), eviction listener
   delivery model (dispatcher vs per-item goroutines?), Range, generics.
C. theine (github.com/Yiling-J/theine-go): same audit list.
D. Purpose-built prototype (~300–500 lines): xsync.Map storage (lock-free
   reads) + atomic per-entry deadline, lazy expiry on Get (never return
   expired), timing-wheel proactive eviction (1-minute buckets; evictor
   drains one bucket/tick), single dispatcher goroutine for eviction
   callbacks (bounded queue, ordered). Match the API surface above. Note:
   GetOrSetFunc single-winner atomicity comes free from xsync.Map.Compute.

## Benchmark matrix (all with -benchmem, and run key ones with -race off AND
## a separate goroutine-count + GC-pause measurement)

1. Read-hot: 90% Get / 10% Set, zipf keys over 10M entries, parallel at
   8/32/96 goroutines. (Models pokemon+spawnpoint steady state.)
2. Touch cost isolation: Get with touch-on-hit ON vs OFF vs candidate's
   equivalent. (Models spawnpoint fast path per-wild Get.) NOTE: touch is
   LOAD-BEARING for the spawnpoint/pokestop/gym/station caches — it is what
   keeps actively-seen entries resident past their fixed TTLs — so
   candidates must make touch cheap (e.g., an atomic deadline store),
   not merely support disabling it.
3. Mass-expiry wave: preload 10M entries with TTLs uniformly in [0, 60s],
   run mixed load while the entire population expires; measure: p99 Get
   latency during the wave, max goroutine count, callback delivery lag,
   GC pause distribution. (Models the overnight incident.)
4. GetOrSetFunc storm: 96 goroutines racing to create the same 1k keys.
   (Models cold-start convoy.)
5. Eviction callback throughput: callbacks that enqueue to a channel (like
   the tree writer) — measure end-to-end eviction->callback-delivered rate
   and goroutines used.
6. Single-goroutine consumer tax: the encounterCache pattern — one
   goroutine doing Get+Set pairs as fast as possible. (Models stats worker
   headroom.)
7. Memory: bytes/entry at 10M entries for each candidate (runtime.MemStats
   before/after), plus GC cycle time at steady churn.

## Success criteria / decision rule

Replace the cache layer ONLY if a candidate shows ALL of:
- ≥2x throughput on benchmarks 1+2 (read-hot paths), or equivalently
  removes shard-lock reads entirely;
- No pathological behavior on benchmark 3 (bounded goroutines, p99 Get
  under the wave < 10x steady-state p99);
- Correct single-winner semantics (benchmark 4, verified with -race);
- Memory within ~1.5x of ttlcache at 10M entries.
Otherwise: recommend keeping neutralized ttlcache and document that the
workarounds are load-bearing.

## Deliverables

1. A `cachebench/` module (separate go.mod, NOT in the main build) with all
   benchmarks, runnable via `go test -bench . -benchmem`.
2. A results table (one row per benchmark x candidate) with the test
   hardware noted.
3. A written recommendation against the decision rule, including the
   upstream-ttlcache audit result and the API-fit audit for otter/theine.
4. If the purpose-built prototype wins: the prototype itself with its tests,
   NOT wired into Golbat — integration is a separate decision.

## Constraints

- Do not modify anything under decoder/ or change Golbat's build.
- Read CLAUDE.md first (esp. "Caching", "Eviction Callbacks and the Tree
  Writer", "Atomic Cache Population") — the locking model constrains what
  a cache may do.
- Branch perf/eviction-lock-contention has the current state; commits
  ed50bf8, f5c2b29, 3d25017 contain the incident context in their messages.
- Another session may be working in this same checkout concurrently: create
  and modify files ONLY under cachebench/ (plus this brief's Results section
  outputs listed below). No git commits, no git branch operations, no pushes.

## Reporting back (contract with the main session)

Write results as you go — these files are how the main session picks up
your work:
- cachebench/PROGRESS.md — append one line per completed step (audit done,
  benchmark N done, blocked-on-X). Update it before stopping for any reason.
- cachebench/RESULTS.md — the benchmark table (one row per benchmark x
  candidate, hardware noted) plus raw `go test -bench` output appended at
  the bottom.
- cachebench/RECOMMENDATION.md — the decision-rule verdict, the upstream
  ttlcache audit result, the otter/theine API-fit audits, and (if built)
  where the prototype lives and what its tests cover.
Keep all three self-explanatory: the reader has the context of THIS brief
but was not present for your session.

## Appendix: conversion sketch (evaluate candidates against THIS, not their README)

If a candidate wins, the conversion happens behind the existing seam
(decoder/sharded_cache.go); sharding deletes (candidate tables are
internally concurrent). Audit each candidate on these five points — they
are where conversion risk concentrates:

1. Single-winner atomic creation equivalent to ShardedCache.GetOrSetFunc
   (two racers MUST receive the same pointer) — verify with a race test.
2. Per-entry variable TTL. Caffeine-style ExpiryCalculator inverts our
   flow (TTL derived per cache instead of passed per Set): pokemon =
   value.remainingDuration(now); forts = creation TTL + access-refresh
   (which would DELETE the hysteresis-touch code, commit 75b3df0);
   encounterCache = encounterStatsDuration.
3. **Deletion-cause mapping**: ttlcache fires eviction callbacks on expiry
   and explicit delete only. Caffeine-lineage caches ALSO fire on
   REPLACEMENT — Golbat re-Sets live entries routinely (re-cache, TTL
   re-stamp), and a Replaced event reaching handlePokemonEviction /
   deferFortEviction would enqueue bogus tree deletes. The adapter must
   filter cause==Replaced. Passes unit tests, corrupts production — treat
   as the highest-risk item.
4. Callback delivery: bounded pipeline (goroutine bomb structurally gone)
   but still async vs entity-lock holders — the existing guards and
   TryEnqueue REMAIN. Verify delivery goroutine/ordering model.
5. Full-cache iteration cost/semantics (PreservePokemonToDatabase Ranges
   10M entries at shutdown; preload Ranges at startup).

Item-type bridge: callers hold *ttlcache.Item and call .Value() (~100
sites) — either a local Item wrapper (zero call-site churn) or return V
directly (mechanical sweep). Migration order if it proceeds:
encounterCache -> spawnpoint -> forts -> pokemon (most invariants last),
each step separately deployable.

Also add to benchmark 2: a third arm for the hysteresis-touch baseline
(75b3df0) — the replace-the-cache case must beat NEUTRALIZED ttlcache,
not stock ttlcache.

