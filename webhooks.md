# Golbat Webhooks Reference

This document defines every outbound webhook Golbat sends. It is intended as a
specification for implementers building webhook receivers: every field is
documented with its source, type, JSON encoding, and the conditions under which
the webhook fires.

The authoritative source is the code under `decoder/` and `webhooks/`. If this
document and the code disagree, the code wins — please update this document.

---

## Table of Contents

- [Transport](#transport)
- [Envelope](#envelope)
- [Common conventions](#common-conventions)
- [Area filtering](#area-filtering)
- [Webhook types](#webhook-types)
  - [pokemon (PokemonIV / PokemonNoIV)](#pokemon)
  - [gym_details](#gym_details)
  - [raid](#raid)
  - [pokestop](#pokestop)
  - [quest](#quest)
  - [invasion](#invasion)
  - [weather](#weather)
  - [fort_update](#fort_update)
  - [max_battle](#max_battle)
- [Configuration](#configuration)

---

## Transport

Golbat delivers webhooks over HTTP:

- **Method**: `POST`
- **Content-Type**: `application/json`
- **Header**: `X-Golbat: hey!`
- **Additional headers**: any entries from the webhook's `header_map` config
- **Body**: a JSON array of [envelopes](#envelope)
- **URL**: as configured per webhook in `config.toml`

Golbat batches messages and flushes them on a configurable interval (default
1 second). A single POST can contain many envelopes, possibly of different
types. The receiver MUST accept the array form and iterate.

The response body is read and discarded. Any 2xx/3xx/4xx/5xx response is
accepted and logged; Golbat does not retry. Receivers that need reliability
MUST idempotently process each envelope, because the same entity may fire
multiple webhooks over its lifetime (see firing-condition sections below).

The HTTP client is `net/http` with default settings; there is no configured
timeout.

---

## Envelope

Every message, regardless of type, is wrapped in this envelope:

```json
{
  "type": "pokemon",
  "message": { ... }
}
```

| Field     | Type   | Description |
|-----------|--------|-------------|
| `type`    | string | One of: `pokemon`, `gym_details`, `raid`, `quest`, `pokestop`, `invasion`, `weather`, `fort_update`, `max_battle`. |
| `message` | object | Type-specific payload; see the sections below. |

Area names are **not** included in the envelope — they are applied server-side
for routing only (see [Area filtering](#area-filtering)).

A single POST body looks like:

```json
[
  { "type": "pokemon",     "message": { ... } },
  { "type": "pokemon_iv",  "message": { ... } },
  { "type": "raid",        "message": { ... } },
  { "type": "fort_update", "message": { ... } }
]
```

Note that a few payload types (notably `pokemon` for both `PokemonIV` and
`PokemonNoIV` variants) use the same `type` string even though they are
classified internally as different webhook types.

---

## Common conventions

### Timestamps

Unless otherwise noted, every timestamp is **Unix seconds** (since 1970-01-01
UTC). The sole exception is the `weather` webhook's internal `UpdatedMs` field,
which is stored in milliseconds and divided by 1000 before being emitted.

### Nullable fields (`null.Int`, `null.String`, `null.Bool`, `null.Float`)

Golbat uses `github.com/guregu/null/v6` for nullable values. These fields
serialize as:

- **Valid**: the underlying value (number, string, boolean, float).
- **Invalid / not set**: the JSON literal `null`.

They are **not** omitted with `omitempty`; a null field is always present in
the payload. Receivers should expect `null` and treat it as "unknown / not
set".

### Coordinate precision

`latitude` and `longitude` are IEEE-754 `float64`, stored in the database with
up to 14 decimal places. Treat them as full-precision doubles.

### Enums

Enum-valued fields (`gender`, `weather gameplay_condition`, invasion
`character`, raid `alignment`, lure `lure_id`, etc.) are emitted as raw
numeric values from the underlying Niantic protobuf definitions. Golbat does
not translate them. Receivers that need human-readable names should maintain
their own mapping table derived from the `.proto` files in `pogo/`.

### Coerced-to-zero fields

Several fields use `.ValueOrZero()` during webhook construction: a null
database value is emitted as `0` (int), `""` (string), or `0.0` (float) rather
than `null`. These are called out explicitly per field below.

### `json.RawMessage`

Fields typed as `json.RawMessage` are pass-through JSON. Golbat stores them in
the database as JSON strings and emits them verbatim. An empty string yields
the JSON literal `null`.

### Pointer-to-string (`*string`)

Pointer-to-string fields serialize as `null` when nil, and as the string value
otherwise. Used in `fort_update` payloads.

---

## Area filtering

Each webhook is geofenced before sending. The relevant entity's location
(usually latitude/longitude, with an optional S2 cell-ID fast path) is matched
against configured geofences, producing a list of `AreaName{Parent, Name}`
entries. The receiver sees only the envelopes whose area list intersects its
configured `area_names`.

Configuration:

```toml
[[webhooks]]
url = "https://example.com/hook"
types = ["pokemon", "raid"]
area_names = ["SanFrancisco/Downtown", "Oakland/*"]
```

`area_names` supports wildcards via `geo.AreaMatchWithWildcards`:

- Empty (`area_names = []` or omitted): receive all areas.
- `"Parent/*"`: all subareas of `Parent`.
- `"Parent/Name"`: exact match.

Entities with no matching areas still produce webhook messages internally, but
receivers not configured for any of those areas will not receive them.

The area list itself is **never** included in the JSON payload.

---

## Webhook types

### pokemon

Sent when a wild / nearby / map / disk-encounter Pokémon is first seen or
changes meaningfully. Internally there are two sub-types, `PokemonIV` and
`PokemonNoIV`, but both emit the same `"type": "pokemon"` envelope with an
identical payload schema. The discriminator for downstream consumers is
whether the IV fields are populated.

**Source**: `decoder/pokemon_state.go`, `createPokemonWebhooks`.

#### Firing conditions

The webhook fires when **any** of these is true:

- The Pokémon is new (first time persisted).
- `PokemonId` changed since the last snapshot.
- `Weather` changed.
- `Cp` changed.

IV vs no-IV routing:

```go
if pokemon.AtkIv.Valid && pokemon.DefIv.Valid && pokemon.StaIv.Valid {
    // PokemonIV
} else {
    // PokemonNoIV
}
```

Configuration type strings:

- `"pokemon"` — subscribe to both IV and NoIV.
- `"pokemon_iv"` — only after all three IVs are known.
- `"pokemon_no_iv"` — only while IVs are still unknown.

#### Payload

| JSON field                | Go type           | Description |
|---------------------------|-------------------|-------------|
| `spawnpoint_id`           | string            | Spawn-point ID as **lowercase hex** (`strconv.FormatInt(id, 16)`). Literal `"None"` if the spawn ID is unknown. |
| `pokestop_id`             | string            | Pokestop fort ID if the Pokémon was seen at one. Literal `"None"` otherwise. |
| `pokestop_name`           | *string (JSON `null`able) | Pokestop name looked up at send time. `null` if `pokestop_id` is `"None"`. `"Unknown"` if the pokestop row has no name. |
| `encounter_id`            | string            | Unique encounter ID (uint64 rendered as decimal string). |
| `pokemon_id`              | int16             | Pokédex species ID. |
| `latitude`                | float64           | Geographic latitude. |
| `longitude`               | float64           | Geographic longitude. |
| `disappear_time`          | int64             | Unix seconds when the Pokémon despawns. `0` if unknown. |
| `disappear_time_verified` | bool              | `true` if the despawn time comes from a trusted source (spawnpoint history, encounter). |
| `first_seen`              | int64             | Unix seconds when Golbat first saw this encounter. |
| `last_modified_time`      | null.Int          | Unix seconds of Golbat's most recent update to the record. |
| `gender`                  | null.Int          | Gender enum: 1=male, 2=female, 3=genderless. |
| `cp`                      | null.Int          | Combat Power at trainer level 30 / weather-boosted level. |
| `form`                    | null.Int          | Form ID. |
| `costume`                 | null.Int          | Costume ID. |
| `individual_attack`       | null.Int          | Attack IV 0–15. |
| `individual_defense`      | null.Int          | Defense IV 0–15. |
| `individual_stamina`      | null.Int          | Stamina IV 0–15. |
| `pokemon_level`           | null.Int          | Pokémon level 1–50. |
| `move_1`                  | null.Int          | Fast-move ID. |
| `move_2`                  | null.Int          | Charged-move ID. |
| `weight`                  | null.Float        | Weight in kg. **Observer-specific and unreliable** — see note below. |
| `size`                    | null.Int          | Size bucket enum. Observer-specific; same caveats as `weight`. |
| `height`                  | null.Float        | Height in m. **Observer-specific and unreliable** — see note below. |
| `weather`                 | null.Int          | Weather boost enum at this spawn. |
| `capture_1`               | float64           | **Currently always `0.0`.** No code path populates the underlying `Capture1` field; the setter is unused. Emitted for legacy compatibility. |
| `capture_2`               | float64           | **Currently always `0.0`.** Same caveat as `capture_1`. |
| `capture_3`               | float64           | **Currently always `0.0`.** Same caveat as `capture_1`. |
| `shiny`                   | null.Bool         | Shiny flag. |
| `username`                | null.String       | Trainer username of the scanner. |
| `display_pokemon_id`      | null.Int          | Apparent Pokédex ID for disguised Pokémon (Ditto). |
| `display_pokemon_form`    | null.Int          | Apparent form for disguised Pokémon. |
| `is_event`                | int8              | Event flag (part of the composite primary key with `encounter_id`). |
| `seen_type`               | null.String       | How Golbat learned about this Pokémon. See [seen_type values](#seen_type-values). |
| `pvp`                     | json.RawMessage   | PvP league rankings produced by gohbem's `QueryPvPRank`. See [pvp structure](#pvp-structure). `null` when IVs are not yet known. |

#### height / weight / size caveats

`height`, `weight`, and `size` are read from the `PokemonProto` inside an
`EncounterOutProto`, `DiskEncounterOutProto`, or equivalent:

```go
pokemon.SetHeight(null.FloatFrom(float64(proto.HeightM)))
pokemon.SetWeight(null.FloatFrom(float64(proto.WeightKg)))
```

Two reasons these fields are unreliable:

1. **Observer-specific.** The client computes these values using the
   observer's trainer level and the Pokémon's IVs, so different scanners can
   report different values for the same wild Pokémon.
2. **Cleared on display change.** When a later observation shows the
   Pokémon's species, form, costume, gender, or strong-pokemon flag has
   changed from what Golbat had recorded, these three fields are reset to
   `null` (along with `move_1`, `move_2`, `cp`, `shiny`, ditto display fields,
   and `pvp`). In practice any Pokémon whose display changes loses its
   measurements. See `decoder/pokemon_decode.go` around the display-change
   block.

#### seen_type values

The `seen_type` field is an enum tracking the source of the most recent
update. Full set (defined in `decoder/pokemon_decode.go`, database enum in
`sql/45_tappables_seen_type_lure.up.sql`):

| Value                       | Meaning |
|-----------------------------|---------|
| `"nearby_cell"`             | Seen on the cell's nearby list with no fort attached. Location is the S2 cell centre — imprecise. |
| `"nearby_stop"`             | Seen on a fort's nearby list. Location is the fort's coordinates — imprecise. |
| `"wild"`                    | Seen in the wild feed. Real spawn-point location, but no IV details yet. |
| `"encounter"`               | Full `ENCOUNTER` proto decoded. IVs, moves, weight, height, size, PvP all known. |
| `"lure_wild"`               | Seen on a lure's map list (pre-encounter). No IVs yet. |
| `"lure_encounter"`          | Full `DISK_ENCOUNTER` proto decoded for a lure-spawned Pokémon. |
| `"tappable_encounter"`      | Full encounter decoded via `PROCESS_TAPPABLE` (tappable overworld object). |
| `"tappable_lure_encounter"` | Full encounter decoded via `PROCESS_TAPPABLE` at a lured pokestop. |

The value transitions as more data arrives: typically `nearby_*` → `wild` →
`encounter` for a fully-scanned Pokémon.

#### pvp structure

When IVs are known, Golbat calls `ohbem.QueryPvPRank(pokemonId, form,
costume, gender, atkIv, defIv, staIv, level)` from
`github.com/UnownHash/gohbem` and stores the JSON-encoded result. The
envelope emits it verbatim as `json.RawMessage`.

The top-level is a map keyed by league name. Keys come from Golbat's gohbem
configuration (`pvp_leagues` in `config.toml`); common keys are `"little"`,
`"great"`, `"ultra"`, and `"master"`, but any configured league name may
appear.

Each value is an array of `PokemonEntry` objects (from
`gohbem@v0.12.0/structs.go`):

| Field        | Type    | Description |
|--------------|---------|-------------|
| `pokemon`    | int     | Pokédex ID of the ranked form — may differ from the current `pokemon_id` if an evolution was projected. |
| `form`       | int     | Form ID. Omitted (`omitempty`) when `0`. |
| `evolution`  | int     | Temporary-evolution (mega/primal) ID. Omitted when `0`. |
| `cap`        | float64 | **Level** cap applied when computing this entry (e.g. `40`, `50`). The field is declared `float64` in the Go source (`gohbem@v0.12.0/structs.go:78`), but all values ever assigned to it come from `Ohbem.LevelCaps []int` or the `MaxLevel = 100` constant — so in practice it is always a whole integer, and JSON emits it as one (`50`, not `50.0`). `0` means no level cap was applied. Omitted when `0`. |
| `value`      | float64 | Comparator score used for ranking — typically `⌊Attack × Defense × Stamina⌋`. Omitted when `0`. |
| `level`      | float64 | Level (half-level allowed, e.g. `29.5`) at which the Pokémon maximises `value` for this league. |
| `cp`         | int     | CP at `level`. Omitted when `0`. |
| `percentage` | float64 | Rank value as a fraction of the best possible (0.0–1.0). |
| `rank`       | int16   | Integer rank within the league (1 = best). |
| `capped`     | bool    | `true` if the ranking row duplicates a previous row that had a lower level cap — i.e. the Pokémon cannot improve past an earlier level cap under this league's CP limit. Omitted when `false`. |

> Do **not** confuse `PokemonEntry.cap` (the **level** cap, emitted in webhook
> output) with `League.Cap` (the **CP** cap, an `int` in the gohbem
> configuration struct at `structs.go:43`). They share the name but are
> different quantities.

Example:

```json
{
  "great": [
    {
      "pokemon": 68,
      "cap": 50,
      "value": 2298123,
      "level": 29.5,
      "cp": 1498,
      "percentage": 0.9812,
      "rank": 7
    }
  ],
  "ultra": [
    {
      "pokemon": 68,
      "cap": 40,
      "level": 40,
      "cp": 2354,
      "percentage": 0.8733,
      "rank": 128,
      "capped": true
    }
  ]
}
```

---

### gym_details

Sent when a gym's team/slots/in-battle state changes.

**Source**: `decoder/gym_state.go`, `createGymWebhooks`.

#### Firing conditions

Fires when **any** of these is true:

- Gym is new.
- `AvailableSlots` changed.
- `TeamId` changed.
- `InBattle` changed.

#### Payload

| JSON field              | Go type | Description |
|-------------------------|---------|-------------|
| `id`                    | string  | Gym fort ID. |
| `name`                  | string  | Gym name, empty string if null in DB. |
| `url`                   | string  | Gym photo URL, empty string if null. |
| `latitude`              | float64 | Latitude. |
| `longitude`             | float64 | Longitude. |
| `team`                  | int64   | Team enum: 0=neutral, 1=mystic (blue), 2=valor (red), 3=instinct (yellow). |
| `guard_pokemon_id`      | int64   | Pokédex ID of the defending (top-left) Pokémon. `0` if none. |
| `slots_available`       | int64   | Number of empty defender slots. Defaults to `6` when DB value is null. |
| `ex_raid_eligible`      | int64   | `0` or `1`. |
| `in_battle`             | bool    | `true` if the gym is currently contested. Derived from `InBattle != 0`. |
| `sponsor_id`            | int64   | Always `0` — present in the struct but not populated by the current code. |
| `partner_id`            | int64   | Always `0` — not populated. |
| `power_up_points`       | int64   | Always `0` — not populated. |
| `power_up_level`        | int64   | Always `0` — not populated. |
| `power_up_end_timestamp`| int64   | Always `0` — not populated. |
| `ar_scan_eligible`      | int64   | Always `0` — not populated. |
| `defenders`             | json.RawMessage / null | JSON array of defender details. See [defenders structure](#defenders-structure). `null` when not present. |

> Note: `sponsor_id`, `partner_id`, `power_up_*`, and `ar_scan_eligible` are
> declared on the struct but never assigned in the current implementation. They
> always emit as `0` in the JSON. Receivers looking for gym power-up data
> should use the `raid` webhook, where those fields are populated.

#### defenders structure

Built by `updateGymFromGymGetStatusProto` in `decoder/gym_decode.go`. The
value is a JSON array, one entry per defending Pokémon in the order Niantic
reports them. Populated from `METHOD_GYM_GET_INFO` responses.

| Field                      | Type    | Notes |
|----------------------------|---------|-------|
| `pokemon_id`               | int     | Pokédex ID. Omitted (`omitempty`) when `0`. |
| `form`                     | int     | Form ID. Omitted when `0`. |
| `costume`                  | int     | Costume ID. Omitted when `0`. |
| `gender`                   | int     | Gender enum: 1=male, 2=female, 3=genderless. Always present. |
| `shiny`                    | bool    | Shiny flag. Omitted when `false`. |
| `temp_evolution`           | int     | Mega/primal evolution ID currently active. Omitted when `0`. |
| `temp_evolution_finish_ms` | int64   | Unix milliseconds when the temporary evolution ends. Omitted when `0`. |
| `alignment`                | int     | Alignment enum (shadow / purified / normal). Omitted when `0`. |
| `badge`                    | int     | Pokémon badge / ribbon enum. Omitted when `0`. |
| `background`               | int64 \| null | Display-background ID. Omitted when not set. |
| `deployed_ms`              | int64   | Milliseconds since the Pokémon was deployed to the gym. Omitted when `0`. |
| `deployed_time`            | int64   | Approximate Unix seconds of deployment (`now - deployed_ms`). Omitted when `0`. |
| `battles_won`              | int32   | Defence battles won. Always present. |
| `battles_lost`             | int32   | Defence battles lost. Always present. |
| `times_fed`                | int32   | Berry-feed count. Always present. |
| `motivation_now`           | float64 | Current motivation 0.0–1.0, rounded to 4 decimal places. Always present. |
| `cp_now`                   | int32   | Current CP after motivation decay. Always present. |
| `cp_when_deployed`         | int32   | CP at deployment time. Always present. |

Example:

```json
[
  {
    "pokemon_id": 143,
    "gender": 2,
    "deployed_ms": 36123456,
    "deployed_time": 1744128000,
    "battles_won": 4,
    "battles_lost": 1,
    "times_fed": 7,
    "motivation_now": 0.8234,
    "cp_now": 2890,
    "cp_when_deployed": 3511
  }
]
```

---

### raid

Sent when a raid egg or raid boss appears or changes.

**Source**: `decoder/gym_state.go`, `createGymWebhooks`.

#### Firing conditions

Two gates, both must pass:

1. `RaidSpawnTimestamp > 0` **AND** (new-record OR `RaidLevel`/`RaidPokemonId`/`RaidSpawnTimestamp`/`Rsvps` changed).
2. Raid is not yet expired:
   - egg phase: `RaidBattleTimestamp > now` AND `RaidLevel > 0`, OR
   - boss phase: `RaidEndTimestamp > now` AND `RaidPokemonId > 0`.

Both comparisons use `time.Now().Unix()` (Unix seconds).

#### Payload

| JSON field              | Go type         | Description |
|-------------------------|-----------------|-------------|
| `gym_id`                | string          | Gym fort ID. |
| `gym_name`              | string          | Gym name, `"Unknown"` if null. |
| `gym_url`               | string          | Gym photo URL, empty string if null. |
| `latitude`              | float64         | Latitude. |
| `longitude`             | float64         | Longitude. |
| `team_id`               | int64           | Team enum (same values as `gym_details.team`). |
| `spawn`                 | int64           | Unix seconds when the raid egg was first seen. |
| `start`                 | int64           | Unix seconds when the raid battle begins (egg hatches). |
| `end`                   | int64           | Unix seconds when the raid ends. |
| `level`                 | int64           | Raid tier 1–6. |
| `pokemon_id`            | int64           | Pokédex ID of the boss. `0` during the egg phase. |
| `cp`                    | int64           | Boss CP. |
| `gender`                | int64           | Boss gender enum. |
| `form`                  | int64           | Boss form ID. |
| `alignment`             | int64           | Alignment enum (shadow/purified/normal). |
| `costume`               | int64           | Boss costume ID. |
| `evolution`             | int64           | Mega/primal evolution enum. |
| `move_1`                | int64           | Boss fast-move ID. |
| `move_2`                | int64           | Boss charged-move ID. |
| `ex_raid_eligible`      | int64           | `0` or `1`. |
| `is_exclusive`          | int64           | `0` or `1` — whether this is an EX raid. |
| `sponsor_id`            | int64           | Sponsor enum. |
| `partner_id`            | string          | Partner ID, empty string if null. |
| `power_up_points`       | int64           | Gym power-up points. |
| `power_up_level`        | int64           | Gym power-up level 0–3. |
| `power_up_end_timestamp`| int64           | Unix seconds when power-up expires. |
| `ar_scan_eligible`      | int64           | `0` or `1`. |
| `rsvps`                 | json.RawMessage | RSVP counts per timeslot. See [rsvps structure](#rsvps-structure). `null` if no timeslot has any attendees. |
| `raid_seed`             | null.String     | Raid seed as a **decimal string** (converted from int64 via `strconv.FormatInt`). |

#### rsvps structure

Built by `updateGymFromRsvpProto` in `decoder/gym_decode.go` from a
`GetEventRsvpsOutProto` message. The value is a JSON array of one entry per
timeslot that has at least one `going_count` or `maybe_count` player, sorted
ascending by timeslot.

| Field         | Type  | Notes |
|---------------|-------|-------|
| `timeslot`    | int64 | Unix seconds of the start of this raid timeslot. |
| `going_count` | int32 | Players who RSVPed "going" to this timeslot. |
| `maybe_count` | int32 | Players who RSVPed "maybe" to this timeslot. |

If every timeslot has zero attendees, Golbat clears the field (writes
`null.String{}`), and the webhook emits `null`.

Example:

```json
[
  { "timeslot": 1744131600, "going_count": 3, "maybe_count": 1 },
  { "timeslot": 1744132500, "going_count": 8, "maybe_count": 2 }
]
```

---

### pokestop

Sent when a lure or community power-up starts or changes on a pokestop.

**Source**: `decoder/pokestop_state.go`, `createPokestopWebhooks`.

#### Firing conditions

Fires when either:

- Pokestop is new AND (`LureId != 0` OR `PowerUpEndTimestamp != 0`), OR
- Pokestop is not new AND:
  - `LureExpireTimestamp` changed AND `LureId != 0`, OR
  - `PowerUpEndTimestamp` changed.

Note: a lure *expiring* (transitioning `LureId` from non-zero back to 0) does
not fire this webhook because the second branch's `LureId != 0` check gates
the lure-change path. A lure expiring will, however, be reflected in the next
webhook fired for any other reason (since all fields are a snapshot at send
time).

#### Payload

| JSON field                    | Go type         | Description |
|-------------------------------|-----------------|-------------|
| `pokestop_id`                 | string          | Pokestop fort ID. |
| `latitude`                    | float64         | Latitude. |
| `longitude`                   | float64         | Longitude. |
| `name`                        | string          | Pokestop name, `"Unknown"` if null. |
| `url`                         | string          | Pokestop photo URL, empty string if null. |
| `lure_expiration`             | int64           | Unix seconds when the active lure expires. `0` if no lure. |
| `last_modified`               | int64           | Unix seconds when the server last modified the pokestop. |
| `enabled`                     | bool            | Whether the pokestop is operational. |
| `lure_id`                     | int16           | Lure enum: 501=Regular, 502=Glacial, 503=Mossy, 504=Magnetic, 505=Rainy, 506=Sparkly (values from pogo `Item_` enum). `0` if no lure. |
| `ar_scan_eligible`            | int64           | `0` or `1`. |
| `power_up_level`              | int64           | Community power-up level 0–3. |
| `power_up_points`             | int64           | Points accumulated toward the next power-up level. |
| `power_up_end_timestamp`      | int64           | Unix seconds when the current power-up expires. `0` if none. |
| `updated`                     | int64           | Unix seconds when Golbat last saved the record. |
| `showcase_focus`              | null.String     | Showcase category descriptor. |
| `showcase_pokemon_id`         | null.Int        | Pokédex ID featured in the showcase. |
| `showcase_pokemon_form_id`    | null.Int        | Form ID featured. |
| `showcase_pokemon_type_id`    | null.Int        | Pokémon type enum featured. |
| `showcase_ranking_standard`   | null.Int        | Ranking metric enum (size, weight, etc.). |
| `showcase_expiry`             | null.Int        | Unix seconds when the showcase ends. |
| `showcase_rankings`           | json.RawMessage | Top-3 contest rankings for the showcase. See [showcase_rankings structure](#showcase_rankings-structure). `null` if not set. |

#### showcase_rankings structure

Built by `updatePokestopFromGetPokemonSizeContestEntryOutProto` in
`decoder/pokestop_decode.go` from a `GetPokemonSizeLeaderboardEntryOutProto`.
The value is a JSON object, **not** an array:

| Field            | Type   | Notes |
|------------------|--------|-------|
| `total_entries`  | int    | Total number of contest entries at the pokestop, regardless of how many are listed below. |
| `last_update`    | int64  | Unix seconds at which Golbat last refreshed this showcase's rankings. |
| `contest_entries`| array  | Ordered list of **up to 3** entries (the leaderboard top 3). |

Each `contest_entries[i]` object:

| Field                      | Type    | Notes |
|----------------------------|---------|-------|
| `rank`                     | int     | `1`, `2`, or `3`. |
| `score`                    | float64 | The raw score used for ranking — interpretation depends on `showcase_ranking_standard` (size, weight, etc.). |
| `pokemon_id`               | int     | Pokédex ID of the entered Pokémon. |
| `form`                     | int     | Form ID. |
| `costume`                  | int     | Costume ID. |
| `gender`                   | int     | Gender enum. |
| `shiny`                    | bool    | Shiny flag. |
| `temp_evolution`           | int     | Active temporary evolution. |
| `temp_evolution_finish_ms` | int64   | Unix milliseconds when temp evolution ends. |
| `alignment`                | int     | Alignment enum. |
| `badge`                    | int     | Pokémon badge enum. |
| `background`               | int64 \| null | Display background (omitted when not set). |

Example:

```json
{
  "total_entries": 142,
  "last_update": 1744128000,
  "contest_entries": [
    { "rank": 1, "score": 45.12, "pokemon_id": 66, "form": 0, "costume": 0, "gender": 1, "shiny": false, "temp_evolution": 0, "temp_evolution_finish_ms": 0, "alignment": 0, "badge": 0 },
    { "rank": 2, "score": 44.98, "pokemon_id": 66, "form": 0, "costume": 0, "gender": 2, "shiny": false, "temp_evolution": 0, "temp_evolution_finish_ms": 0, "alignment": 0, "badge": 0 },
    { "rank": 3, "score": 44.71, "pokemon_id": 66, "form": 0, "costume": 0, "gender": 1, "shiny": false, "temp_evolution": 0, "temp_evolution_finish_ms": 0, "alignment": 0, "badge": 0 }
  ]
}
```

---

### quest

Sent when a quest is first observed or its type changes.

**Source**: `decoder/pokestop_state.go`, `createPokestopWebhooks` (two call
sites: AR and non-AR).

Two independent webhook messages are possible per pokestop per scan: one for
the AR quest, one for the non-AR (alternative) quest. They share the same
payload schema and are distinguished by the `with_ar` field.

#### Firing conditions

Fires separately for each quest kind:

- **AR (`with_ar: true`)**: `QuestType.Valid` AND (new-record OR `QuestType` changed).
- **Non-AR (`with_ar: false`)**: `AlternativeQuestType.Valid` AND (new-record OR `AlternativeQuestType` changed).

> Note: the webhook fires only when the **type** changes. Changes to rewards,
> conditions, title, target, or template *without* a type change do not
> produce a new webhook. This is intentional debouncing.

#### Payload

| JSON field          | Go type         | Description |
|---------------------|-----------------|-------------|
| `pokestop_id`       | string          | Pokestop fort ID. |
| `latitude`          | float64         | Latitude. |
| `longitude`         | float64         | Longitude. |
| `pokestop_name`     | string          | Pokestop name, `"Unknown"` if null. |
| `type`              | null.Int        | Quest type enum (Niantic `QuestType` proto). |
| `target`            | null.Int        | Quest completion target (e.g., "catch 3 Pokémon" → 3). |
| `template`          | null.String     | Niantic quest template ID. |
| `title`             | null.String     | Localized quest title. |
| `conditions`        | json.RawMessage | JSON array of quest conditions. See [conditions structure](#conditions-structure). `null` if empty. |
| `rewards`           | json.RawMessage | JSON array of rewards. See [rewards structure](#rewards-structure). `null` if empty. |
| `updated`           | int64           | Unix seconds when Golbat last saved the pokestop. |
| `ar_scan_eligible`  | int64           | Whether the pokestop supports AR scanning. |
| `pokestop_url`      | string          | Pokestop photo URL, empty string if null. |
| `with_ar`           | bool            | `true` for AR quest, `false` for non-AR alternative quest. |
| `quest_seed`        | null.String     | Quest seed as a **decimal string** (converted from int64). `null` if no seed. |

Both arrays are built by `updatePokestopFromQuestProto` in
`decoder/pokestop_decode.go`. Each entry is wrapped in a uniform envelope:

```json
{ "type": <int>, "info": { /* type-specific fields, possibly empty */ } }
```

`type` is the raw enum value from `QuestConditionProto.ConditionType` or
`QuestRewardProto.Type`. `info` is always present (as `{}` for types that
carry no extra data). Unknown types that haven't been added to Golbat's
switch statements emit an empty `info`.

##### conditions structure

The following condition types have populated `info` fields; the rest emit
`info: {}`.

| `type` | `QuestConditionProto` constant     | `info` fields |
|--------|------------------------------------|---------------|
| 3      | `WITH_ITEM`                        | `item_id` (int, omitted if `0`). |
| 6      | `WITH_POKEMON_TYPE`                | `pokemon_type_ids` (int array). |
| 7      | `WITH_POKEMON_CATEGORY`            | `category_name` (string, omitted if empty), `pokemon_ids` (int array). |
| 8      | `WITH_WIN_RAID_STATUS`             | (no info) |
| 9      | `WITH_RAID_LEVEL`                  | `raid_levels` (int array). |
| 11     | `WITH_THROW_TYPE`                  | `throw_type_id` (int, omitted if `0`), `hit` (bool). |
| 13     | `WITH_LOCATION`                    | `cell_ids` (int64 array of S2 cell IDs). |
| 14     | `WITH_DISTANCE`                    | `distance` (float64 km). |
| 15     | `WITH_POKEMON_ALIGNMENT`           | `alignment_ids` (int array). |
| 16     | `WITH_INVASION_CHARACTER`          | `character_category_ids` (int array). |
| 17     | `WITH_NPC_COMBAT`                  | `win` (bool), `template_ids` (string array). |
| 26     | `WITH_THROW_TYPE_IN_A_ROW`         | `throw_type_id` (int, omitted if `0`), `hit` (bool). |
| 27     | `WITH_PLAYER_LEVEL`                | `level` (int). |
| 28     | `WITH_BUDDY`                       | `min_buddy_level` (int), `must_be_on_map` (bool). |
| 30     | `WITH_DAILY_BUDDY_AFFECTION`       | `min_buddy_affection_earned_today` (int). |
| 33     | `WITH_BADGE_TYPE`                  | `amount` (int), `badge_rank` (int), `badge_types` (int array). |
| 36     | `WITH_RAID_ELAPSED_TIME`           | `time` (int64 seconds — converted from the proto's ms). |
| 39     | `WITH_ITEM_TYPE`                   | `item_type_ids` (int array). |
| 43     | `WITH_TEMP_EVO_POKEMON`            | `raid_pokemon_evolutions` (int array of mega IDs). |

Types that are recognised but carry no `info` fields:
`WITH_WIN_GYM_BATTLE_STATUS`, `WITH_SUPER_EFFECTIVE_CHARGE`,
`WITH_UNIQUE_POKESTOP`, `WITH_QUEST_CONTEXT`, `WITH_WIN_BATTLE_STATUS`,
`WITH_CURVE_BALL`, `WITH_NEW_FRIEND`, `WITH_DAYS_IN_A_ROW`,
`WITH_WEATHER_BOOST`, `WITH_DAILY_CAPTURE_BONUS`, `WITH_DAILY_SPIN_BONUS`,
`WITH_UNIQUE_POKEMON`, `WITH_BUDDY_INTERESTING_POI`, `WITH_POKEMON_LEVEL`,
`WITH_SINGLE_DAY`, `WITH_UNIQUE_POKEMON_TEAM`, `WITH_MAX_CP`,
`WITH_LUCKY_POKEMON`, `WITH_LEGENDARY_POKEMON`, `WITH_GBL_RANK`,
`WITH_CATCHES_IN_A_ROW`, `WITH_ENCOUNTER_TYPE`, `WITH_COMBAT_TYPE`,
`WITH_GEOTARGETED_POI`, `WITH_FRIEND_LEVEL`, `WITH_STICKER`,
`WITH_POKEMON_CP`, `WITH_RAID_LOCATION`, `WITH_FRIENDS_RAID`,
`WITH_POKEMON_COSTUME`. The enum value is preserved in `type` so receivers
can still match them.

Numeric values shown above are the `ConditionType` proto values at the time
of writing. Treat the constant name as authoritative and look up the
numeric value from the bundled `pogo` proto definitions if you need it.

##### rewards structure

| `type` | `QuestRewardProto` constant        | `info` fields |
|--------|------------------------------------|---------------|
| 1      | `EXPERIENCE`                       | `amount` (int). |
| 2      | `ITEM`                             | `amount` (int), `item_id` (int). |
| 3      | `STARDUST`                         | `amount` (int). |
| 4      | `CANDY`                            | `amount` (int), `pokemon_id` (int). |
| 5      | `AVATAR_CLOTHING`                  | (no info) |
| 6      | `QUEST`                            | (no info) |
| 7      | `POKEMON_ENCOUNTER`                | See below. |
| 8      | `POKECOIN`                         | `amount` (int). |
| 9      | `XL_CANDY`                         | `amount` (int), `pokemon_id` (int). |
| 10     | `LEVEL_CAP`                        | (no info) |
| 11     | `STICKER`                          | `amount` (int), `sticker_id` (string). |
| 12     | `MEGA_RESOURCE`                    | `amount` (int), `pokemon_id` (int). |
| 13     | `INCIDENT`                         | (no info) |
| 14     | `PLAYER_ATTRIBUTE`                 | (no info) |

**`POKEMON_ENCOUNTER` info**:

| Field                | Type    | Notes |
|----------------------|---------|-------|
| `pokemon_id`         | int     | Pokédex ID. Set to `132` (Ditto) if `is_hidden_ditto` is true. |
| `pokemon_id_display` | int     | Only present when `pokemon_id` is forced to `132`: the apparent species the client will show. |
| `shiny_probability`  | float64 | Omitted when `0`. |
| `costume_id`         | int     | Omitted when `0`. |
| `form_id`            | int     | Omitted when `0`. |
| `gender_id`          | int     | Omitted when `0`. |
| `shiny`              | bool    | Omitted when `false`. |
| `background`         | int64   | Display-background ID. Omitted when not set. |
| `bread_mode`         | int     | Dynamax/Gigantamax enum. Omitted when `0`. |

Example reward array:

```json
[
  { "type": 3, "info": { "amount": 500 } },
  { "type": 2, "info": { "amount": 3, "item_id": 101 } },
  { "type": 7, "info": { "pokemon_id": 133, "shiny_probability": 0.02, "gender_id": 1 } }
]
```

---

### invasion

Sent when a Team Rocket invasion (incident) is first observed or its
expiration, character, confirmation status, or lineup changes.

**Source**: `decoder/incident_state.go`, `createIncidentWebhooks`.

#### Firing conditions

Fires when **any** of these is true:

- Incident is new.
- `ExpirationTime` changed.
- `Character` changed.
- `Confirmed` changed.
- `Slot1PokemonId` changed (acts as a canary for lineup updates).

Changes to slot 2 or slot 3 alone do **not** fire the webhook.

#### Payload

| JSON field                  | Go type         | Description |
|-----------------------------|-----------------|-------------|
| `id`                        | string          | Incident ID. |
| `pokestop_id`               | string          | Pokestop fort ID hosting the invasion. |
| `latitude`                  | float64         | Pokestop latitude (looked up at send time; `0` if pokestop is missing). |
| `longitude`                 | float64         | Pokestop longitude (same caveat). |
| `pokestop_name`             | string          | Pokestop name, `"Unknown"` if null or missing. |
| `url`                       | string          | Pokestop photo URL. |
| `enabled`                   | bool            | Whether the parent pokestop is operational. |
| `start`                     | int64           | Unix seconds when the invasion started. |
| `incident_expire_timestamp` | int64           | Unix seconds when the invasion expires. **Duplicated in `expiration`** for legacy compatibility. |
| `expiration`                | int64           | Same value as `incident_expire_timestamp`. |
| `display_type`              | int16           | Display enum (Niantic `IncidentDisplayType`). |
| `style`                     | int16           | Style enum. |
| `grunt_type`                | int16           | Character enum (Niantic `InvasionCharacter`). **Duplicated in `character`** for legacy compatibility. |
| `character`                 | int16           | Same value as `grunt_type`. |
| `updated`                   | int64           | Unix seconds when Golbat last saved the record. |
| `confirmed`                 | bool            | Whether the lineup is verified. |
| `lineup`                    | array           | Array of up to 3 `{slot, pokemon_id, form}` entries. Empty array `[]` if slot 1 is unknown. |

Each `lineup` entry:

| Field        | Type     | Description |
|--------------|----------|-------------|
| `slot`       | uint8    | `1`, `2`, or `3`. |
| `pokemon_id` | null.Int | Pokédex ID of the Pokémon in this slot. `null` if unknown. |
| `form`       | null.Int | Form ID. `null` if unknown. |

> Note: when slot 1 is known, all three slots are emitted (with `null` fields
> for unknown slots). When slot 1 is unknown, `lineup` is `[]` and the other
> slots are omitted.

---

### weather

Sent when a weather S2 cell's gameplay condition or warn flag changes.

**Source**: `decoder/weather.go`, `createWeatherWebhooks`.

#### Firing conditions

Fires when **any** of these is true:

- Cell is new.
- `GameplayCondition` changed.
- `WarnWeather` changed.

Changes to intensity fields (`cloud_level`, `rain_level`, etc.) alone do **not**
fire the webhook.

#### Payload

| JSON field              | Go type            | Description |
|-------------------------|--------------------|-------------|
| `s2_cell_id`            | int64              | Google S2 cell ID (level-10 cells, ~1 km² each). |
| `latitude`              | float64            | Center latitude of the cell. |
| `longitude`             | float64            | Center longitude of the cell. |
| `polygon`               | array of 4 `[lat, lon]` pairs | The four corners of the S2 cell, computed from `s2.CellFromCellID(id).Vertex(i)` in order 0–3. Each element is a two-element `[lat, lon]` array in degrees. |
| `gameplay_condition`    | int64              | Niantic `GameplayWeatherProto_WeatherCondition` enum: 0=none, 1=clear, 2=rainy, 3=partly cloudy, 4=overcast, 5=windy, 6=snow, 7=fog. |
| `wind_direction`        | int64              | Compass direction 0–359°. |
| `cloud_level`           | int64              | 0–3 scale. |
| `rain_level`            | int64              | 0–3 scale. |
| `wind_level`            | int64              | 0–3 scale. |
| `snow_level`            | int64              | 0–3 scale. |
| `fog_level`             | int64              | 0–3 scale. |
| `special_effect_level`  | int64              | 0–3 scale. |
| `severity`              | int64              | Alert severity enum (for hazardous weather). |
| `warn_weather`          | bool               | `true` if Niantic is displaying a weather warning. |
| `updated`               | int64              | Unix seconds of Golbat's most recent update (derived from `UpdatedMs / 1000`). |

---

### fort_update

Sent when a fort (pokestop, gym, or station) is created, has its metadata
edited, or is removed.

**Source**: `decoder/fort.go`, `CreateFortWebHooks`. Called by
`createPokestopFortWebhooks`, `createGymFortWebhooks`, and the fort-tracker
stale-detection path.

The payload has three variants distinguished by `change_type`:

- `"new"` — only `new` is populated.
- `"edit"` — both `old` and `new` are populated, and `edit_types` lists the
  changed fields.
- `"removal"` — only `old` is populated.

#### Firing conditions

- **new**: a pokestop, gym, or station is seen for the first time.
- **edit**: any of name, description, image URL path, or location differ
  between the old snapshot and the current state. At least one of the
  following changes must produce a non-empty `edit_types`, otherwise no
  webhook fires:
  - `"name"` — name went from nil to a non-empty string, or changed value.
  - `"description"` — same rules as name.
  - `"image_url"` — the URL's path component changed. Domain-only changes are
    **not** reported. A transition from nil/empty to non-empty URL is reported.
  - `"location"` — latitude or longitude changed beyond `floatTolerance`.
- **removal**: the fort-tracker detected the fort has been absent from its S2
  cell for longer than the stale threshold.

Pokestop↔gym conversions are reported as a `removal` of the old type followed
by a `new` of the new type, not as an `edit`.

#### Payload

| JSON field     | Go type        | Description |
|----------------|----------------|-------------|
| `change_type`  | string         | `"new"`, `"edit"`, or `"removal"`. |
| `edit_types`   | []string       | Only present on `"edit"`. Any subset of `["name", "description", "image_url", "location"]`. Omitted if empty. |
| `old`          | FortWebhook    | Previous state. Present on `"edit"` and `"removal"`, omitted on `"new"`. |
| `new`          | FortWebhook    | Current state. Present on `"new"` and `"edit"`, omitted on `"removal"`. |

The nested `FortWebhook` object:

| JSON field    | Go type   | Description |
|---------------|-----------|-------------|
| `id`          | string    | Fort ID. |
| `type`        | string    | `"pokestop"`, `"gym"`, or `"station"`. |
| `name`        | *string   | Fort name. `null` if not set. |
| `description` | *string   | Fort description. `null` if not set. |
| `image_url`   | *string   | Fort photo URL. `null` if not set. |
| `location`    | object    | `{"lat": float64, "lon": float64}`. |

The internal `CellId` field is **not** serialized (Go tag `json:"-"`).

##### Example — new

```json
{
  "type": "fort_update",
  "message": {
    "change_type": "new",
    "new": {
      "id": "...",
      "type": "pokestop",
      "name": "Bronze Statue",
      "description": null,
      "image_url": "https://lh3.googleusercontent.com/...",
      "location": { "lat": 37.78, "lon": -122.41 }
    }
  }
}
```

##### Example — edit

```json
{
  "type": "fort_update",
  "message": {
    "change_type": "edit",
    "edit_types": ["image_url"],
    "old": { "id": "...", "type": "gym", "name": "...", ... },
    "new": { "id": "...", "type": "gym", "name": "...", ... }
  }
}
```

##### Example — removal

```json
{
  "type": "fort_update",
  "message": {
    "change_type": "removal",
    "old": { "id": "...", "type": "station", ... }
  }
}
```

---

### max_battle

Sent when a station's Max Battle (Dynamax battle) state changes.

**Source**: `decoder/station_state.go`, `createStationWebhooks`.

#### Firing conditions

Fires when **any** of these is true:

- Station is new, OR
- `BattlePokemonId.Valid == true` AND at least one of the following changed:
  - `EndTime`
  - `BattleEnd`
  - `BattlePokemonId`
  - `BattlePokemonForm`
  - `BattlePokemonCostume`
  - `BattlePokemonGender`
  - `BattlePokemonBreadMode`

> Note: the firing condition requires `BattlePokemonId.Valid` for the
> non-new-record path. A station that transitions from having a battle to
> having none will **not** emit a webhook for that transition.

#### Payload

| JSON field                 | Go type  | Description |
|----------------------------|----------|-------------|
| `id`                       | string   | Station ID. |
| `latitude`                 | float64  | Latitude. |
| `longitude`                | float64  | Longitude. |
| `name`                     | string   | Station name. |
| `start_time`               | int64    | Unix seconds when the station opened. |
| `end_time`                 | int64    | Unix seconds when the station closes. |
| `is_battle_available`      | bool     | Whether a Max Battle is currently available. |
| `battle_level`             | null.Int | Max Battle level 1–6. |
| `battle_start`             | null.Int | Unix seconds when the battle window opens. |
| `battle_end`               | null.Int | Unix seconds when the battle window closes. |
| `battle_pokemon_id`        | null.Int | Pokédex ID of the boss. |
| `battle_pokemon_form`      | null.Int | Boss form ID. |
| `battle_pokemon_costume`   | null.Int | Boss costume ID. |
| `battle_pokemon_gender`    | null.Int | Boss gender enum. |
| `battle_pokemon_alignment` | null.Int | Boss alignment enum. |
| `battle_pokemon_bread_mode`| null.Int | Dynamax/Gigantamax mode enum (`BreadModeEnum` in proto). |
| `battle_pokemon_move_1`    | null.Int | Boss fast-move ID. |
| `battle_pokemon_move_2`    | null.Int | Boss charged-move ID. |
| `total_stationed_pokemon`  | null.Int | Total Pokémon stationed at the location. |
| `total_stationed_gmax`     | null.Int | Total Gigantamax Pokémon stationed. |
| `updated`                  | int64    | Unix seconds when Golbat last saved the record. |

---

## Configuration

Webhooks are configured in `config.toml`:

```toml
webhook_interval = "1s"   # how often to flush; default 1s

[[webhooks]]
url = "https://example.com/hook"
types = ["pokemon", "raid", "fort_update"]
area_names = ["SanFrancisco/*"]

[webhooks.header_map]
Authorization = "Bearer secret"
X-Custom-Header = "value"
```

| Setting            | Required | Meaning |
|--------------------|----------|---------|
| `url`              | yes      | HTTP(S) endpoint. Must include scheme. |
| `types`            | no       | Array of type strings (see below). Omit or empty to receive everything. |
| `area_names`       | no       | Area filter; omit or empty to receive all areas. |
| `header_map`       | no       | Extra HTTP headers to set on every POST. |

### Accepted type strings

| Config string     | Emits envelope type(s) | Covers |
|-------------------|------------------------|--------|
| `gym`             | `gym_details`          | Gym team/slots/battle changes. |
| `raid`            | `raid`                 | Raid spawns and changes. |
| `quest`           | `quest`                | Quests (both AR and non-AR). |
| `pokestop`        | `pokestop`             | Lure and power-up changes. |
| `invasion`        | `invasion`             | Team Rocket invasions. |
| `weather`         | `weather`              | Weather cell changes. |
| `fort_update`     | `fort_update`          | Fort create/edit/remove. |
| `pokemon_iv`      | `pokemon`              | Only Pokémon with all three IVs known. |
| `pokemon_no_iv`   | `pokemon`              | Only Pokémon without full IVs. |
| `pokemon`         | `pokemon`              | Both IV and no-IV variants. |
| `max_battle`      | `max_battle`           | Station Max Battle state. |

Unknown type strings cause Golbat to fail to start with a config error.
