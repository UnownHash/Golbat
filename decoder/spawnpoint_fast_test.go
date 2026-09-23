package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"
	"golbat/geo"
	"golbat/pogo"

	"github.com/guregu/null/v6"
)

// The lock-free despawn mirror has three states: unsynced (fall back to the
// locked path), known-null, and a valid despawn second. SetDespawnSec and
// the DB-load sync must publish all of them correctly.
func TestDespawnSecFastMirror(t *testing.T) {
	s := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 1}}

	if _, _, synced := s.DespawnSecFast(); synced {
		t.Fatal("fresh spawnpoint must report unsynced (locked-path fallback)")
	}

	s.SetDespawnSec(null.IntFrom(0)) // second-of-hour 0 is a valid value
	if v, known, synced := s.DespawnSecFast(); !synced || !known || v != 0 {
		t.Fatalf("despawn=0: got v=%d known=%v synced=%v", v, known, synced)
	}

	// Note: 0 -> 3599 would be swallowed by the setter's hour-wraparound
	// tolerance (by design), so use a fresh instance for the second value.
	s2 := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 3}}
	s2.SetDespawnSec(null.IntFrom(3599))
	if v, known, synced := s2.DespawnSecFast(); !synced || !known || v != 3599 {
		t.Fatalf("despawn=3599: got v=%d known=%v synced=%v", v, known, synced)
	}

	s.SetDespawnSec(null.NewInt(0, false))
	if _, known, synced := s.DespawnSecFast(); !synced || known {
		t.Fatalf("null despawn must be synced+unknown, got known=%v synced=%v", known, synced)
	}

	// DB-load path bypasses setters; syncDespawnFast publishes directly.
	loaded := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 2, DespawnSec: null.IntFrom(1800)}}
	loaded.syncDespawnFast()
	if v, known, synced := loaded.DespawnSecFast(); !synced || !known || v != 1800 {
		t.Fatalf("post-load sync: got v=%d known=%v synced=%v", v, known, synced)
	}
}

// applyVerifiedDespawn is shared by the lock-free and locked paths; pin the
// second-of-hour wraparound math.
func TestApplyVerifiedDespawn(t *testing.T) {
	// timestamp at 10:50:00 UTC => secondOfHour = 3000
	ts := int64(1751799000000) // 2025-07-06 10:50:00 UTC
	cases := []struct {
		despawnSec int
		wantOffset int64
	}{
		{3100, 100},  // later this hour
		{3000, 0},    // exactly now
		{600, 1200},  // wraps to next hour (600-3000+3600)
		{2999, 3599}, // just missed: nearly a full hour away
	}
	for _, c := range cases {
		p := &Pokemon{}
		p.applyVerifiedDespawn(c.despawnSec, ts)
		if !p.ExpireTimestampVerified {
			t.Fatalf("despawn %d: expiry not verified", c.despawnSec)
		}
		got := int64(p.ExpireTimestamp.ValueOrZero()) - ts/1000
		if got != c.wantOffset {
			t.Fatalf("despawn %d: offset=%d want %d", c.despawnSec, got, c.wantOffset)
		}
	}
}

// The writer fast path must skip the lock exactly when nothing would
// change: known spawnpoint, fresh LastSeen, and (with a TTH) a despawn
// second inside SetDespawnSec's tolerance. despawnSecUnchanged is the
// shared predicate.
func TestDespawnSecUnchanged(t *testing.T) {
	cases := []struct {
		old, new int64
		want     bool
	}{
		{1800, 1800, true},
		{1800, 1802, true},
		{1800, 1803, false},
		{0, 3599, true},  // hour wraparound
		{3599, 1, true},  // hour wraparound
		{3, 3599, false}, // outside wraparound window
	}
	for _, c := range cases {
		if got := despawnSecUnchanged(c.old, c.new); got != c.want {
			t.Errorf("despawnSecUnchanged(%d,%d)=%v want %v", c.old, c.new, got, c.want)
		}
	}
}

// A persisted, fresh spawnpoint must be provably skippable from the
// mirrors alone; an unpersisted or stale one must not be.
func TestWriterFastPathPreconditions(t *testing.T) {
	now := time.Now().Unix()

	fresh := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 1, DespawnSec: null.IntFrom(1200), LastSeen: now}}
	fresh.syncFastFields()
	if _, known, synced := fresh.DespawnSecFast(); !synced || !known {
		t.Fatal("persisted spawnpoint must expose synced+known despawn")
	}
	if last := fresh.LastSeenFast(); last != now {
		t.Fatalf("LastSeenFast=%d want %d", last, now)
	}

	// New record that resolved to no DB row: despawn authoritative-null,
	// but lastSeenFast stays 0 so the writer fast path stays disabled
	// until first persist.
	unpersisted := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 2}, newRecord: true}
	unpersisted.syncDespawnFast()
	if _, known, synced := unpersisted.DespawnSecFast(); !synced || known {
		t.Fatal("no-row spawnpoint must expose synced+null despawn")
	}
	if unpersisted.LastSeenFast() != 0 {
		t.Fatal("unpersisted spawnpoint must report LastSeenFast=0")
	}

	// SetLastSeen publishes the mirror on every path.
	unpersisted.SetLastSeen(now)
	if unpersisted.LastSeenFast() != now {
		t.Fatal("SetLastSeen must publish lastSeenFast")
	}
}

// A wild sighting at 0,0 takes the id-derived location; real coordinates
// are used as sent; an undecodable id with no coordinates is not ok.
func TestWildPokemonLocation(t *testing.T) {
	lat, lon, ok := wildPokemonLocation(8855336329721, &pogo.WildPokemonProto{Latitude: 10.5, Longitude: 20.5})
	if !ok || lat != 10.5 || lon != 20.5 {
		t.Errorf("sent coordinates not used: %f,%f ok=%v", lat, lon, ok)
	}
	lat, lon, ok = wildPokemonLocation(8855336329721, &pogo.WildPokemonProto{})
	if !ok {
		t.Fatal("0,0 sighting with a decodable id must be ok")
	}
	if _, _, ok := wildPokemonLocation(0, &pogo.WildPokemonProto{}); ok {
		t.Error("0,0 sighting with an undecodable id must not be ok")
	}
	if d := haversine(geo.Location{Latitude: lat, Longitude: lon}, geo.Location{Latitude: 34.06451334604487, Longitude: -117.39832236239404}) * 1000; d > 0.5 {
		t.Errorf("0,0 sighting: derived %f,%f is %.2fm off", lat, lon, d)
	}
}

// A wild sighting whose spawnpoint id is not hex is dropped by the
// spawnpoint path without panicking and without creating a spawnpoint.
func TestSpawnpointUpdateFromWildUnparseableId(t *testing.T) {
	wild := &pogo.WildPokemonProto{
		EncounterId:  4243,
		SpawnPointId: "not-hex",
		Latitude:     10.5,
		Longitude:    20.5,
		Pokemon:      &pogo.PokemonProto{PokemonId: 25, PokemonDisplay: &pogo.PokemonDisplayProto{}},
	}
	spawnpointUpdateFromWild(context.Background(), db.DbDetails{}, wild, 1700000000000)
}

// The pokemon is placed by the same rule as the spawnpoint row: a wild
// sighting or encounter at 0,0 takes the id-derived location, real
// coordinates are used as sent, and a 0,0 sighting whose id does not decode
// is refused before the record is touched.
func TestAddWildPokemonPlacement(t *testing.T) {
	const encounterId = 4242
	wild := func(spawnId string, lat, lon float64) *pogo.WildPokemonProto {
		return &pogo.WildPokemonProto{
			EncounterId:  encounterId,
			SpawnPointId: spawnId,
			Latitude:     lat,
			Longitude:    lon,
			Pokemon: &pogo.PokemonProto{
				PokemonId:      25,
				PokemonDisplay: &pogo.PokemonDisplayProto{},
			},
		}
	}
	derived := geo.Location{Latitude: 34.06451334604487, Longitude: -117.39832236239404}
	const derivedSpawnId = "80dcb2d21f9" // 8855336329721, the id TestWildPokemonLocation decodes
	ctx := context.Background()

	// A decodable id reaches setExpireTimestampFromSpawnpoint, which would
	// otherwise go to the (absent) database; a resident spawnpoint keeps it
	// on the cache.
	spawnpointCache.Set(8855336329721, &Spawnpoint{SpawnpointData: SpawnpointData{Id: 8855336329721, Lat: derived.Latitude, Lon: derived.Longitude}}, time.Minute)
	defer spawnpointCache.Delete(8855336329721)

	t.Run("wild: sent coordinates are used", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = encounterId
		if !p.updateFromWild(ctx, db.DbDetails{}, wild("0", 10.5, 20.5), 99, nil, 1700000000000, "tester") {
			t.Fatal("sighting with real coordinates must be accepted")
		}
		if p.Lat != 10.5 || p.Lon != 20.5 {
			t.Errorf("placed at %f,%f, want 10.5,20.5", p.Lat, p.Lon)
		}
	})

	t.Run("wild: 0,0 takes the id-derived location", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = encounterId
		if !p.updateFromWild(ctx, db.DbDetails{}, wild(derivedSpawnId, 0, 0), 99, nil, 1700000000000, "tester") {
			t.Fatal("0,0 sighting with a decodable id must be accepted")
		}
		if d := haversine(geo.Location{Latitude: p.Lat, Longitude: p.Lon}, derived) * 1000; d > 0.5 {
			t.Errorf("placed at %f,%f, %.2fm from the id's cell centre", p.Lat, p.Lon, d)
		}
		if p.SpawnId.ValueOrZero() != 8855336329721 {
			t.Errorf("SpawnId = %d, want 8855336329721", p.SpawnId.ValueOrZero())
		}
	})

	t.Run("wild: 0,0 with an undecodable id is dropped untouched", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = encounterId
		if p.updateFromWild(ctx, db.DbDetails{}, wild("0", 0, 0), 99, nil, 1700000000000, "tester") {
			t.Fatal("0,0 sighting with an undecodable id must be dropped")
		}
		if p.IsDirty() || p.SeenType.Valid || p.CellId.Valid || p.PokemonId != 0 {
			t.Errorf("dropped sighting mutated the record: dirty=%t seenType=%v cell=%v pokemonId=%d", p.IsDirty(), p.SeenType, p.CellId, p.PokemonId)
		}
	})

	t.Run("wild: an unparseable spawnpoint id is dropped untouched", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = encounterId
		if p.updateFromWild(ctx, db.DbDetails{}, wild("not-hex", 10.5, 20.5), 99, nil, 1700000000000, "tester") {
			t.Fatal("sighting with an unparseable spawnpoint id must be dropped")
		}
		if p.IsDirty() || p.Lat != 0 || p.Lon != 0 {
			t.Errorf("dropped sighting mutated the record: dirty=%t at %f,%f", p.IsDirty(), p.Lat, p.Lon)
		}
	})

	t.Run("encounter: 0,0 takes the id-derived location", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = encounterId
		enc := &pogo.EncounterOutProto{Pokemon: wild(derivedSpawnId, 0, 0)}
		if !p.updatePokemonFromEncounterProto(ctx, db.DbDetails{}, enc, "tester", 1700000000000) {
			t.Fatal("0,0 encounter with a decodable id must be accepted")
		}
		if d := haversine(geo.Location{Latitude: p.Lat, Longitude: p.Lon}, derived) * 1000; d > 0.5 {
			t.Errorf("placed at %f,%f, %.2fm from the id's cell centre", p.Lat, p.Lon, d)
		}
		if !p.CellId.Valid || p.CellId.ValueOrZero() == 0 {
			t.Errorf("CellId = %v, want one derived from the placed location", p.CellId)
		}
	})

	t.Run("encounter: 0,0 with an undecodable id is dropped untouched", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = encounterId
		enc := &pogo.EncounterOutProto{Pokemon: wild("0", 0, 0)}
		if p.updatePokemonFromEncounterProto(ctx, db.DbDetails{}, enc, "tester", 1700000000000) {
			t.Fatal("0,0 encounter with an undecodable id must be dropped")
		}
		if p.IsDirty() || p.SeenType.Valid || p.CellId.Valid {
			t.Errorf("dropped encounter mutated the record: dirty=%t seenType=%v cell=%v", p.IsDirty(), p.SeenType, p.CellId)
		}
	})
}
