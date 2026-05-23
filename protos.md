# Proto Support

This document lists every Pokémon GO client proto method Golbat recognises,
what it does with each one, and which methods are explicitly ignored.

The authoritative source is `decode.go` in the repository root. If this
document and the code disagree, the code wins — please update this document.

## Processing rules

- **Minimum level**: the account that produced the payload must be at least
  trainer level 30 for any method to be processed. The sole exception is the
  internal `INTERNAL_PROXY_SOCIAL_ACTION` path, which runs regardless of level.
  Lower-level submissions are counted as `error/low_level` and dropped.
- **Scan parameters**: several methods are additionally gated by per-area scan
  rules (`ProcessPokemon`, `ProcessPokestops`, `ProcessGyms`, `ProcessStations`,
  `ProcessTappables`, `ProcessWeather`, `ProcessCells`) configured in
  `config.toml`. A method may be decoded but produce no side effects if the
  relevant flag is disabled for the scan's location or `scan_context`.
- **Raw request required**: some methods need the original client request in
  addition to the server response. These are marked below; if the raw does not
  include the request payload, Golbat logs and skips the message.

## Processed methods

### `Method_METHOD_GET_MAP_OBJECTS`

The primary data feed. Golbat extracts and processes all of the following
from a single `GetMapObjectsOutProto`:

- **Forts** (pokestops and gyms) with their public attributes, including
  `active_pokemon` (map pokémon spawned at lures). Gated by `ProcessPokestops`
  / `ProcessGyms`.
- **Wild pokémon** — basic location and despawn data, plus spawnpoint and
  TTH updates. Gated by `ProcessPokemon` + `ProcessWild`.
- **Nearby pokémon** — minimal data; written only if the pokémon is attached
  to a pokestop, or if `ProcessNearbyCell` is enabled for cell-only nearby.
  Gated by `ProcessPokemon` + `ProcessNearby`.
- **Map pokémon** (pokémon tied to forts, e.g. lure spawns). Gated by
  `ProcessPokemon`.
- **Stations** — Max Battle station metadata and battle windows. Gated by
  `ProcessStations`.
- **Client weather** — one entry per weather S2 cell. Gated by
  `ProcessWeather`. If `ProactiveIVSwitching` is also enabled, a weather
  change triggers a re-evaluation of encountered pokémon whose weather boost
  may have flipped.
- **S2 cells** — non-empty map cells are recorded for timestamp tracking.
  Gated by `ProcessCells`.
- **Fort removal detection** — every observed cell is also passed to the
  fort-tracker to detect forts that disappeared from the game.

### `Method_METHOD_GET_MAP_FORTS`

Bulk image URLs and names for forts. Updates pokestops or gyms in place,
looking up unknown IDs against both caches.

### `Method_METHOD_FORT_DETAILS`

Per-fort detail request. Dispatches on `FortType`:

- `CHECKPOINT` → updates pokestop name/description/image/lure details.
- `GYM` → updates gym name/description/image.

### `Method_METHOD_GYM_GET_INFO`

Full gym details (name, team, current defenders, raid info).

### `Method_METHOD_ENCOUNTER`

Full wild pokémon encounter — IV, CP, level, moves, capture rates, weight,
size, height. Gated by `ProcessPokemon`.

### `Method_METHOD_DISK_ENCOUNTER`

Full lure pokémon encounter — same payload shape as `METHOD_ENCOUNTER` but
for pokémon spawned from a lured pokestop.

### `Method_METHOD_FORT_SEARCH`

Quest type, target, template, conditions, and rewards for a pokestop. The
raw MUST include the `have_ar` parameter — Golbat cannot distinguish AR from
non-AR quests without it and will drop the payload. Non-`SUCCESS` results
are ignored.

### `Method_METHOD_INVASION_OPEN_COMBAT_SESSION`

Team Rocket invasion lineup. **Requires the proto request** to identify the
incident being opened. Writes the lineup into the incident's slot data.

### `Method_METHOD_START_INCIDENT`

Confirmation of a real or decoy Giovanni / Team Rocket Leader incident.
Non-`SUCCESS` results are ignored.

### `Method_METHOD_GET_ROUTES`

Route definitions (walking paths). Only routes with
`RouteSubmissionStatus == PUBLISHED` are accepted; others are logged and
skipped. Non-`SUCCESS` results are ignored.

### `Method_METHOD_GET_CONTEST_DATA`

Contest metadata for a pokestop — featured pokémon, ranking method, end
time. The proto request is helpful (it supplies the pokestop ID) but not
strictly required — Golbat can derive what it needs from the response alone.
Gated by `ProcessPokestops`.

### `Method_METHOD_GET_POKEMON_SIZE_CONTEST_ENTRY`

Top-3 player scores for a pokémon-size contest. **Requires the proto
request** — the pokestop ID and contest key live in the request, not the
response. Gated by `ProcessPokestops`. Non-`SUCCESS` results are ignored.

### `Method_METHOD_GET_STATION_DETAILS` *(= `METHOD_GET_STATIONED_POKEMON_DETAILS`)*

Stationed pokémon at a Max Battle station. **Requires the proto request**
to correlate the response with the station being queried. Gated by
`ProcessStations`.

- `SUCCESS` → updates `total_stationed_pokemon`, `total_stationed_gmax`, and
  the per-slot pokémon JSON blob on the station record.
- `STATION_NOT_FOUND` → clears the stationed-pokémon columns on the known
  station.
- Other statuses are ignored.

### `Method_METHOD_PROCESS_TAPPABLE` *(= 1408)*

Pokémon encounters and item rewards from tappable overworld objects.
**Requires the proto request** (contains the tappable's location and ID).
Gated by `ProcessTappables`. If the response contains an encounter, the
pokémon is also persisted via the standard encounter pipeline. Non-`SUCCESS`
results are ignored.

### `Method_METHOD_GET_EVENT_RSVPS` *(= 3031)*

Raid RSVP list — count of players who have indicated they will be present,
by response time bucket. Gated by `ProcessGyms`. The request's
`event_details` discriminator determines how the response is interpreted:

- `Raid` → attached to the corresponding gym's raid.
- `GmaxBattle` → currently unsupported; logged and skipped.

Non-`SUCCESS` results are ignored.

### `Method_METHOD_GET_EVENT_RSVP_COUNT` *(= 3036)*

Aggregate RSVP counts per location. Gated by `ProcessGyms`. Any location
whose `maybe_count` and `going_count` are both zero has its RSVP data
cleared on the gym. Non-`SUCCESS` results are ignored.

> Note: this method was previously ignored but is now processed. The earlier
> behaviour is obsolete.

## Social actions

The master `ClientAction_CLIENT_ACTION_PROXY_SOCIAL_ACTION` proto is decoded
from the internal proxy action channel. **Requires the proto request** — the
specific social-action discriminator lives in the request's `action` field.
Only `COMPLETED` and `COMPLETED_AND_REASSIGNED` responses are processed.

- `SocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS` → each friend's public
  summary updates the corresponding player record.
- `SocialAction_SOCIAL_ACTION_SEARCH_PLAYER` → the searched player's summary
  updates the corresponding player record; requires a non-empty friend code
  in the request.

Other social actions are counted as `ok/unknown` and left unprocessed.

## Explicitly ignored methods

These methods are seen by Golbat but deliberately not processed. They do
not emit warnings:

- `Method_METHOD_GET_PLAYER`
- `Method_METHOD_GET_HOLOHOLO_INVENTORY`
- `Method_METHOD_CREATE_COMBAT_CHALLENGE`

## Unknown methods

Any other method is logged at debug level as `Did not know hook type <name>`
and counted as `unprocessed`.
