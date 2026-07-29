# Request-first lure pokemon processing

**Date:** 2026-07-29
**Issues:** [#390](https://github.com/UnownHash/Golbat/issues/390) (lure pokemon unqueryable after pokestop lookup failure), [#283](https://github.com/UnownHash/Golbat/issues/283) (disk encounter cache no longer needed)

## Problem

### The bug (#390)

The map-pokemon loop in `UpdatePokemonBatch` (`decoder/gmo_decode.go:179-197`) commits records without validating placement:

1. `getOrCreatePokemonRecord` creates an empty record; `updateFromMap`
   (`decoder/pokemon_decode.go:180`) returns early when the pokestop lookup
   fails, discarding the error — a transient DB error and an unknown stop are
   indistinguishable.
2. The caller saves anyway: `newRecord` is cleared, the pokemon is R-tree
   indexed at (0,0), `pokestop_id` and `expire_timestamp` stay null.
3. The poisoning is permanent: `updateFromMap` starts with
   `if !isNewRecord() return`, so no later GMO can repair the record. A disk
   encounter then adds IVs to the husk and fires webhooks (including hundo
   alerts) at lat/lon 0 with `disappear_time` 0, while the v2 scan API —
   which drops records without a future expiry — never shows it.

### The structural fragility (#283)

`diskEncounterCache` exists only because `DiskEncounterOutProto` (the
response) carries no location, so Golbat holds the response until a GMO names
the fort. But the *request* proto (`DiskEncounterProto`) carries
`encounter_id`, `fort_id`, and the fort's real coordinates
(`gym_lat_degrees`/`gym_lng_degrees`). The ingest already plumbs request
bytes (`ProtoData.Request`, used by ~6 other methods); `decodeDiskEncounter`
(`decode.go:397`) simply ignores them and instead uses
`Pokemon.PokemonDisplay.DisplayId` as an encounter-ID stand-in.

The one fact the request cannot supply is the despawn time — only the GMO's
`MapPokemonProto.ExpirationTimeMs` has it.

### The unnecessary lookup

A `MapPokemonProto` never travels alone: it arrives as `fort.ActivePokemon`
inside the fort's own GMO entry (`decode.go:518-519`), which carries
`FortId`, `Latitude`, and `Longitude`. The extraction loop discards those
fields, and `updateFromMap` then tries to reconstruct them via a pokestop
cache/DB lookup — the very lookup whose failure causes #390. The lookup can
fail even though the fort is in the same payload: `UpdateFortBatch` is gated
by `ProcessGyms || ProcessPokestops` while map pokemon are processed under
`ProcessPokemon`, so a pokemon-only scan context never populates the fort
cache.

The pokestop record lookup in the lure flow only ever served placement
(coordinates); the `pokestop.Id` it copies is just `SpawnpointId` echoed
back. (Webhook pokestop-*name* enrichment at `pokemon_state.go:435` is a
separate, nil-safe path and is unaffected.)

## Decisions taken during design

- **Request payloads are reliably present** for `METHOD_DISK_ENCOUNTER` in
  the deployments that matter (maintainer confirmed). The request becomes
  essential — same posture as `GetStationDetails` — and the legacy cache path
  is deleted rather than kept as a fallback.
- **Lure spawn lifetime is 3 minutes** (a lure spits out a new pokemon every
  3 minutes, each lasting 3 minutes). An encounter-created record therefore
  gets a **now+180s unverified** expiry estimate (worst-case remaining life),
  not the 20-minute `setUnknownTimestamp` convention used for wild spawns.
- **The lure fort's identity and coordinates are captured at GMO extraction**
  (maintainer insight: the fort is always in the same GMO as its lure
  pokemon). No lure path performs a pokestop record lookup — placement can
  no longer fail, eliminating the #390 failure mode structurally instead of
  guarding it.
- **Approach chosen:** request-first processing (below), over (a) a minimal
  #390-only fix, and (b) the maximal #283 reading that drops GMO lure
  processing entirely — rejected because it loses verified despawns and
  `lure_wild` visibility for mons nobody encounters.

## Design

### Model

Each proto contributes only what it alone knows, in any arrival order, to
the shared record keyed by encounter ID:

| Source | Contributes |
|--------|-------------|
| `DiskEncounterProto` (request) | encounter ID, fort ID, fort coords — placement for `lure_encounter` |
| `DiskEncounterOutProto` (response) | species, IVs, CP, level (existing apply, unchanged) |
| GMO fort entry (`fort.ActiveFortPokemon[]` LURE wrappers; legacy singular `fort.ActivePokemon`) | fort ID + coords (placement for `lure_wild`), verified `ExpirationTimeMs`, display, cell ID |

`diskEncounterCache` is deleted — nothing ever waits for anything, and no
lure path consults the pokestop record.

**Invariant:** a record is *placed* when it has a pokestop ID and
coordinates. An unplaced record is never committed: never saved, never
R-tree indexed, never webhooked. (Both creation paths now always have
placement data in hand, so this holds structurally.)

**Placement happens exactly once, at record creation** (`isNewRecord()`),
by whichever proto arrives first. Pre-existing #390 husks need no repair
path: lure pokemon live 3 minutes, and deploying the fix restarts Golbat —
any poisoned records are long gone (maintainer decision).

### Component changes

**`decode.go` — GMO extraction (`decode.go:518-519`)**
- `RawMapPokemonData` gains `FortId string` and `Lat, Lon float64`,
  populated from the enclosing fort entry (`fort.FortId`, `fort.Latitude`,
  `fort.Longitude`) at extraction time.
- Lure pokemon are collected from both the repeated
  `fort.ActiveFortPokemon` wrappers (`SpawnType == LURE`, nested
  `MapPokemonProto`) and the legacy singular `fort.ActivePokemon`,
  deduplicated by encounter ID. Live captures (PR #391 review) show current
  clients send only the repeated form, with zero lat/lon on the nested
  proto — the enclosing fort's coordinates are the only usable ones, which
  the capture-at-extraction design already provides. `POWER_UP` wrapper
  entries are not lures and never enter this path.

**`decode.go` — `decodeDiskEncounter`**
- Gains the request parameter (`protoData.Request`).
- Nil or unparseable request →
  `IncDecodeDiskEncounter("error", "request_missing" | "request_parse")`,
  skip processing.
- Non-success response handling unchanged.
- Passes both request and response to the decoder layer.

**`decoder/pokemon_process.go` — `UpdatePokemonRecordWithDiskEncounterProto`**
- Takes the request proto. Encounter ID comes from `request.EncounterId`;
  log a warning if it disagrees with the old `DisplayId` stand-in, which is
  removed.
- `getPokemonRecordForUpdate` → `getOrCreatePokemonRecord`.
- If the record is new: pokestop ID from `request.FortId`, coordinates from
  `request.GymLatDegrees/GymLngDegrees` (no pokestop lookup); if there is no
  verified expiry, set now+180s unverified.
- Then the existing `updatePokemonFromDiskEncounterProto` apply (display,
  `lure_encounter` seen type, IVs), save, stats event.
- The `diskEncounterCache.Set` stash branch is deleted.

**`decoder/pokemon_decode.go` — `updateFromMap`**
Restructured from `if !isNewRecord() return` into a merge that returns
`saveNeeded bool`; the pokestop record lookup is removed:
- *New record*: pokestop ID and coordinates come from the captured fort
  fields on `RawMapPokemonData`. Placement cannot fail.
- *Placed record*: contribute only GMO-owned facts — verified expiry from
  `ExpirationTimeMs` when not already verified (including the
  `encounterCache.UpdateTTL` call), cell ID, username-if-missing. Never
  downgrade `lure_encounter` → `lure_wild`; never touch encounter data.
  Return `false` when nothing changed, so repeated GMO sightings of the same
  lure mon do not generate saves (preserving today's early-return economy).
- This restructure also removes the existing quirk where the `else` branch
  could flip `ExpireTimestampVerified` back to false.
- The redundant `pokemon.Id =` assignment is removed
  (`getOrCreatePokemonRecord` already sets it).

**`decoder/gmo_decode.go` — map-pokemon loop**
- `if pokemon.updateFromMap(...) { savePokemonRecordAsAtTime(...) }`.
- The disk-cache lookup/apply block is deleted.

**`decoder/main.go`**
- `diskEncounterCache` declaration and construction deleted.

### Order matrix

| Arrival order | Outcome |
|---------------|---------|
| GMO first (any fort-cache state) | `lure_wild` placed from the same GMO's fort entry + verified expiry; encounter later adds IVs directly — no cache hop. Works even in pokemon-only scan contexts where the fort cache is never populated |
| Encounter first | Full record immediately: request coords, `lure_encounter`, ≤3-min estimated expiry; later GMO tightens to verified. (Previously: response waited in cache, hoping for a GMO) |
| Encounter only, no GMO ever | Visible for its estimated lifetime with correct coords — strictly better than today (stashed proto, then nothing) |

The old failure case — GMO whose fort Golbat doesn't know — no longer
exists: placement data rides in the same payload on both paths.

### Error handling and observability

- Missing/unparseable requests are visible in the existing
  `IncDecodeDiskEncounter` counter labels.
- The discarded-lookup-error problem from #390 is moot: the lookup no longer
  exists. No new Prometheus series.

### Testing

Package-level regression tests in `decoder` mirroring the issue's repro and
the order matrix:

1. GMO whose fort is absent from cache and DB (pokemon-only scan context):
   record is fully placed from the captured fort fields — correct coords,
   verified expiry, `lure_wild`, R-tree entry at the fort, webhook with real
   lat/lon.
2. Encounter-created record: request coords, now+180s unverified expiry,
   `lure_encounter`, webhook carries real lat/lon.
3. GMO after encounter: merges verified expiry; seen type not downgraded;
   no-change GMO replay does not save.
4. GMO first + encounter later: unchanged behavior, no cache involved.
5. `decodeDiskEncounter` with missing request: skipped and counted.
6. v2 scan API returns the estimated-expiry record (the visibility symptom
   from the issue).

### Out of scope

- Webhook re-emission policy.
- Wild/nearby/tappable seen-type flows.
- Config knob for the 180s estimate.
- Any change to encounter stats handling.
- Webhook pokestop-name enrichment (existing nil-safe path, untouched).
