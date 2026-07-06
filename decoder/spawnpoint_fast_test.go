package decoder

import (
	"testing"
	"time"

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
		got := p.ExpireTimestamp.Int64 - ts/1000
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
