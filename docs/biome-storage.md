# Biome geography and wild spawn rates

Enable `[biomes].enabled` to record encounter geography and publish observed
wild spawn rates in MariaDB. Collection is off by default. Run one collector
per database. Golbat applies the schema migration at startup even when
collection is disabled.

Successful wild encounters provide the geography of their level-16 S2 cell.
For spawn estimates, the scan rule must process wild Pokémon and weather.
A sighting counts once, after the cell's geography is known and a weather
report is available for its level-10 parent. The weather report must be from
the same UTC hour and at most 15 minutes old.

By default, samples are local to a level-10 weather cell. To pool observations
across geofences, list their scan-rule area names in `[biomes].shared_areas`.
Cells whose centres fall in those areas share estimates when both geography
and weather match. Other cells keep their local scope.

## Reading the tables

| Table | Contents |
| --- | --- |
| `biome_cell` | Level-16 S2 cell ID, geography as JSON (game enum values), geography signature and aggregation scope |
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

Golbat refreshes a rolling 60-minute window every five minutes. A group
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
