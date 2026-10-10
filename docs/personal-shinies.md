# Personal shiny filtering

Configure `[shinx]` with `url` and `token` (the Shinx API token). Golbat caches
`/v1/rates?compact=1` for five minutes, including form, costume and timezone
overrides. Unknown variants assume 1/512; Ditto is excluded. A failed refresh
retains the last rates and retries after 30 seconds.

On the existing HTTP `POST /api/pokemon/v3/scan`, add `shiny_for` to a filter
clause:

```json
{"min":{"latitude":33.15,"longitude":-112.8},"max":{"latitude":34.0,"longitude":-111.4},"filters":[{"shiny_for":["player-id"]}]}
```

The clause matches Pokémon shiny for **any** supplied player ID, AND its other
conditions. Clauses are ORed. IDs are opaque, nonempty strings. Result limits
apply after shiny filtering. The API secret is still required as configured;
frontends must authorize account ownership and map access themselves.

`/api/status` advertises `filters.shiny_for` when configured. This extension is
HTTP-only; ordinary HTTP/gRPC scans are unchanged. No database changes are
required. Calculations use bounded memory (up to 96 MiB shared across players);
concurrent shiny scans queue behind four active slots and respect cancellation.
