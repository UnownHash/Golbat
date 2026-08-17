package decoder

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"golbat/pogo"
)

func testWild(tthMs int32) RawWildPokemonData {
	return RawWildPokemonData{
		Cell:      9803580370416762880,
		Timestamp: 1755400000123,
		Data: &pogo.WildPokemonProto{
			EncounterId:      12345678901234567890,
			LastModifiedMs:   1755399999456,
			Latitude:         51.501234,
			Longitude:        -0.141592,
			SpawnPointId:     "47c19f4e441",
			TimeTillHiddenMs: tthMs,
			Pokemon:          &pogo.PokemonProto{PokemonId: pogo.HoloPokemonId(129)},
		},
	}
}

// A verified-TTH sighting must map every raw field through unmodified and
// compute the full-precision absolute despawn timestamp (no /1000, no mod-hour).
func TestBuildSpawnpointObservationVerified(t *testing.T) {
	wild := testWild(45500)

	obs, ok := buildSpawnpointObservation(wild, 1755399000, 1755400000500)
	if !ok {
		t.Fatal("TTH 45500ms is within (0, 90000] and must be exported")
	}

	if obs.ReceivedMs != 1755400000500 {
		t.Errorf("received_ms: got %d", obs.ReceivedMs)
	}
	if obs.CellId != "9803580370416762880" {
		t.Errorf("cell_id must be exact decimal string, got %q", obs.CellId)
	}
	if obs.CellAsOfMs != 1755400000123 {
		t.Errorf("cell_as_of_ms: got %d", obs.CellAsOfMs)
	}
	if obs.LastModifiedMs != 1755399999456 {
		t.Errorf("last_modified_ms: got %d", obs.LastModifiedMs)
	}
	if obs.SpawnIdHex != "47c19f4e441" {
		t.Errorf("spawn_id_hex must be the unparsed wire string, got %q", obs.SpawnIdHex)
	}
	if obs.Lat != 51.501234 || obs.Lon != -0.141592 {
		t.Errorf("lat/lon: got %v/%v", obs.Lat, obs.Lon)
	}
	if obs.TthMs != 45500 {
		t.Errorf("tth_ms: got %d", obs.TthMs)
	}
	if obs.DespawnMs != 1755400000123+45500 {
		t.Errorf("despawn_ms must be cell_as_of_ms + tth_ms, got %d", obs.DespawnMs)
	}
	if obs.EncounterId != "12345678901234567890" {
		t.Errorf("encounter_id must be exact decimal string, got %q", obs.EncounterId)
	}
	if obs.PokemonId != 129 {
		t.Errorf("pokemon_id: got %d", obs.PokemonId)
	}
	if obs.FirstSeen != 1755399000 {
		t.Errorf("first_seen: got %d", obs.FirstSeen)
	}
}

// Only TTH in (0, 90000] is client-verified; everything else is garbage and
// must not produce a row. 90000 itself is in range (matches spawnpoint.go).
func TestBuildSpawnpointObservationVerifiedWindow(t *testing.T) {
	cases := []struct {
		tthMs int32
		want  bool
	}{
		{0, false},
		{-1, false},
		{1, true},
		{90000, true},
		{90001, false},
	}
	for _, c := range cases {
		if _, ok := buildSpawnpointObservation(testWild(c.tthMs), 0, 0); ok != c.want {
			t.Errorf("tth=%d: exported=%v, want %v", c.tthMs, ok, c.want)
		}
	}
}

// A wild without a nested PokemonProto must still export (pokemon_id 0),
// not panic.
func TestBuildSpawnpointObservationNilPokemon(t *testing.T) {
	wild := testWild(1000)
	wild.Data.Pokemon = nil
	obs, ok := buildSpawnpointObservation(wild, 0, 0)
	if !ok {
		t.Fatal("nil nested pokemon must not suppress the row")
	}
	if obs.PokemonId != 0 {
		t.Errorf("pokemon_id: got %d, want 0", obs.PokemonId)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The logger must write one parseable JSON object per line and close must
// drain everything already recorded.
func TestSpawnpointObservationLoggerWritesJsonl(t *testing.T) {
	buf := &syncBuffer{}
	l := newSpawnpointObservationLogger(buf, 16)

	obs1, _ := buildSpawnpointObservation(testWild(1000), 1, 2)
	obs2, _ := buildSpawnpointObservation(testWild(2000), 3, 4)
	l.record(obs1)
	l.record(obs2)
	l.close()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %q", len(lines), buf.String())
	}
	var back spawnpointObservation
	if err := json.Unmarshal([]byte(lines[1]), &back); err != nil {
		t.Fatalf("line not valid JSON: %v", err)
	}
	if back.TthMs != 2000 || back.SpawnIdHex != "47c19f4e441" {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}

// gatedWriter blocks the worker goroutine inside Write until the gate is
// closed, so the channel can be filled deterministically.
type gatedWriter struct {
	gate <-chan struct{}
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	<-w.gate
	return len(p), nil
}

// record must never block the decode path: with the worker wedged and the
// channel full, further records are dropped and counted.
func TestSpawnpointObservationLoggerDropsWhenFull(t *testing.T) {
	gate := make(chan struct{})
	l := newSpawnpointObservationLogger(&gatedWriter{gate: gate}, 1)

	obs, _ := buildSpawnpointObservation(testWild(1000), 0, 0)
	// Capacity 1 plus at most 1 in flight in the worker: of 10 records at
	// least 8 must drop, regardless of scheduling.
	for i := 0; i < 10; i++ {
		l.record(obs)
	}
	if d := l.droppedTotal.Load(); d < 8 {
		t.Errorf("expected >=8 drops, got %d", d)
	}
	close(gate)
	l.close()
}

// The hook must be a no-op when the exporter is disabled (nil global) and
// must emit only verified-TTH rows, carrying first_seen, when enabled.
func TestMaybeExportSpawnpointObservation(t *testing.T) {
	// Disabled: must not panic.
	setSpawnpointObservationLogger(nil)
	maybeExportSpawnpointObservation(testWild(1000), 42)

	buf := &syncBuffer{}
	l := newSpawnpointObservationLogger(buf, 16)
	setSpawnpointObservationLogger(l)
	defer setSpawnpointObservationLogger(nil)

	maybeExportSpawnpointObservation(testWild(0), 42)     // unverified: no row
	maybeExportSpawnpointObservation(testWild(70000), 42) // verified: row
	l.close()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("expected exactly 1 row, got %q", buf.String())
	}
	var back spawnpointObservation
	if err := json.Unmarshal([]byte(lines[0]), &back); err != nil {
		t.Fatalf("row not valid JSON: %v", err)
	}
	if back.FirstSeen != 42 {
		t.Errorf("first_seen not carried through: %+v", back)
	}
	if back.ReceivedMs == 0 {
		t.Errorf("hook must stamp received_ms: %+v", back)
	}
}

var _ io.Writer = (*syncBuffer)(nil)
