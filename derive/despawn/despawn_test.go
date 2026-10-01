package despawn

import (
	"testing"
	"time"
)

// An id that approaches the modular wrap without crossing it: v + 34486400
// lands 12 short of 2^30. An implementation whose wrap boundary is off by a
// few agrees everywhere it is likely to be sampled and disagrees here, so
// this one vector is the cheap way to catch it.
func TestPositionMsBoundaryVector(t *testing.T) {
	if got := PositionMs(0x47b973e0595); got != 1784988 {
		t.Fatalf("boundary token: got %d want 1784988", got)
	}
}

// Live ground-truth vectors: real spawn ids with the despawn position
// observed for each in game. These are the only assertions here answerable
// from outside the code — everything else checks the derivation against
// itself, so a shared misconception would survive it. These four would not.
func TestPositionMsLiveVectors(t *testing.T) {
	for _, c := range []struct {
		spawnID   int64
		wantPosMs int64
	}{
		{0x47dec0a8621, 503756},
		{0x47deb691a53, 486420},
		{0x47dec0b94b9, 477996},
		{0x47decbaae8b, 538420},
	} {
		if got := PositionMs(c.spawnID); got != c.wantPosMs {
			t.Errorf("PositionMs(%#x): got %d want %d", c.spawnID, got, c.wantPosMs)
		}
	}
}

// Odd ids only: every real spawn id is odd, and the derivation is defined
// only for those. Even ids stay in range but leave the grid — measured, all
// 199999 of them below 400000 — so nothing should assert against them. The
// loop steps by 2 from 1 to stay on odd ids.
func TestPositionIsWithinHalfHour(t *testing.T) {
	for id := int64(1); id < 200000; id += 2 {
		p := PositionMs(id)
		if p < 0 || p >= 1800000 {
			t.Fatalf("id %d: position %d outside the half hour", id, p)
		}
		// Positions land on an 8ms grid, offset by 4.
		if p%8 != 4 {
			t.Fatalf("id %d: position %d off grid", id, p)
		}
	}
}

// Only the low 28 bits matter.
func TestHighBitsDoNotMatter(t *testing.T) {
	const id = int64(0x47b973e0595)
	if PositionMs(id) != PositionMs(id&0x0FFFFFFF) {
		t.Fatal("high bits changed the result")
	}
}

// The answer is always the next occurrence strictly after now, and always
// within one half hour of it.
func TestNextOccurrenceIsAheadAndWithinHalfHour(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 17, 33, 0, time.UTC)
	for id := int64(1); id < 5000; id++ {
		got := NextOccurrence(id, now)
		if !got.After(now) {
			t.Fatalf("id %d: %s is not after %s", id, got, now)
		}
		if got.Sub(now) > 30*time.Minute {
			t.Fatalf("id %d: %s is more than a half hour after %s", id, got, now)
		}
	}
}
