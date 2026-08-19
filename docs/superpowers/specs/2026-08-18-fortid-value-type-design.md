# Fort id value type and username persistence option — design

**Date:** 2026-08-18
**Status:** Approved direction (spike complete, decision posted to PR 395); spec pending review
**Supersedes:** PR 395's string interning (`interned_string.go`) as the fort-id foundation
**References:** PR 394 (pokemon packing), PR 395 (interning split-out), #384 (parked incident-id
value type — the fallible-parse precedent), PR 395 comment 5330695262 (spike results)

## 1. Context and goals

PR 394 packed the cached `Pokemon` from 800 to 312 bytes. PR 395 split out the string-interning
half: `pokestop_id` and `username` as `uint32` handles into a global append-only table. Two open
questions were left in that PR: whether username needs persisting at all, and whether fort ids —
which are structurally 128-bit hex GUIDs plus a tiny suffix — should be a fixed-width value type
instead of interned handles.

A benchmark spike (three representations, Golbat's own rtree/xsync versions, 2M forts + 3.25M
pokemon resident) answered this:

- **GC mark time** — the metric this work arc exists for — is a tie: interned handles and the
  value type both cut it ~28% versus strings.
- The value type drives **heap objects** to the floor (15.3M vs 20.6M; the handle table still
  holds every id string). The handle is ~100 MB tighter on **bytes** and 1.8× vs 1.1× faster on
  the fort scan inner loop (low-traffic today).
- A census of a production database found **every real fort id parses** into the value type,
  including bare 32-char sponsored-fort ids, with exactly one junk row (empty string) in the
  whole database.

Decision: **`[16]byte` GUID + 1-byte numeric suffix, everywhere fort ids live, including
`pokemon.PokestopId`.** On the decisive metric the options tie, so structure wins: the value type
deletes the global table, its unbounded-growth ceilings, its deleted-fort leak, and its resolve
failure modes at ~35 emit sites, rather than engineering around them. The accepted cost is
`Pokemon` returning to the 352 allocator class (~104 MB at 3.25M cached — size-golf we are
deliberately not optimizing) and one mechanical constraint change in the write-behind queue.

Username interning consequently buys nothing (`Pokemon` is in the 352 class regardless), so the
interning machinery exits PR 395 entirely. Username persistence becomes a config option,
**default off**, with the account name threaded transiently from the decode context to the two
consumers that actually need it: the webhook payload and the shiny/duplicate-encounter dedup.

### Goals

1. Remove fort-id string objects from the resident heap (~28% GC mark reduction at scale).
2. No global mutable state on the hot path; no table that outlives its entries.
3. Byte-for-byte identical output at every boundary: DB varchar, JSON API, webhooks, gRPC.
4. A structurally malformed id is an unexpected data format: error log + skip, consistent
   with the rest of the decode path. Suffix drift is not a failure case — the suffix byte is
   numeric, not enumerated, so new suffixes parse natively.
5. Username: stop persisting account names by default without losing webhook enrichment or
   shiny-stat dedup.

### Non-goals

- Incident ids (`incident.id`) — parked in #384, different format, out of scope. (Census
  2026-08-19: the population is uniform random *signed* `int64` rendered in decimal — sign
  split and digit-length distribution match uniform 64-bit randomness, no leading zeros, no
  overflow — so #384 is evidenced and ready to unpark as its own follow-up.)
- Route ids (`route.id`) — not fort ids; only `start_fort_id`/`end_fort_id` convert.
- DB schema changes — every column stays `varchar(35)`.
- A cleanup migration for junk rows (the empty-string pokestop) — may ride along, separate
  decision.
- Interning *on top of* value ids for scan throughput — remains the pre-scoped lever if fort
  scans become hot (CLAUDE.md documents this).

## 2. The `FortId` value type

New file `decoder/fortid.go` (package `decoder` — every consumer lives there; the `db` and
`webhooks` packages only ever see strings).

```go
type FortId struct {
    Guid [16]byte
    // Suffix holds the id's two-hex-digit suffix value directly; 0 means a
    // bare 32-char id. Bare is treated as Niantic's null suffix — the bare
    // form and a literal ".00" (never observed in any census) are the same
    // id, and a ".00" canonicalizes to bare on parse, with a log line,
    // since its existence would be the first evidence for that reading.
    // Any two lowercase hex digits parse — ".ff" included — so the
    // encoding is correct whether Niantic's suffix is decimal or hex, and
    // nothing is enumerated: a new suffix ships without a code change.
    // Byte order matches varchar order by construction: bare (0, the
    // shorter string) sorts first, and lowercase hex digits ascend in
    // ASCII exactly as they ascend in value, so fixed-width lexicographic
    // order equals numeric order.
    Suffix uint8
}
```

17 bytes, `comparable`, zero pointers. The suffix set observed in production is
`.11`/`.12`/`.16` (pokestops, gyms, occasionally stations) and `.23` (stations), plus bare —
no hex-letter suffix has been observed, but the encoding deliberately hardcodes nothing. The
zero value of `FortId` is the **absent/"None" sentinel** (replaces `null.String` validity
where the field is optional): `func (f FortId) Valid() bool`.

### 2.1 Parse and format

`ParseFortId(s string) (FortId, bool)`: accepts 32 lowercase hex chars (→ suffix byte 0) or
32 hex + `.` + any two lowercase hex digits. Hand-rolled nibble table (no intermediate `[]byte`
allocation; measured 218 ns). `String()` formats back losslessly — bare for suffix 0, two
lowercase hex digits otherwise (measured 103 ns, one 35-byte allocation). Every id shape in the
production census round-trips byte-identically; the single non-identity case is a literal
`".00"`, which canonicalizes to the bare form (§2). The spike's round-trip and order-congruence
tests carry into the real implementation.

Implementation shape, borrowed from Go 1.27's `uuid` package (whose storage — a bare
`type UUID [16]byte`, no uint64 halves, byte-loop `Compare` — independently confirms §2's
layout, though its dashed output format is unusable for us):

- **Formatting core is `AppendText(b []byte) ([]byte, error)`** (`encoding.TextAppender`):
  append a 35-byte template in one shot, `hex.Encode` the GUID in place, write the two suffix
  digits; `String()` and `MarshalText()` are one-line derivations. Callers with a buffer
  (batch JSON encoding, SQL arg building) format with zero per-id allocations, and
  `encoding/json` v2 uses the appender automatically.
- **JSON behavior derives from `TextMarshaler`/`TextUnmarshaler`** rather than hand-written
  `MarshalJSON`/`UnmarshalJSON` — both `encoding/json` and goccy honor them, and text
  marshaling also covers JSON map keys. `Valuer`/`Scanner` remain for SQL.
- **`UnmarshalText` parses into a local and assigns only on success** (stdlib's `dst` pattern),
  so a failed parse never half-writes the receiver.
- **Parse keeps the hand-rolled lowercase-only nibble table** — deliberately *not* stdlib
  `hex.Decode`, which accepts uppercase; strict lowercase is what keeps parse↔format bijective
  and in-memory order congruent with the varchar. Format does use `hex.Encode` (it emits
  lowercase, exactly what we want).

Two totality rules, both pinned by tests:

- **`Parse("")` fails.** The empty string must never map to the zero value at a parse site —
  zero is the None sentinel and a real `''` key (one exists in production) must not alias it.
  Call sites where absence is *expected* (e.g. a pokemon with no nearby fort) check for empty
  first, per the existing empty-string-as-absent convention, and use the zero value without
  logging; `ParseFortId` itself only ever sees ids claimed to be present.
- **All-zero GUID with no suffix fails too.** `"0"×32` is syntactically valid hex that would
  otherwise collide with the sentinel. Practically impossible (GUIDs are hash-derived),
  guarded anyway — one comparison on the parse slow path.

### 2.2 Ordering

`FortId.Compare` (guid bytewise, then suffix byte) matches varchar ordering for **every**
representable id, by construction of the suffix encoding (§2) — pinned by the spike's
exhaustive-pairs test including the bare-id case. This is what lets the write-behind's
deterministic lock-order sort and any in-memory sorted iteration stay congruent with
`ORDER BY id`.

### 2.3 Parse failure handling

There is no fallback representation. An id that fails to parse — wrong length, non-hex GUID,
malformed suffix — is treated exactly like any other unexpected data format in Golbat: an
**error log** naming the offending id and the path it arrived on, and the update or row is
skipped. With the suffix un-enumerated, the failure surface is only *structurally* different
ids; a new Niantic suffix parses natively and never reaches this path. The one nonconforming
id in production today (the empty-string pokestop row) simply never loads into memory.

The consequence is accepted deliberately: if Niantic ever changes the id *structure*, affected
forts drop out of Golbat with error logs until the parser is extended — the same posture as
every other proto-format change, which requires a code update regardless.

### 2.4 Boundary adapters

| Boundary | Mechanism |
|---|---|
| DB write | `driver.Valuer`: `String()` for valid ids, `nil` for the zero value (nullable columns); PK columns are never zero by construction (a fort entity cannot be created without a parsed id) |
| DB read | `sql.Scanner`: parse; failure → error log and the loading loop skips that row (a malformed id row never enters memory, and never poisons the rest of its batch — the PR 395 lesson) |
| JSON out (API, webhooks) | `MarshalJSON`: string form; the pokemon webhook's explicit `"None"` sentinel is produced by its existing site checking `Valid()` |
| JSON in (preserve file, API query bodies) | `UnmarshalJSON`: parse; failure → error log, entry skipped (a query id that fails to parse matches nothing) |
| API path/query params | parse at the handler; an unparseable id simply doesn't exist → empty result / 404, never 500 |
| gRPC `InvasionContext.FortId`, raw-JSON `fort_id` | parse at ingest |
| `getFortIdFromContest` | parse the derived substring; failure → error log, contest update skipped |
| `maphash` (station battles) | `h.Write(id.Guid[:])` + suffix byte, replacing `writeString` |

The preserve-on-shutdown pokemon snapshot serializes through JSON: `MarshalJSON`/`UnmarshalJSON`
keep old snapshot files loadable (they contain the string form either way).

## 3. Conversion inventory

What changes type (everything else keeps strings):

**Entity fields** — `PokestopData.Id`, `GymData.Id`, `StationData.Id`,
`Incident.PokestopId`, `Pokemon.PokestopId` (drops `null.String`; zero = absent),
`Tappable.FortId` (same), `Route.StartFortId`/`EndFortId`, `StationBattleData.StationId` and
`stationBattleWrite.StationId`.

**Keys** — `pokestopCache`, `gymCache`, `stationCache`, `getMapFortsCache` →
`OtterCache[FortId, …]`; `fortLookupCache` → `xsync.Map[FortId, FortLookup]`; `fortTree` →
`rtree.RTreeG[FortId]` (+ snapshot, + `newTreeEvictor[FortId]`); every `FortTracker` map/set and
channel payload; `stationBattleCache`; the write-behind queues for pokestop/gym/route/station/
station-battle; `TrackedMutex[FortId]` on the four fort-side entities; all transient
`map[string]struct{}` sets and `[]string` carriers in the scan/GMO/tracker paths.

**Stays `string`** — `incidentCache` key and `Incident.Id` (incident ids, not fort ids);
`Route.Id`; `partner_id` columns (same width, not fort ids — explicitly excluded); every SQL
statement and every JSON wire field (adapters convert at the edge).

## 4. Write-behind queue constraint change

`TypedQueue[K cmp.Ordered, T]` becomes `TypedQueue[K comparable, T]` with a required
`KeyCompare func(K, K) int` in `TypedQueueConfig`. The existing deadlock-avoidance sort
(`typed_queue.go:306`) calls it instead of `cmp.Compare`. Existing queues pass
`cmp.Compare[uint64]` etc.; fort queues pass `FortId.Compare`, which preserves the sort order
byte-for-byte versus today (§2.2). Purely mechanical; no behavior change for non-fort queues.

## 5. Username persistence option

**Config:** top-level `store_username bool` (koanf), **default `false`**, alongside
`pokemon_memory_only`/`preserve_pokemon`.

**Representation:** `Pokemon.Username` stays `null.String`. No interning — with `FortId` at 17
bytes, `Pokemon` sits in the 352 allocator class regardless, so a 4-byte username handle buys
no size class, and the caller-supplied unbounded key set that motivated the PR's open question
never enters a global table at all.

**Off (default):** decode paths never call `SetUsername`; the field stays invalid; the DB
column writes NULL (existing rows' usernames are progressively NULLed as they re-save — this is
the documented meaning of the option); the API returns null.

**Threading (independent of the option):**

- `savePokemonRecordAsAtTime` gains a `username string` parameter. Seven call sites: three in
  `gmo_decode.go` and three in `pokemon_process.go` already have it in scope; `weather_iv.go`
  (proactive IV re-save, no account context) passes `""`.
- **Webhook** (`createPokemonWebhooks`): uses `pokemon.Username` when valid (option on →
  behavior unchanged), else the threaded username. The one semantic change: with the option
  off, webhooks carry the *triggering request's* account rather than the first-seen account.
- **Shiny/duplicate dedup**: `statsSnapshot()` stops copying `pokemon.Username`; the three
  encounter enqueue sites set the snapshot's username from their in-scope decode username.
  Only encounter events consume it, and the dedup semantically wants the *current* account —
  this is a correction, not just a workaround.

## 6. Error handling summary

| Failure | Behavior | Signal |
|---|---|---|
| Nonconforming id at ingest (GMO fort, gRPC invasion, contest-derived, raw JSON) | update skipped | error log naming id + path |
| Nonconforming id in a DB row (preload, bulk loads, tracker pagination) | row skipped, never enters memory | error log |
| Nonconforming id in a preserve-file entry | entry skipped | error log |
| API lookup/query with an unparseable id | empty result / 404, never 500 | — |
| Zero `FortId` reaching a PK write | impossible by construction (entities require a parsed id); asserted in tests | — |

## 7. Testing

- **Unit:** parse/format round-trip over every census shape (all four observed suffixes, bare
  ids) plus arbitrary two-hex-digit suffixes (`.99`, `.ff`, unobserved values — the encoding
  must not privilege the observed set), `".00"` canonicalizing to bare (with its log line),
  `Parse("")` and all-zero-GUID totality rules,
  order-congruence vs strings (exhaustive pairs, carried from the spike), parse-failure
  log-and-skip behavior at each boundary, `Valuer`/`Scanner`/JSON adapters.
- **MariaDB round-trip** (PR 395's pattern, against a real instance): rows written before the
  change load identically; new writes read back as raw strings byte-identical to old writes —
  the test that catches a missing `Valuer` writing garbage into the varchar.
- **Boundary goldens:** webhook payload and API response JSON byte-identical to current output
  for the same entities, including the `"None"` sentinel.
- **Size pins:** `entity_sizes_test.go` updated — `Pokemon` at the 352 class with the new
  fields, fort entities pinned at their new sizes.
- **Perf gates:** protobench for the decode path (parse at ingest is +218 ns/fort — expect
  noise); the spike's scan benchmark shape re-run once on the real types as a one-off sanity
  check, not a committed benchmark.
- Green under both build tags (full/thin), `-race`, `golangci-lint`.

## 8. Sequencing and PR shape

Stacked on PR 394's branch, replacing PR 395's current commits (it is a draft; force-push):

1. **`FortId` type** — type, parse/format, ordering, adapters, full unit tests.
   Standalone, no callers.
2. **`TypedQueue` constraint change** — mechanical, all queues on explicit comparators.
3. **Fort subsystem conversion** — entities, caches, R-tree/evictor, tracker, queues, scan
   paths, boundaries. The big diff; behavior-neutral by the goldens.
4. **`Pokemon.PokestopId` + `Tappable.FortId` conversion** — the pokemon-side win.
5. **Username option + threading** — config, save-path parameter, stats/webhook rerouting.
   Because the branch is rebuilt from PR 394's base, `interned_string.go` and its stats
   collectors never appear in the new history — there is no removal commit.

Steps 1–2 are reviewable in isolation; 3 is where review attention belongs (the
pokestop↔gym conversion path, tracker channel payloads, eviction guards); 4–5 are small.
Whether this lands as one reshaped PR 395 or a short stack on it is a call to make when the
diffs exist.

## 9. Open items

- Cleanup of the one empty-string pokestop row (and any orphaned bare rows that turn out to be
  dead sponsored stops) — offered as an optional migration, not part of this change.
- `station.id` occasionally arrives with `.11`/`.12`/`.16` suffixes (census-confirmed): no code
  consequence (the suffix byte carries any value), noted so nobody "fixes" it later.
