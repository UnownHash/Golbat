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

func TestProcessCellUpdate_PendingGymBecomesStaleAfterMultipleScans(t *testing.T) {
	// Initialize tracker with 5 second stale threshold
	InitFortTracker(5)
	ft := GetFortTracker()
	if ft == nil {
		t.Fatal("fortTracker is nil after InitFortTracker")
	}

	cellId := uint64(12345)
	gymId := "gym_pending"

	// Set up initial state: cell has the gym seen at time 1000ms
	ft.mu.Lock()
	cell := ft.getOrCreateCellLocked(cellId)
	cell.lastSeen = 500
	cell.gyms = make(map[string]struct{})
	cell.gyms[gymId] = struct{}{}
	ft.forts[gymId] = &FortTrackerLastSeen{cellId: cellId, lastSeen: 1000, isGym: true}
	ft.mu.Unlock()

	// First scan: gym missing but not yet stale (only 2 seconds passed)
	result1 := ft.ProcessCellUpdate(cellId, []string{}, []string{}, 3000)
	if result1 == nil {
		t.Fatal("ProcessCellUpdate returned nil on first scan")
	}
	if len(result1.StaleGyms) != 0 {
		t.Fatalf("gym should not be stale yet on first scan, got: %v", result1.StaleGyms)
	}

	// Verify gym is still tracked in cell (this was the bug - it was being removed)
	ft.mu.RLock()
	_, inCell := ft.cells[cellId].gyms[gymId]
	ft.mu.RUnlock()
	if !inCell {
		t.Fatal("pending gym was removed from cell tracking - this is the bug!")
	}

	// Second scan: gym still missing, now stale (6 seconds total since lastSeen)
	result2 := ft.ProcessCellUpdate(cellId, []string{}, []string{}, 6500)
	if result2 == nil {
		t.Fatal("ProcessCellUpdate returned nil on second scan")
	}
	if len(result2.StaleGyms) != 1 || result2.StaleGyms[0] != gymId {
		t.Fatalf("expected gym %s to be stale on second scan, got: %v", gymId, result2.StaleGyms)
	}

	// cleanup
	InitFortTracker(3600)
}
