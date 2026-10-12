# Personal shiny and background filtering

Configure `[shiny_rates]` with the `url` of a shiny-rate source and an optional
bearer `token`. A background task fetches `GET <url>/v1/rates?compact=1` at
startup and every five minutes. A failed refresh keeps the last rates and
retries after 30 seconds; until the first successful load, personal scans fail
rather than guess. Unknown variants assume 1/512 shiny and no background, unless their species is listed in `unsupportedPokemon`.
Ditto, disguised Pokémon and nearby-only sightings are never eligible.

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
  "unsupportedPokemon": [132, 931]
}
```

- `prediction.probability` is the shiny probability for that species/form/costume.
- `supported: false` means the variant is never shiny-eligible.
- `unsupportedPokemon` is optional: species with no released shiny. Their variants are never shiny-eligible even without an entry in `rates`.
- `overrides` is optional and keyed by IANA timezone of the spawn's location.
  Local-time events such as Community Day change rates per timezone.
- `background` and `background_overrides` are optional and work the same way
  for backgrounds. A variant without them never has a background.

Rolls come from a per-player, per-encounter `java.util.Random` (seeded from
SHA-1 of the little-endian encounter ID followed by the player ID). The game
takes a draw only for rolls the variant actually has, in this order:

1. the shiny roll, if the variant can be shiny at that location
   (`supported`, or an unknown variant);
2. the background roll, if a background is on offer (nonzero rate);
3. two more draws if a background was rolled (then the height draws).

So the background is the second draw for shiny-capable variants and the
first otherwise. The test vectors pin current behaviour, so a change in the
game would break predictions without an error.

Limitations:

- Rates are keyed by variant and, optionally, IANA timezone. Backgrounds tied
  to a city, Go Fest venue or landmark would need geofence- or S2-keyed rates,
  which this format cannot express.
- Rates are applied when the scan runs, not when the Pokémon spawned or was
  encountered, and the cached rates can be up to five minutes old. A Pokémon
  that spawned before a rate change is judged at the new rate. Which time the
  game itself uses has not been verified.

On the existing HTTP `POST /api/pokemon/v3/scan`, add `shiny_for` to a filter
clause:

```json
{"min":{"latitude":33.15,"longitude":-112.8},"max":{"latitude":34.0,"longitude":-111.4},"filters":[{"shiny_for":["player-id"]}]}
```

The clause matches Pokémon shiny for **any** supplied player ID, AND its other
conditions. Clauses in the group that applies are ORed. IDs are opaque, nonempty strings; a request may use at most 50 distinct IDs
across all fields, each at most 256 bytes (otherwise 400). Result limits
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
`shiny_for` and the IDs it has a background for in `background_for`. Both
fields are omitted when empty:

```json
{"min":{...},"max":{...},"filters":[{}],"annotate_for":["player-id"]}
```

`/api/status` advertises `filters.shiny_for`, `filters.background_for` and
`filters.annotate_for` when configured. This extension is
HTTP-only; ordinary HTTP/gRPC scans are unchanged. No database changes are
required. Rolls are recomputed rather than cached (one SHA-1 block each), and
candidates that can never qualify are skipped before hashing. Personal scans
run like ordinary scans, without a separate concurrency limit.
