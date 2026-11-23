# Biome geography and wild spawn rates

Enable `[biomes].enabled` to record encounter geography and publish observed
wild spawn rates in MariaDB. Collection is off by default. Run one collector
per database. Golbat applies the schema migration at startup even when
collection is disabled.

Successful wild encounters provide the geography of their level-16 S2 cell.
Golbat inserts a cell on discovery and writes it again when its reported
geography changes. Identical encounters advance an in-memory confirmation
timestamp. The stored `observed_at_ms` is the encounter time of the last
saved profile. Existing rows keep their previous timestamp until a change.
A bounded cache avoids reading or encoding frequently repeated profiles.
After startup or cache turnover, existing profiles are read in batches.
Corrections older than the discarded confirmation history are ignored until
confirmed by a newer encounter. Previously unknown cells can still be discovered
from older observations.

For spawn estimates, the scan rule must process wild Pokémon and weather.
A sighting counts once, after the cell's geography is known and a weather
report is available for its level-10 parent. The weather report must be from
the same UTC hour and at most 15 minutes old.

Samples are grouped by the IANA time zone of the cell centre, current
gameplay weather, and habitat category. A category combines temperature with
the first matching vista (beach, ocean, river, lake, desert, city), falling
back to land cover when none applies. For example, cool lake cells in forest
and cropland share rates. Their individual geography remains available for
display. Where the time zone cannot be resolved, the sample stays local to
the individual cell. On upgrade from local or geofence grouping, Golbat
reclassifies stored cells in batches. Existing sightings keep their original
scope until they expire; new estimates fill as fresh sightings arrive.

## Reading the tables

| Table | Contents |
| --- | --- |
| `biome_cell` | Level-16 S2 cell ID, geography as JSON (game enum values), category key and time-zone scope |
| `biome_spawn_group` | Sample size, distinct cell count, window bounds and expiry per scope, geography and weather |
| `biome_spawn_rate` | Observed percentage per group, species and form. Form `-1` means the display data was missing. Form `0` is a separate value. |
| `biome_spawn_observation` | Internal deduplication history, keyed by a hash of the encounter ID and retained for three hours |

Fetch visible cells by primary key. Pass unsigned 64-bit S2 IDs as decimal
strings when using JavaScript. For a popup, bind the cell ID, its current
gameplay weather condition and the current Unix time in milliseconds:

```sql
SELECT r.pokemon_id, r.form, r.percent, g.sample_count, g.cell_count
FROM biome_cell AS c
JOIN biome_spawn_group AS g USING (spawn_scope, biome_key)
JOIN biome_spawn_rate AS r USING (spawn_scope, biome_key, weather)
WHERE c.cell_id = CAST(? AS UNSIGNED)
  AND g.weather = ?
  AND g.valid_until_ms > ?
ORDER BY r.percent DESC, r.pokemon_id, r.form;
```

A single background task publishes each snapshot while the collector continues
accepting observations. Group totals and form rates use the same materialized
sample. Golbat refreshes a rolling 60-minute window every five minutes. A group
needs at least 100 unique encounters from three distinct cells. A missing
group means there is no eligible sample. Percentages have twelve decimal
places. A refresh updates an existing rate only when its percentage changes,
even if the sample size grows. Rates describe observed wild spawns and do
not distinguish event or regional pools.

## Integration test

Set `GOLBAT_TEST_BIOME_DSN` to a **new, empty, disposable** MariaDB database
with `multiStatements=true`, then run
`go test -tags go_json ./decoder -run TestBiomeSpawnSnapshotsMariaDB -count=1`.
The test creates the biome tables and an audit trigger in that database.
