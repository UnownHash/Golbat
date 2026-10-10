# Personal shiny filtering

Configure `[shiny_rates]` with the `url` of a shiny-rate source and an optional
bearer `token`. Golbat fetches `GET <url>/v1/rates?compact=1` and caches it for
five minutes. Unknown variants assume 1/512; Ditto is excluded. A failed refresh
retains the last rates and retries after 30 seconds.

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
      }
    }
  ]
}
```

- `prediction.probability` is the shiny probability for that species/form/costume.
- `supported: false` means the variant is never shiny-eligible.
- `overrides` is optional and keyed by IANA timezone of the spawn's location.

On the existing HTTP `POST /api/pokemon/v3/scan`, add `shiny_for` to a filter
clause:

```json
{"min":{"latitude":33.15,"longitude":-112.8},"max":{"latitude":34.0,"longitude":-111.4},"filters":[{"shiny_for":["player-id"]}]}
```

The clause matches Pokémon shiny for **any** supplied player ID, AND its other
conditions. Clauses are ORed. IDs are opaque, nonempty strings. Result limits
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

`/api/status` advertises `filters.shiny_for` when configured. This extension is
HTTP-only; ordinary HTTP/gRPC scans are unchanged. No database changes are
required. Calculations use bounded memory (up to 96 MiB shared across players);
concurrent shiny scans queue behind four active slots and respect cancellation.
