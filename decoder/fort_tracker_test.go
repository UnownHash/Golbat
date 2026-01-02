package decoder

import (
	"testing"
)

func TestProcessCellUpdate_RemovesStaleGym(t *testing.T) {
	// Initialize tracker with 1 second stale threshold
	InitFortTracker(1)
	ft := GetFortTracker()
	if ft == nil {
		t.Fatal("fortTracker is nil after InitFortTracker")
	}

	cellId := uint64(12345)
	gymId := "gym_1"

	// Set up initial state: cell has the gym seen at time 1000ms
	ft.mu.Lock()
	cell := ft.getOrCreateCellLocked(cellId)
	cell.lastSeen = 500 // previous lastSeen
	cell.gyms = make(map[string]struct{})
	cell.gyms[gymId] = struct{}{}
	ft.forts[gymId] = &FortTrackerLastSeen{cellId: cellId, lastSeen: 1000, isGym: true}
	ft.mu.Unlock()

	// Now simulate a GMO for the same cell with no gyms and timestamp advanced beyond stale threshold
	result := ft.ProcessCellUpdate(cellId, []string{}, []string{}, int64(1000+2000)) // timestamp in ms

	if result == nil {
		t.Fatal("ProcessCellUpdate returned nil; expected a result")
	}

	if len(result.StaleGyms) == 0 {
		t.Fatalf("expected stale gyms, got none: %+v", result)
	}

	found := false
	for _, id := range result.StaleGyms {
		if id == gymId {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected gym %s to be marked stale, result: %+v", gymId, result)
	}

	// Also ensure no false positives for pokestops
	if len(result.StalePokestops) != 0 {
		t.Fatalf("unexpected stale pokestops: %+v", result.StalePokestops)
	}

	// cleanup
	InitFortTracker(3600)
}
