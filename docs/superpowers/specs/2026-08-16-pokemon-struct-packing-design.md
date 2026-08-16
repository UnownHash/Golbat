# PokemonData struct packing

**Date:** 2026-08-16
**Status:** Design approved, not yet implemented
**Scope:** Shrink the cached `Pokemon` entity below Go's 512-byte allocation
threshold by narrowing nullable field wrappers, reordering for alignment, and
deleting three fields that cost memory without earning it.

## Motivation

The pokemon cache is the largest single consumer of Golbat's heap. A cached
`Pokemon` is currently 800 bytes (`PokemonData` 592 of that), landing in Go's
896-byte size class, and with a populated `PokestopId` costs about 951 bytes per
live entry. At the evening-peak scale documented in
`docs/cache-investigation-brief.md` (~10M live entries) that is roughly 9.5 GB
of entity data alone.

The reason to act on this is not the byte count. It is a threshold effect that
turned up while measuring: **Go's garbage collector treats objects above 512
bytes substantially worse, and treats zero-pointer objects as `noscan` and skips
them entirely.** Measured across 5M live entries with forced GC (go1.26.5,
darwin/arm64, mean of 5 runs):

| object shape | GC mark |
| --- | --- |
| 800 B, 12 pointers (the real `Pokemon`) | 279.7 ms |
| 800 B, 1 pointer | 271.6 ms |
| 800 B, 0 pointers | 62.4 ms |
| 512 B, 1 pointer | 62.6 ms |
| **520 B, 1 pointer** | **194.4 ms** |
| 256 B, 1 pointer | 33.2 ms |

Reducing pointer count from 12 to 1 buys 3%. Adding 8 bytes across the 512-byte
boundary costs 3.1x. **Getting under 512 bytes is the entire win available from
layout work**, and this design targets that threshold rather than byte count for
its own sake.

A second, independent benchmark (1M entities, `map[uint64]*T`, baseline-subtracted
`MemStats`, GC forced via `debug.SetGCPercent(5)` following the method documented
in `cachebench/scenario_test.go:548-576`) corroborates the direction: the current
layout costs 896 heap bytes/entity and 5.985 s of mark CPU; a packed layout costs
448 bytes/entity and 4.621 s; a pointer-free layout costs 288 bytes/entity and
2.722 s.

### What this is worth, honestly

`docs/decode-performance-findings.md` measures Golbat's total GC share at roughly
5% of CPU via the `protobench` harness. Pokemon objects are the largest single
contributor to that but compete with ~1M forts, millions of spawnpoints, the
lookup caches, and the S2 geofence structures. So the CPU saving is on the order
of low single-digit percent, not a transformation. The RSS saving is the more
concrete benefit: roughly 480 bytes per cached pokemon once Go's size classes are
accounted for (see [Expected result](#expected-result)), about 2.4 GB at 5M.

This estimate should be checked against a production profile before or alongside
implementation — see [Verification](#verification).

## Non-goals

- **Off-heap / arena storage.** A pointer-free slab measured better still
  (~119 bytes/entry, near-zero mark cost), but packing captures roughly 90% of
  its RSS win and 87% of its mark-time win. The remainder does not justify a slab
  allocator, free list, `unsafe` access, and the loss of per-entity lock
  diagnostics. This design deliberately leaves that door open: once field access
  goes through a wrapper type, swapping the backing store later is a much smaller
  change than doing it from scratch.
- **A Redis or other remote entity store.** Evaluated separately and rejected;
  the R-tree and lookup caches cannot leave the process, and per-entity
  read-modify-write cannot tolerate a network hop.
- **Changing the database schema.** Every narrowed Go type matches a column width
  the schema already declares. No migration required.
- **Changing `Pokemon`'s locking model, cache behavior, or TTL handling.**
- **`Pokestop` (1176 B) and `Gym` (992 B).** Both are far over the threshold and
  deserve the same treatment, but ~1M forts against millions of pokemon makes the
  payoff proportionally smaller. Separate spec.

## Approaches considered

| | `PokemonData` | `Pokemon` (est.) | verdict |
| --- | --- | --- | --- |
| **A** — single `ValidMask uint64`, plain narrow types | 168 B | ~346 B | smaller, riskier |
| **B** — narrow null wrapper types | 232 B | ~410 B | **chosen** |
| **C** — deletions and reordering only, no narrowing | 647 B | — | rejected |

C is rejected because it does not clear 512, so it buys 1.5x instead of 6.9x.
The cheap deletions are worth doing, but only as part of the narrowing pass — on
their own most of their value is stranded.

**B is chosen over A** at a cost of 64 bytes per pokemon (about 320 MB at 5M,
against ~2.4 GB saved either way), because:

- **No `PokemonRow` shim.** B's types implement `sql.Scanner` and `driver.Valuer`,
  so all six sqlx call sites keep working untouched. A required a parallel
  DB-shaped struct and an explicit conversion on every read and write.
- **A whole failure mode disappears.** Under A, a NULL column scanned into a bare
  `uint8` fails inside `database/sql`'s `convertAssign` — at runtime, on the first
  row with a null `gender`, not at compile time. B cannot fail that way.
- **Far smaller diff.** B's wrappers carry `.Valid`, `.ValueOrZero()` and `.Ptr()`
  with the same semantics as `guregu/null`, so most read sites compile unchanged.
  A required renaming roughly 250 field reads to accessor-method calls, because
  Go forbids a field and method sharing a name.
- **No mask bookkeeping** in 33 setters.

A is smaller; B is simpler and safer. At 64 bytes, take B.

**Accepted wart:** `NullUint64` is 16 bytes, identical to today's `null.Int`, so
`SpawnId` and `CellId` gain nothing under B. A hybrid using a small mask for just
those two recovers 16 bytes for real added complexity. Not worth it.

## Design

### The `nulltypes` package

A new package (roughly 150 lines plus tests) providing `NullUint8`, `NullUint16`,
`NullUint32`, `NullUint64`, `NullFloat32`, and `NullBool`. Each is a struct of a
sized value plus a `Valid bool`:

```go
type NullUint8 struct {
    V     uint8
    Valid bool
}
```

Measured sizes: `NullUint8` 2 B, `NullUint16` 4 B, `NullUint32` 8 B,
`NullUint64` 16 B, `NullFloat32` 8 B, `NullBool` 2 B — against 16 B for every
`guregu/null` numeric type, which is that size because it embeds
`sql.NullInt64`.

Each type implements:

- `sql.Scanner` and `driver.Valuer` — so sqlx keeps working with no shim
- `ValueOrZero()`, `Ptr()`, `IsZero()` — so existing call sites keep compiling
- `MarshalJSON` / `UnmarshalJSON` producing output byte-identical to
  `guregu/null`'s — the API endpoints and webhook payloads are public contracts

**This API surface is the compatibility contract for the whole change.** The
package has no Golbat dependencies and is testable in complete isolation, which
is why it lands first and alone.

We define our own rather than extending `guregu/null` because that is a
third-party dependency; vendoring or forking it is worse than owning 150 lines.

### Field table

Types narrow to the width the schema already declares. `Lat`/`Lon` stay
`float64` — the column is `double(18,14)` and the precision is load-bearing for
spatial matching.

| field | current | MariaDB column | proposed |
| --- | --- | --- | --- |
| `Id` | `Uint64Str` (8) | `varchar(25)` PK | unchanged (8) |
| `PokestopId` | `null.String` (24) | `varchar(35)` | unchanged (24) |
| `SpawnId` | `null.Int` (16) | `bigint unsigned` | `NullUint64` (16) |
| `Lat` | `float64` (8) | `double(18,14)` NOT NULL | unchanged (8) |
| `Lon` | `float64` (8) | `double(18,14)` NOT NULL | unchanged (8) |
| `Weight` | `null.Float` (16) | `double(18,14)` | `NullFloat32` (8) |
| `Size` | `null.Int` (16) | `tinyint unsigned` | `NullUint8` (2) |
| `Height` | `null.Float` (16) | `double(18,14)` | `NullFloat32` (8) |
| `ExpireTimestamp` | `null.Int` (16) | `int unsigned` | `NullUint32` (8) |
| `Updated` | `null.Int` (16) | `int unsigned` | `NullUint32` (8) |
| `PokemonId` | `int16` (2) | `smallint unsigned` NOT NULL | unchanged (2) |
| `Move1` | `null.Int` (16) | `smallint unsigned` | `NullUint16` (4) |
| `Move2` | `null.Int` (16) | `smallint unsigned` | `NullUint16` (4) |
| `Gender` | `null.Int` (16) | `tinyint unsigned` | `NullUint8` (2) |
| `Cp` | `null.Int` (16) | `smallint unsigned` | `NullUint16` (4) |
| `AtkIv` / `DefIv` / `StaIv` | `null.Int` (16 each) | `tinyint unsigned` | `NullUint8` (2 each) |
| `GolbatInternal` | `[]byte` (24) | `tinyblob` | unchanged (24) |
| `Iv` | `null.Float` (16) | `float(5,2)` nullable | `NullFloat32` (8) |
| `Form` | `null.Int` (16) | `smallint unsigned` | `NullUint16` (4) |
| `Level` | `null.Int` (16) | `tinyint unsigned` | `NullUint8` (2) |
| `IsStrong` | `null.Bool` (2) | `BOOLEAN` nullable | `NullBool` (2) |
| `Weather` | `null.Int` (16) | `tinyint unsigned` | `NullUint8` (2) |
| `Costume` | `null.Int` (16) | `tinyint unsigned` | `NullUint8` (2) |
| `FirstSeenTimestamp` | `int64` (8) | `int unsigned` NOT NULL | `uint32` (4) |
| `Changed` | `int64` (8) | `int unsigned` NOT NULL | `uint32` (4) |
| `CellId` | `null.Int` (16) | `bigint unsigned` (S2 cell id) | `NullUint64` (16) |
| `ExpireTimestampVerified` | `bool` (1) | `tinyint unsigned` NOT NULL | unchanged (1) |
| `DisplayPokemonId` | `null.Int` (16) | `smallint unsigned` | `NullUint16` (4) |
| `DisplayPokemonForm` | `null.Int` (16) | `smallint unsigned` | `NullUint16` (4) |
| `IsDitto` | `bool` (1) | `BOOLEAN` NOT NULL | unchanged (1) |
| `SeenType` | `null.String` (24) | `enum`, 8 values | `NullUint8` (2) |
| `Shiny` | `null.Bool` (2) | `tinyint(1)` | `NullBool` (2) |
| `Username` | `null.String` (24) | `varchar(32)` | unchanged (24) |
| `Capture1` / `Capture2` / `Capture3` | `null.Float` (16 each) | `double(18,14)` | **dropped** |
| `Pvp` | `null.String` (24) | `text` | unchanged (24) |
| `IsEvent` | `int8` (1) | `tinyint unsigned` NOT NULL | unchanged (1) |

### Field ordering

Fields must be declared in descending alignment order — 8-byte, then 4, then 2,
then 1, then the string and slice headers. This is not cosmetic. On the approach
A prototype, narrowing every type but keeping the original declaration order
measured **264 bytes**; the same types reordered measured **232 bytes**.
Reordering was worth 32 bytes there, about 12% of the struct, and cost nothing.
The lesson transfers directly to approach B, whose 232-byte figure already
assumes the ordered layout — declare the fields out of order and it will not
reproduce.

### Why `Iv` is kept

An earlier draft of this design dropped `Iv` as a derived value, on the strength
of the schema comment at `decoder/pokemon.go:113` declaring the column
`GENERATED ALWAYS AS (((atk_iv + def_iv + sta_iv) * 100) / 45) VIRTUAL`.

**That comment is stale.** `sql/11_ivchanges.up.sql` drops the generated column
and adds a plain nullable `float(5,2)` in its place. The column is real and
writable, `pokemonBatchUpsertQuery` writing `:iv` is correct, and there is no
pre-existing bug to fix here.

`Iv` could still be dropped from memory and recomputed, since its value is
genuinely derived from the three IV fields. That would mean replacing `:iv` with
an arithmetic expression in the upsert, removing `iv` from
`pokemonSelectColumns`, and computing at four call sites
(`api_pokemon_response.go:111`, `pokemonRtree.go:256-258`, and two setters in
`pokemon_decode.go`). It saves 8 further bytes.

Not worth it. The threshold is cleared with roughly 100 bytes to spare either
way, and one of those call sites is on the public API response path. Narrow it to
`NullFloat32` and leave the logic alone.

### The three deletions

**`Capture1` / `Capture2` / `Capture3`** — functionally dead. Absent from both
`pokemonSelectColumns` (`decoder/pokemon_state.go:28-31`) and
`pokemonBatchUpsertQuery`, so they are neither loaded nor persisted. Their
setters (`decoder/pokemon.go:453-473`) have no callers outside `pokemon.go`. The
only read is `decoder/pokemon_state.go:475-477`, copying `.ValueOrZero()` into a
webhook payload — always 0, because nothing ever sets them.

**The webhook payload keeps its `capture_*` fields**, sourced from a literal `0`,
so external consumers see exactly what they see today. Only the storage on
`PokemonData` goes away.

**`changedFields []string`** on the `Pokemon` wrapper — `dbDebugEnabled` is a
build-tag `const` (`decoder/db_debug_off.go:7`), false in production builds, so
every `append` is const-folded away and the slice is never allocated. The 24-byte
header remains regardless, and one of its words sits in the GC pointer bitmap.
Move the field behind the same build tag, or drop it and have the debug path
build a local slice.

**`SeenType` to `NullUint8`** — the column is an enum of 8 values
(`decoder/pokemon_decode.go:308-315`, widened to 8 by
`sql/45_tappables_seen_type_lure.up.sql`). It currently costs a 24-byte string
header plus a heap pointer to carry one of eight values. Define a constant per
value with `String()` and parse helpers; the `sql.Scanner`/`Valuer` pair converts
at the database boundary and JSON marshalling converts at the API and webhook
boundaries, so both wire formats are unchanged.

The four-value enum at `decoder/pokemon.go:117` and in
`sql/1_rdmdb_tables.up.sql` is a stale schema comment. The migrations are
authoritative.

### The schema comment is not to be trusted

Three separate claims in the block comment at `decoder/pokemon.go:88-140` are
out of date, and two of them nearly sent this design the wrong way:

- `iv` is described as `GENERATED ALWAYS AS ... VIRTUAL`;
  `sql/11_ivchanges.up.sql` replaced it with a plain writable column.
- `seen_type` is described as a four-value enum;
  `sql/45_tappables_seen_type_lure.up.sql` widened it to eight.
- `size` is described as `double(18,14)`; `sql/7_add_height_size.up.sql`
  renamed that column to `height` and added a new `size tinyint unsigned`.

**Verify every column type against `sql/*.up.sql` before narrowing it**, not
against the comment. Refreshing the comment to match is worth doing as part of
this work.

### Expected result

- `PokemonData`: **232 bytes**, from 592 (measured on a prototype).
- `Pokemon` wrapper, also dropping `changedFields`: **~410 bytes**, from 800 —
  under the 512 threshold with roughly 100 bytes of headroom. This figure is
  computed by summing components, not measured, and confirming it is the first
  task of the plan.
- **What the allocator actually returns matters more than the struct size.** Go
  rounds every allocation up to a size class: 800 B lands in the 896 class, and
  ~400 B lands in the 416 class. So the real per-entity saving is roughly
  **480 bytes**, not 400 — about 2.4 GB at 5M cached pokemon.
- The size-class table is also why headroom is worth guarding. Growing the
  wrapper past 416 bytes silently costs 32 bytes per pokemon at the next class
  boundary, and past 512 costs 3x GC mark. Hence the assertion test in
  [Verification](#verification).

## Compatibility

### Setters

All 33 `SetXxx` methods on `Pokemon` keep their `null.Int` / `null.Float` /
`null.Bool` / `null.String` parameter types, converting to the narrow type inside
the method body. Writes only ever go through these methods, so their roughly 150
external call sites need no changes.

**Range checking.** A `null.Int` carries an `int64`; the field may be a `uint8`.
Values are bounded game data, so an out-of-range value means a protocol change
rather than a normal case — but silently truncating a gender or an IV to garbage
is worse than noticing. On out-of-range: clamp to the type's maximum and
increment a Prometheus counter, so the condition is visible without being fatal.

The alternative — storing `Valid=false`, treating out-of-range as null — was
considered and rejected: a clamped IV is closer to the truth than a missing one.

### Field reads

Most read sites compile unchanged, because the wrappers carry `.Valid`,
`.ValueOrZero()` and `.Ptr()` with `guregu/null` semantics. What breaks is
narrower and self-announcing: `ValueOrZero()` now returns `uint8`/`uint16`/etc.
rather than `int64`, so sites feeding the result to something expecting `int64`
need an explicit cast. Every one of those is a compile error.

Roughly 487 `pokemon.<Field>` occurrences exist across `decoder/`, concentrated
in `pokemon_state.go` (85), `pokemon_decode.go` (~130), `pokemon.go` (106),
`stats.go` (59), `api_pokemon_response.go` (42), `pokemonRtree.go` (29),
`weather_iv.go` (12), the v2/v3 scan endpoints (~12 combined),
`pokemon_preserve.go` (4) and `pokemon_process.go` (3). Under approach B only the
subset with a type mismatch needs touching, and the compiler enumerates it.

`decoder/pokemon_decode.go` additionally assigns `AtkIv` / `DefIv` / `StaIv` /
`Iv` directly, bypassing the setters. Those assignments must move to setter calls
so range checking is not skipped.

Two traps for whoever does the pass: `api_pokemon_scan_v2.go` and `_v3.go` each
contain a second, unrelated loop variable also named `pokemon` (an
`ApiPokemonDnfId` shape, distinguishable because its fields are compared against
literal `nil`, which `Uint64Str` and `null.Int` never can be). And
`decoder/station_decode.go` has a `pokemon` that is a
`*pogo.PlayerClientStationedPokemonProto`, unaffected by any of this.

Everything above is inside package `decoder`. No other package reads these fields
individually, which caps the blast radius.

### The sqlx boundary

Six call sites bind to `PokemonData`'s field types by reflection over `db` tags:

| site | operation |
| --- | --- |
| `decoder/pokemon_state.go:47` | `GetContext` single-row load |
| `decoder/pokemon_preserve.go:169` | `StructScan` in the bulk preload loop |
| `decoder/pokemon_preserve.go:65` | `NamedExecContext` preserve batch upsert |
| `decoder/writebehind_batch.go:237` | `NamedExecContext` write-behind flush |
| `decoder/pokemon_state.go:307` | `NamedExecContext` raw INSERT |
| `decoder/pokemon_state.go:330` | `NamedExecContext` raw UPDATE |

Under approach B all six work unchanged, because `sql.Scanner` and
`driver.Valuer` are exactly the interfaces sqlx reflects over. This is the
primary reason B was chosen. The correctness of that claim rests entirely on the
`nulltypes` tests, which is why they land first.

## Migration

Four commits, each independently compiling and green:

1. **`nulltypes` package with full tests.** Nothing uses it yet. Tests cover
   NULL and non-NULL scan, `Valuer` round-trip, JSON output byte-compared against
   `guregu/null` for the same values, and the boundary values of each width.
2. **Repack `PokemonData`; rewrite the 33 setters** to convert and range-check.
   Delete `Iv`, `Capture1/2/3` and `changedFields`; narrow `SeenType`; reorder
   fields by descending alignment. Add `IvFloat()`.
3. **Fix the read sites the compiler flags** — casts where `ValueOrZero()` width
   changed, setter calls where `pokemon_decode.go` assigned directly.
4. **Drop `changedFields`** behind the debug build tag and add the size-assertion
   test proving `Pokemon` is under 512.

Commits 2 and 3 will not compile independently — the type change and the
call-site fixes are one atomic unit. Note that in the commit message rather than
splitting them artificially.

**Effort: 4–6 engineer-days** including tests and review. The `nulltypes` package
and its tests are roughly a day; the repack and setter rewrite another; the
compiler-driven fix pass and the DB round-trip testing are the rest.

## Risks

| risk | mitigation |
| --- | --- |
| `nulltypes` NULL handling differs subtly from `guregu/null` | Tests compare JSON output byte-for-byte and cover NULL/non-NULL scan for every type. This package is the single point of failure for the whole change, so it lands alone in commit 1. |
| Silent precision loss narrowing `null.Float` to `float32` | `Weight`/`Height` are approximate game-supplied values; `Lat`/`Lon` deliberately stay `float64`. Assert tolerance in tests. |
| Out-of-range value clamped where it should have been noticed | Prometheus counter on every clamp; alert if it is ever non-zero. |
| `uint32` timestamps overflow in 2106 | Matches the existing `int unsigned` columns. No regression, but noted. |
| A missed read site compiles silently | It cannot — the types genuinely differ, so every affected site is a compile error. This is a property to rely on. |
| Webhook or API consumers see changed output | JSON marshalling is byte-compared against `guregu/null` in commit 1's tests; `capture_*` fields stay in the payload sourced from `0`, which is what consumers already receive. |

## Verification

- `go build -tags go_json ./...` and `go test -tags go_json ./decoder/ ./decoder/nulltypes/` green at every commit.
- `golangci-lint run` clean.
- **An `unsafe.Sizeof` assertion test pinning `PokemonData` and `Pokemon`.** This
  is the durable value of the whole change — without it, the next feature that
  adds a field pushes `Pokemon` back over 512 bytes, costs 3x GC mark, and
  nothing fails.
- An integration test against a real MariaDB covering a row with every nullable
  column NULL through a full write-read cycle. CI defines no database service
  today, so this likely runs locally or requires adding one.
- Before/after heap comparison on a real instance: `profile_routes = true`, then
  `/debug/pprof/heap`.

### Measure the premise first

Independently of implementation, run `/debug/pprof/profile` on a production
instance, check what share of CPU sits in `runtime.gcDrain` and
`runtime.scanobject`, and compare `HeapAlloc` against RSS.

- If GC is around 5% and live heap is well below RSS, most resident memory is
  collector headroom. `tuning.go_mem_limit_mib` (implemented at
  `main.go:246-252`, unset by default) is a one-line lever that beats this entire
  change on RSS, and this work is then worth doing for CPU and headroom rather
  than as a memory fix.
- If live heap is close to RSS, the packing is the right tool and the estimate
  above holds.

That measurement takes about 30 minutes and should gate the effort.

## Follow-ups

- **`PokemonOldValues`** (80 B) holds three nullable fields that could take the
  same treatment. Left alone here to keep the change bounded.
- **Interning `PokestopId`** — a ~35-character fort ID repeated across many
  pokemon at the same stop. Measured at 3.4% on its own, and it removes a heap
  object per entry, but it needs a concurrent intern table with a lifecycle story
  and does not affect the 512-byte threshold.
- **`TrackedMutex` allocates one heap object per Lock/Unlock pair**, boxing the
  caller string into its `atomic.Value`. That is allocation-rate pressure on
  every entity access, unrelated to this design, but it surfaced during
  measurement and deserves its own look.
