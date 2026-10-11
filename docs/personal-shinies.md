# Personal shiny, background and size prediction

Configure `[shiny_rates]` with the `url` of a shiny-rate source, an optional
bearer `token` and an optional `max_bytes`. A background task fetches
`GET <url>/v1/rates?compact=1` at startup and every five minutes. A failed
refresh keeps the last rates and retries after 30 seconds; until the first
successful load, personal scans fail rather than guess. A response larger than
`max_bytes` (default 16 MiB) fails the refresh with an error naming the limit
instead of being parsed truncated; raise it if the feed outgrows the default.
Unknown variants take the feed's `fallback` shiny rate (1/512 if it publishes
none) and no background, unless their species is listed in
`unsupportedPokemon`.
Disguised Pokémon not known to be Ditto and nearby-only sightings are never
eligible; known Dittos follow their own rule (see [Ditto](#ditto)).

The rate source must return JSON in this shape:

```json
{
  "rates": [
    {
      "pokemon": 1,
      "form": 0,
      "costume": 0,
      "prediction": {"probability": 0.001953125, "supported": true},
      "overrides": {
        "Europe/Berlin": {"probability": 0.0625, "supported": true}
      },
      "background": {"probability": 0.04, "supported": true},
      "background_overrides": {
        "Europe/Berlin": {"probability": 0.1, "supported": true}
      }
    }
  ],
  "unsupportedPokemon": [132, 931],
  "fallback": {"probability": 0.001953125, "supported": true}
}
```

- `prediction.probability` is the shiny probability for that species/form/costume.
- `supported: false` means the variant is never shiny-eligible. Probability 0
  means the same, even with `supported: true`: no shiny is available, so the
  variant takes no shiny draw (Golbat treats any zero cutoff as not capable).
- `unsupportedPokemon` is optional: species with no released shiny. Their variants are never shiny-eligible even without an entry in `rates`.
- `fallback` is optional: the shiny rate for variants not listed in `rates`
  (and for listed ones with an invalid probability). Without it Golbat uses
  1/512. A fallback of probability 0 or `supported: false` makes unlisted
  variants never shiny-eligible.
- `overrides` is optional and keyed by IANA timezone of the spawn's location.
  Local-time events such as Community Day change rates per timezone. An
  override changes only the probability used at that location, never whether
  the variant can be shiny: that comes from the base `prediction` alone, so an
  override cannot make a never-shiny variant shiny, and an override with
  `supported: false` means no chance there but still a shiny draw.
- `background` and `background_overrides` are optional and work the same way
  for backgrounds. A variant without them never has a background.

Rolls come from a per-player, per-encounter `java.util.Random` (seeded from
SHA-1 of the little-endian encounter ID followed by the player ID). The game
takes a draw only for rolls the variant actually has, in this order:

1. the shiny roll, if the variant can be shiny (base `prediction` supported
   with a nonzero probability, or an unknown variant), regardless of location;
2. the background roll, if a background is on offer (nonzero rate);
3. two more draws if a background was rolled;
4. the height draw, plus one more for XXS and XXL.

So the background is the second draw for shiny-capable variants and the
first otherwise. The test vectors pin current behaviour, so a change in the
game would break predictions without an error.

The background draw position is validated only for shiny-capable variants.
No encounter of a never-shiny variant with a background has been captured, so
"background is the first draw when there is no shiny draw" is inferred from
the shiny-draw skip seen in height-matched encounters, not observed. Such a
combination may never occur in the wild.

Limitations:

- Rates are keyed by variant and, optionally, IANA timezone. Backgrounds tied
  to a city, Go Fest venue or landmark would need geofence- or S2-keyed rates,
  which this format cannot express.
- Rates are applied when the scan runs, not when the Pokémon spawned or was
  encountered, and the cached rates can be up to five minutes old. A Pokémon
  that spawned before a rate change is judged at the new rate. Which time the
  game itself uses has not been verified.

On the existing HTTP `POST /api/pokemon/v3/scan`, or the gRPC
`GolbatApi.ScanPokemon`, add `shiny_for` to a filter clause:

```json
{"min":{"latitude":33.15,"longitude":-112.8},"max":{"latitude":34.0,"longitude":-111.4},"filters":[{"shiny_for":["player-id"]}]}
```

The clause matches Pokémon shiny for **any** supplied player ID, AND its other
conditions. Clauses in the group that applies are ORed. IDs are opaque, nonempty strings; a request may use at most 50 distinct IDs
across all fields, each at most 256 bytes (otherwise 400, or
`INVALID_ARGUMENT` over gRPC). Result limits
apply after shiny filtering. The API secret is still required as configured;
frontends must authorize account ownership and map access themselves.

Combine it with other conditions in the same clause for AND, or use separate
clauses for OR. For example, shundos only:

```json
"filters": [{"shiny_for": ["player-id"], "iv": {"min": 100, "max": 100}}]
```

Shinies or hundos:

```json
"filters": [{"shiny_for": ["player-id"]}, {"iv": {"min": 100, "max": 100}}]
```

`background_for` works the same way for backgrounds. In one clause,
`shiny_for` and `background_for` are ANDed like any other condition.

`shiny_for` and `background_for` are ordinary clause conditions. Clause
selection follows the v3 filter semantics in `api.md` (#420): a Pokémon is
checked only against the most specific group of clauses that exists for it
(exact id+form, else id, else the clauses with no `pokemon` list), and Golbat
never merges groups. A client that wants "my shinies everywhere" alongside
per-species rules lists `{"shiny_for": [...]}` under those species keys as
well, like any other shared clause. The same rules hide a species: a
never-matching clause under its key keeps it out of a generic `shiny_for`
clause.

To mark results without filtering them, pass player IDs at the top level as
`annotate_for`. Each returned Pokémon then lists the IDs it is shiny for in
`shiny_for`, the IDs it has a background for in `background_for`, and each
player's height (m) and weight (kg) in `size_for`. All three are omitted when
empty:

```json
{"min":{...},"max":{...},"filters":[{}],"annotate_for":["player-id"]}
```

```json
"size_for": {"player-id": {"height": 0.71, "weight": 6.52}}
```

Sizes need the masterfile's per-species/form `height`, `weight` and
`size_settings` (the game master's size-class bounds), and IVs that match the
encounter derivation, which also supplies the size class and weight noise.
Without them `size_for` is left out. Pumpkaboo and Gourgeist have no size
model (their form is the size) and never get one.

Each player's height is `lo + (hi − lo)·u` for the height draw `u` inside the
class's bounds. XXS and XXL read one more draw: below 0.05 it picks the far
20% of the band (away from M), otherwise the near 80%. Weight is base weight ×
ratio, where ratio = (height / base height)² + 0.125 × noise, with the noise
clamped to ±4, the height ratio not squared for XXL, and the noise term
dropped if it would make the ratio nonpositive. The model and the test
vectors come from roundaboutluke's captures (#423).

gRPC uses the same field names with the same semantics:
`PokemonDnfFilter.shiny_for` and `background_for`,
`PokemonScanRequest.annotate_for`, and `Pokemon.shiny_for` and
`background_for` on results (empty when not annotated). `size_for` is
HTTP-only for now. Where HTTP answers
503, gRPC answers `FAILED_PRECONDITION` if `[shiny_rates]` is not configured
and `UNAVAILABLE` while the rates have not loaded yet (retry).

```bash
grpcurl -plaintext -H 'x-golbat-secret: your_api_secret' \
  -d '{"min":{"lat":33.15,"lon":-112.8},"max":{"lat":34.0,"lon":-111.4},
       "filters":[{"shiny_for":["player-id"]}],"annotate_for":["player-id"]}' \
  localhost:50001 golbat_api.GolbatApi/ScanPokemon
```

`/api/status` advertises `filters.shiny_for`, `filters.background_for` and
`filters.annotate_for` when configured, for both APIs. Scans without these
fields are unchanged. No database changes are
required. Rolls are recomputed rather than cached (one SHA-1 block each), and
candidates that can never qualify are skipped before hashing. Rates are
converted to integer cutoffs once per refresh, and overrides equal to their
base rate are dropped, so a spawn's timezone is looked up only for variants
whose rate actually differs by timezone (never, if none do). Personal scans
run like ordinary scans, without a separate concurrency limit.

## Ditto

A record Golbat has identified as Ditto (`is_ditto` with `pokemon_id` 132,
from encounter evidence or the encounter-ID Ditto roll) is shiny for a player
when the **third** draw of the same generator is below 1/64. The game takes
all three draws for a Ditto whatever its disguise's rates: the first two are
the disguise's shiny and background rolls, and the disguise itself is never
shiny. The 1/64 rate is fixed in Golbat; the feed's rates, `fallback` and
`unsupportedPokemon` (which usually lists 132) do not apply to a known Ditto.
The rule was verified against 124 caught Dittos (30 shiny), including
different players' outcomes on shared spawns, and none of 285 disguises was
shiny.

Ditto limitations:

- A Ditto is covered only once Golbat knows it is one. Until then it looks
  like its disguise and is judged as that species, and could be reported
  shiny by the disguise's rate even though it will not be. A roll-marked
  record that a later encounter refutes reverts to the disguise's rule.
- `background_for` never matches a Ditto and `annotate_for` never lists one
  in `background_for`: no Ditto background has been observed.
- `annotate_for` gives a Ditto no `size_for`: its height draws come after
  Ditto's own draws, which are not modelled.
- A Pokémon with `pokemon_id` 132 that is not marked `is_ditto` (an
  undisguised Ditto) is not eligible; the rule is verified only for disguised
  wild Dittos.
