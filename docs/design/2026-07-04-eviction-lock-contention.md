# Eviction lock contention — design record

> **Status:** Implemented. Shipped in PR #377 (`perf/eviction-lock-contention`,
> merged 2026-07-29), which also carried the move from ttlcache to otter v2
> (`ottercache/cache.go`). Reconstructed from the original implementation
> plan and the commit history of that branch; the code and `CLAUDE.md`
> describe current behaviour.

## Problem

Production instances showed multi-second freezes of pokemon entity locks:
decode goroutines holding an entity lock stalled for seconds at a time, in a
cycle of roughly 3–5 s every ~30 s. While frozen, every decoder that touched
the same entities queued behind them, so decode throughput collapsed even
with idle cores.

## Root cause

The original plan blamed the TTL cache: it assumed ttlcache's
`DeleteExpired` sweep called eviction callbacks synchronously under the shard
write lock, so sweep duration propagated into entity-lock hold time. That
was wrong. ttlcache's `OnEviction` wraps each callback in `go fn(...)`, so
callbacks ran on their own goroutines, unsynchronized with the sweep.

The real mechanism was a convoy on the global R-tree mutex:

- Every evicted item spawned a goroutine that took `pokemonTreeMutex` /
  `fortTreeMutex` to delete one tree point. A mass expiry produced thousands
  of these at once.
- Savers inserted into the tree while holding their entity lock, so they
  queued behind the eviction goroutines on the same mutex.
- Every scan request called `tree.Copy()`. The rtree is copy-on-write, so
  each copy left the live tree permanently shared and made subsequent
  inserts and deletes pay clone chains (`newNode <- copy <- cow`).

In-burst goroutine dumps (30 samples at 2 s intervals) confirmed it: 90–96
decoders parked at `addPokemonToTree` on the tree mutex, with the holder
being the eviction drain (`nodeDelete` recursion) or a saver paying COW
clones. No goroutine was blocked on a channel send.

## What was done

**Single ordered tree writer** (`decoder/rtree_evictor.go`). Eviction
callbacks became O(1): they delete the lookup-cache entry inline and enqueue
the tree delete to a buffered channel. One worker per tree drains it and
applies up to 512 mutations per mutex acquisition. Later on the branch every
runtime pokemon tree mutation was routed through the same writer: new-record
inserts, position moves, rehydration inserts and the eviction-race
self-heal. Savers never take the tree mutex. Only the worker and the
snapshot refresh take it. Enqueue order is apply order. Deletes match on
(coords, id), so stale duplicates are no-ops, and the rtree is a multiset, so
duplicate inserts are harmless. Startup preload inserts directly, before
traffic, so it does not flood the queue.

**Shared scan snapshot.** Scans stopped copying the tree per request. They
share an `atomic.Pointer` snapshot refreshed at most once per second
(`refreshTreeSnapshot`, `treeSnapshotMaxAge`), so copy-on-write cost is paid
at most once a second rather than once per request. Candidates are
re-verified against the live lookup cache, so the staleness affects only
candidate discovery.

**TTL jitter and clamp.** A restart preloads or ingests a large cohort at
once, which then expired in one synchronized wave. Unverified pokemon got
a 55–65 min jittered TTL instead of a flat 60 min. Verified pokemon past
despawn got 1 min instead of a fresh hour, and DB-rehydrated pokemon got a
despawn-based TTL. Fort entries got jittered TTLs (25–27 h with
`fort_in_memory`, 60–70 min without; `fortCacheEntryTTL`).

**Race guards.** Because callbacks were asynchronous, they could race with a
save that re-cached the same entity. The pokemon callback takes the entity
lock and skips cleanup if the pokemon was re-cached. A save that finds its
lookup entry missing re-queues the tree insert. Fort callbacks skip cleanup
when the fort is cached again, when the lookup is already gone, or when the
converted counterpart (pokestop ↔ gym) owns it. Scan paths dedupe matched
ids, because deferred deletes can briefly leave a duplicate point.

**Bounded raw processing.** `tuning.raw_processing_concurrency` and
`raw_processing_queue_factor` cap decode goroutines and shed excess packets
(`raw_limiter.go`), so overload produces bounded loss instead of an unbounded
pile-up on internal locks.

## Follow-up findings on the same branch

**Eviction callbacks must never block (commit ed50bf8, 2026-07-06).**
Overnight, a nightly DB-host job stalled decode during a mass-expiry wave.
The tree writer's channel filled. Eviction callbacks did a blocking enqueue
while holding the entity lock, and ttlcache ran one goroutine per evicted
item. The full channel therefore parked a goroutine for every evicted entry,
an unbounded population. The logs showed 3.17M `LOCK_HELD_LONG
holder=cacheEviction` lines and entity locks held for minutes. The parked
goroutines starved the scheduler and GC until the worker drained at about
500 ops/s, a self-sustaining spiral. The design had assumed the parked
goroutines were bounded. They were bounded only by the size of the cached
population.

The fix: eviction callbacks use `TryEnqueue`, which drops on a full channel
with once-per-second accounting. A dropped delete leaves a ghost tree point,
which scans tolerate, because the lookup entry was already removed inline.
Savers keep blocking sends, because the raw-processing limiter bounds their
concurrency.

**Decoders convoyed on other global locks.** A later production goroutine
dump showed 93 of 96 decode slots parked on two more global mutexes: 66 on
`pokemonStatsLock` and 27 on the fort tracker's mutex
(`ProcessCellUpdate` per cell per GMO). Both moved to single-worker pipelines
fed by channels that drop and count when full (`stats.go`,
`fort_tracker.go`). Staleness detection works at hour granularity and does
not need to run in the decode path. The fort tracker worker bounds each
event's DB work with a one-minute deadline (commit 9b6c155). Without that, a
single hung query would wedge staleness detection instance-wide. The same
overnight incident showed 77k tracker drops in one hour from a worker wedged
on an undeadlined call, because the deadline fix was not yet deployed.

## Move to otter

Later on the same branch the entity caches moved from ttlcache to otter v2
behind the hardened `OtterCache` adapter (`ottercache/cache.go`). It
delivers deletion events on one dispatcher goroutine per cache through a
bounded queue. Otter's default executor runs one goroutine per event, which
is the shape behind ed50bf8. Raw inline delivery would deadlock handlers
that take entity locks. The adapter forwards only expiration and
invalidation causes, so overwriting a live entry on save never triggers a
tree delete. Eviction callbacks still use `TryEnqueue`, because blocking the
single dispatcher would stall all eviction delivery for that cache. The
batched tree writer remains the only tree mutator. The `tuning.cache_shards`
knob this work added for the sharded ttlcache wrapper became obsolete and
was removed.
