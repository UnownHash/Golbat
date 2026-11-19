package decoder

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/jmoiron/sqlx"
)

func TestBiomeGeographySignature(t *testing.T) {
	lake := biomeGeography{Landform: 4, Landcover: 1, Temperature: 2, Features: []int32{5}}
	forestLake := lake
	forestLake.Landcover = 3
	forestLake.Features = []int32{1, 5}
	if !bytes.Equal(lake.signature(), forestLake.signature()) {
		t.Fatal("cool lake cells must share rates across land covers and secondary vistas")
	}
	for _, change := range []func(*biomeGeography){
		func(f *biomeGeography) { f.Temperature = 3 },
		func(f *biomeGeography) { f.Features = []int32{5, 2} },
		func(f *biomeGeography) { f.Features = nil; f.Landcover = 5 },
	} {
		other := lake
		change(&other)
		if bytes.Equal(lake.signature(), other.signature()) {
			t.Fatal("different habitat categories must have distinct keys")
		}
	}
	lake.Temperature = 0
	if lake.signature() != nil {
		t.Fatal("incomplete geography must not get a spawn group")
	}
}

func TestBiomeSpawnTimeZone(t *testing.T) {
	scope := func(lat, lng float64) string {
		return biomeSpawnScope(s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lng)).Parent(16))
	}
	if scope(52.51, 13.58) != "tz:Europe/Berlin" || scope(52.13, 11.62) != scope(52.51, 13.58) {
		t.Fatal("Berlin and Magdeburg must share a time-zone scope")
	}
	if scope(48.86, 2.35) == scope(52.51, 13.58) {
		t.Fatal("equal UTC offsets must not combine different named time zones")
	}
}

func TestBiomeWeatherWindow(t *testing.T) {
	hour := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		weather, spawn time.Duration
		want           bool
	}{
		{"fresh", time.Minute, 2 * time.Minute, true},
		{"age limit", time.Minute, 16 * time.Minute, true},
		{"expired", time.Minute, 16*time.Minute + time.Millisecond, false},
		{"previous hour", -time.Millisecond, 0, false},
		{"future report", time.Minute, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := biomeEvent{weather: 1, atMS: hour.Add(tt.weather).UnixMilli()}
			if got := report.coversSpawn(hour.Add(tt.spawn).UnixMilli()); got != tt.want {
				t.Fatalf("coversSpawn = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestBiomeWildObservationsKeepForms(t *testing.T) {
	previous := biomeCollector
	biomeCollector = &biomeWorker{queue: make(chan biomeEvent, 3)}
	t.Cleanup(func() { biomeCollector = previous })

	var wild []RawWildPokemonData
	for index, form := range []int32{0, 42, -1} {
		pokemon := &pogo.PokemonProto{PokemonId: 133}
		if form >= 0 {
			pokemon.PokemonDisplay = &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(form)}
		}
		wild = append(wild, RawWildPokemonData{Data: &pogo.WildPokemonProto{
			EncounterId: uint64(index + 1), Latitude: 52.52, Longitude: 13.40, Pokemon: pokemon,
		}})
	}

	ObserveBiomeWildBatch(ScanParameters{ProcessWeather: true, ProcessPokemon: true, ProcessWild: true}, wild, nil, 1)
	for _, want := range []int32{0, 42, -1} {
		select {
		case observation := <-biomeCollector.queue:
			if observation.form != want || observation.pokemonID != 133 {
				t.Fatalf("observation species/form = %d/%d, want 133/%d", observation.pokemonID, observation.form, want)
			}
		default:
			t.Fatal("missing observation")
		}
	}
}

// GOLBAT_TEST_BIOME_DSN must point to a new, empty disposable MariaDB database.
func TestBiomeSpawnSnapshotsMariaDB(t *testing.T) {
	dsn := os.Getenv("GOLBAT_TEST_BIOME_DSN")
	if dsn == "" {
		t.Skip("requires a disposable MariaDB database")
	}
	conn, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	run := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile("../sql/56_biome_observations.up.sql")
	run(err)
	_, err = conn.Exec(string(schema))
	run(err)
	_, err = conn.Exec("CREATE TABLE biome_rate_test_updates (form INT)")
	run(err)
	_, err = conn.Exec(`CREATE TRIGGER biome_rate_test_audit AFTER UPDATE ON biome_spawn_rate
		FOR EACH ROW INSERT INTO biome_rate_test_updates (form) VALUES (NEW.form)`)
	run(err)

	ctx := context.Background()
	worker := &biomeWorker{db: conn}
	now := time.Now().Truncate(time.Millisecond)
	cellAt := func(lat, lng float64) uint64 {
		return uint64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lng)).Parent(16))
	}
	cells := []uint64{cellAt(52.51, 13.58), cellAt(52.13, 11.62), cellAt(48.40, 9.99)}
	at := now.Add(-time.Minute).UnixMilli()
	for index, cell := range cells {
		fields := biomeGeography{Landform: 4, Landcover: int32(index + 1), Temperature: 2, Features: []int32{5}}
		run(worker.persist(ctx, []biomeEvent{{kind: biomeCellEvent, cellID: cell, geography: fields, atMS: at}}))
	}
	// A profile written by the earlier collector must join the same pool
	// even if no new encounter for that cell has arrived.
	_, err = conn.Exec("UPDATE biome_cell SET spawn_scope='12345', biome_key=NULL WHERE cell_id=?", cells[0])
	run(err)
	_, err = worker.backfillScopeBatch(ctx, 0)
	run(err)
	var preserved int64
	run(conn.Get(&preserved, "SELECT geography_changed_at_ms FROM biome_cell WHERE cell_id=? AND spawn_scope='tz:Europe/Berlin'", cells[0]))
	if preserved != at {
		t.Fatal("reclassification changed the geography timestamp")
	}

	// A changed-cell audit counts writes even if an UPDATE assigns the old values.
	_, err = conn.Exec("CREATE TABLE biome_cell_test_updates (cell_id BIGINT UNSIGNED)")
	run(err)
	_, err = conn.Exec(`CREATE TRIGGER biome_cell_test_audit AFTER UPDATE ON biome_cell
		FOR EACH ROW INSERT INTO biome_cell_test_updates (cell_id) VALUES (NEW.cell_id)`)
	run(err)
	profile := biomeEvent{kind: biomeCellEvent, cellID: cells[0], atMS: at + 10,
		geography: biomeGeography{Landform: 4, Landcover: 1, Temperature: 2, Features: []int32{5}}}
	// Force a cold read of the existing profile, then confirm it from cache.
	worker = &biomeWorker{db: conn, cellFloor: at + 1}
	run(worker.persist(ctx, []biomeEvent{profile}))
	profile.atMS++
	run(worker.persist(ctx, []biomeEvent{profile}))
	checkCell := func(writes int, savedAt int64) {
		t.Helper()
		var count int
		var observed int64
		run(conn.Get(&count, "SELECT COUNT(*) FROM biome_cell_test_updates"))
		run(conn.Get(&observed, "SELECT observed_at_ms FROM biome_cell WHERE cell_id=?", cells[0]))
		if count != writes || observed != savedAt {
			t.Fatalf("cell updates/time = %d/%d, want %d/%d", count, observed, writes, savedAt)
		}
	}
	checkCell(0, at)
	// Moisture changes the full display profile while retaining the same habitat.
	profile.geography.Moisture++
	profile.atMS--
	run(worker.persist(ctx, []biomeEvent{profile}))
	checkCell(0, at)
	profile.atMS = at + 20
	repeat := profile
	repeat.atMS++
	run(worker.persist(ctx, []biomeEvent{profile, repeat, profile}))
	checkCell(1, profile.atMS)
	// A delayed conflicting packet after restart must not revert the saved cell.
	worker = &biomeWorker{db: conn, cellFloor: at + 30}
	repeat.geography.Moisture--
	run(worker.persist(ctx, []biomeEvent{repeat}))
	checkCell(1, profile.atMS)
	var moisture int
	run(conn.Get(&moisture, "SELECT JSON_VALUE(geography,'$.moisture') FROM biome_cell WHERE cell_id=?", cells[0]))
	if moisture != int(profile.geography.Moisture) {
		t.Fatalf("stored moisture = %d", moisture)
	}

	add := func(from, until int, at time.Time) {
		t.Helper()
		events := make([]biomeEvent, 0, until-from)
		for i := from; i < until; i++ {
			events = append(events, biomeEvent{
				kind: biomeSpawnEvent, cellID: cells[i%len(cells)], atMS: at.UnixMilli(),
				weather: 1, pokemonID: 133, form: int32(10 + i%2),
				encounterKey: [16]byte{byte(i >> 8), byte(i)},
			})
		}
		run(worker.persist(ctx, events))
	}
	add(0, 120, now)
	add(0, 120, now)
	run(worker.refresh(ctx, now))
	var sample int
	run(conn.Get(&sample, "SELECT sample_count FROM biome_spawn_group"))
	if sample != 120 {
		t.Fatalf("sample count = %d, want 120 unique encounters", sample)
	}
	rates := []string{}
	run(conn.Select(&rates, "SELECT percent FROM biome_spawn_rate ORDER BY form"))
	if len(rates) != 2 || rates[0] != "50.000000000000" || rates[1] != rates[0] {
		t.Fatalf("rates by form = %v, want 50%% for each", rates)
	}

	later := now.Add(5 * time.Minute)
	add(120, 240, later)
	run(worker.refresh(ctx, later))
	run(conn.Get(&sample, "SELECT sample_count FROM biome_spawn_group"))
	if sample != 240 {
		t.Fatalf("sample count = %d, want 240", sample)
	}
	var writes int
	run(conn.Get(&writes, "SELECT COUNT(*) FROM biome_rate_test_updates"))
	if writes != 0 {
		t.Fatalf("unchanged rates received %d updates", writes)
	}
	add(240, 241, later)
	run(worker.refresh(ctx, later))
	run(conn.Get(&writes, "SELECT COUNT(*) FROM biome_rate_test_updates"))
	if writes != 2 {
		t.Fatalf("changed rates received %d updates, want 2", writes)
	}

	// An in-flight sighting must not block the snapshot's read of the
	// committed window, even when its row is locked by another connection.
	writer, err := conn.BeginTxx(ctx, nil)
	run(err)
	defer func() { _ = writer.Rollback() }()
	_, err = writer.ExecContext(ctx, "UPDATE biome_spawn_observation SET weather=2 WHERE encounter_key=?", make([]byte, 16))
	run(err)
	refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err = worker.refresh(refreshCtx, later)
	cancel()
	run(err)
	run(writer.Rollback())

	run(worker.refresh(ctx, later.Add(time.Hour)))
	run(conn.Get(&sample, "SELECT COUNT(*) FROM biome_spawn_rate"))
	if sample != 0 {
		t.Fatalf("expired window still has %d rates", sample)
	}

	// Retention must remove only expired observations and preserve newer ones.
	run(worker.refresh(ctx, now.Add(biomeRetention)))
	run(conn.Get(&sample, "SELECT COUNT(*) FROM biome_spawn_observation"))
	if sample != 121 {
		t.Fatalf("retention kept %d observations, want the 121 newer sightings", sample)
	}
}
