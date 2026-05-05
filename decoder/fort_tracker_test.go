package decoder

import (
	"testing"
	"time"
)

// resetTracker reinitialises the global tracker for the next test.
func resetTracker() {
	InitFortTracker(3600, 1)
}

func TestProcessCellUpdate_RemovesStaleGym(t *testing.T) {
	InitFortTracker(1, 1)
	defer resetTracker()
	ft := GetFortTracker()
	if ft == nil {
		t.Fatal("fortTracker is nil after InitFortTracker")
	}

	cellId := uint64(12345)
	gymId := "gym_1"

	now := time.Now().UnixMilli()
	ft.mu.Lock()
	cell := ft.getOrCreateCellLocked(cellId)
	cell.lastSeen = now - 5000
	cell.gyms[gymId] = struct{}{}
	ft.forts[gymId] = &FortTrackerLastSeen{cellId: cellId, lastSeen: now - 4000, isGym: true}
	ft.mu.Unlock()

	result := ft.ProcessCellUpdate(cellId, nil, nil, now)
	if result == nil {
		t.Fatal("ProcessCellUpdate returned nil; expected a result")
	}

	if len(result.StaleGyms) != 1 || result.StaleGyms[0] != gymId {
		t.Fatalf("expected gym %s to be marked stale, got: %+v", gymId, result.StaleGyms)
	}
	if len(result.StalePokestops) != 0 {
		t.Fatalf("unexpected stale pokestops: %+v", result.StalePokestops)
	}
}

func TestProcessCellUpdate_PendingGymBecomesStaleAfterMultipleScans(t *testing.T) {
	InitFortTracker(5, 1)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(12346)
	gymId := "gym_pending"

	// Synthetic small timestamps: well below time.Now().UnixMilli()+60_000,
	// so the future-timestamp guard does not reject them.
	ft.mu.Lock()
	cell := ft.getOrCreateCellLocked(cellId)
	cell.lastSeen = 500
	cell.gyms[gymId] = struct{}{}
	ft.forts[gymId] = &FortTrackerLastSeen{cellId: cellId, lastSeen: 1000, isGym: true}
	ft.mu.Unlock()

	// First scan: 2s gap, < 5s threshold → pending.
	result1 := ft.ProcessCellUpdate(cellId, nil, nil, 3000)
	if result1 == nil || len(result1.StaleGyms) != 0 {
		t.Fatalf("gym should not be stale on first scan, got: %+v", result1)
	}

	ft.mu.RLock()
	_, inCell := ft.cells[cellId].gyms[gymId]
	ft.mu.RUnlock()
	if !inCell {
		t.Fatal("pending gym was removed from cell tracking")
	}

	// Second scan: 5.5s gap, ≥ 5s threshold → stale.
	result2 := ft.ProcessCellUpdate(cellId, nil, nil, 6500)
	if result2 == nil {
		t.Fatal("ProcessCellUpdate returned nil on second scan")
	}
	if len(result2.StaleGyms) != 1 || result2.StaleGyms[0] != gymId {
		t.Fatalf("expected gym %s stale on second scan, got: %v", gymId, result2.StaleGyms)
	}
}

func TestProcessCellUpdate_NewFortOnFirstScanTrackedForFutureStaleCheck(t *testing.T) {
	InitFortTracker(1, 1)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(99999)
	gymId := "new_gym_first_scan"

	now := time.Now().UnixMilli()
	result1 := ft.ProcessCellUpdate(cellId, nil, []string{gymId}, now-3000)
	if result1 == nil {
		t.Fatal("ProcessCellUpdate returned nil on first scan")
	}

	ft.mu.RLock()
	_, inCell := ft.cells[cellId].gyms[gymId]
	ft.mu.RUnlock()
	if !inCell {
		t.Fatal("new gym from first scan was not added to cell tracking")
	}

	result2 := ft.ProcessCellUpdate(cellId, nil, nil, now-2500)
	if result2 == nil || len(result2.StaleGyms) != 0 {
		t.Fatalf("gym should not yet be stale, got: %+v", result2)
	}

	result3 := ft.ProcessCellUpdate(cellId, nil, nil, now)
	if result3 == nil {
		t.Fatal("ProcessCellUpdate returned nil on third scan")
	}
	if len(result3.StaleGyms) != 1 || result3.StaleGyms[0] != gymId {
		t.Fatalf("expected gym %s stale, got: %v", gymId, result3.StaleGyms)
	}
}

// Regression for the firstScan-overwrite bug (§1.1): preload registers
// {A,B,C} into a cell, a partial GMO {A} arrives, and after the stale
// window a second partial GMO {A} should mark B and C stale (not before).
func TestProcessCellUpdate_PartialFirstGMOPreservesPreloadedForts(t *testing.T) {
	InitFortTracker(1, 1)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(0xdeadbeef)
	registerTs := int64(10_000)

	ft.RegisterFort("A", cellId, false, registerTs)
	ft.RegisterFort("B", cellId, false, registerTs)
	ft.RegisterFort("C", cellId, false, registerTs)

	// First GMO is partial: only sees A; gap from register is 500ms < 1s threshold.
	result1 := ft.ProcessCellUpdate(cellId, []string{"A"}, nil, registerTs+500)
	if result1 == nil {
		t.Fatal("ProcessCellUpdate returned nil on first GMO")
	}
	if len(result1.StalePokestops) != 0 {
		t.Fatalf("first partial GMO must not mark anything stale, got: %v", result1.StalePokestops)
	}

	ft.mu.RLock()
	for _, id := range []string{"A", "B", "C"} {
		if _, ok := ft.cells[cellId].pokestops[id]; !ok {
			ft.mu.RUnlock()
			t.Fatalf("preloaded pokestop %s lost from cell tracking after partial first GMO", id)
		}
	}
	ft.mu.RUnlock()

	// Second partial GMO past the threshold: B and C must now be flagged.
	result2 := ft.ProcessCellUpdate(cellId, []string{"A"}, nil, registerTs+5000)
	if result2 == nil {
		t.Fatal("ProcessCellUpdate returned nil on second GMO")
	}
	stale := map[string]bool{}
	for _, id := range result2.StalePokestops {
		stale[id] = true
	}
	if !stale["B"] || !stale["C"] {
		t.Fatalf("expected B and C stale after partial second GMO, got: %v", result2.StalePokestops)
	}
	if stale["A"] {
		t.Fatalf("A is present in GMO and must not be stale")
	}
}

// §2.2: an implausibly future timestamp must not wedge a cell.
func TestProcessCellUpdate_RejectsFutureTimestamp(t *testing.T) {
	InitFortTracker(1, 1)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(0xfeedface)
	now := time.Now().UnixMilli()

	if got := ft.ProcessCellUpdate(cellId, nil, nil, now+24*60*60*1000); got != nil {
		t.Fatalf("expected nil for far-future GMO, got: %+v", got)
	}

	ft.mu.RLock()
	cell, ok := ft.cells[cellId]
	wedged := ok && cell.lastSeen != 0
	ft.mu.RUnlock()
	if wedged {
		t.Fatal("future-dated GMO wedged the cell (cell.lastSeen advanced)")
	}

	if got := ft.ProcessCellUpdate(cellId, []string{"X"}, nil, now); got == nil {
		t.Fatal("subsequent legitimate GMO must be processed")
	}
}

// §2.1: RestoreFort followed by a partial GMO must not immediately re-flag.
func TestRestoreFort_DoesNotImmediatelyRestale(t *testing.T) {
	InitFortTracker(60, 1)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(0xabc)
	stopId := "restored_stop"
	now := time.Now().UnixMilli()

	ft.RestoreFort(stopId, cellId, false, now)

	// A partial GMO for the same cell that doesn't include the restored fort.
	result := ft.ProcessCellUpdate(cellId, nil, nil, now+1000)
	if result == nil {
		t.Fatal("ProcessCellUpdate returned nil")
	}
	if len(result.StalePokestops) != 0 {
		t.Fatalf("restored fort must not be re-staled by the next GMO: %v", result.StalePokestops)
	}
}

// §2.6: id appearing in both pokestopIds and gymIds — gym wins.
func TestProcessCellUpdate_TypeRaceDedup(t *testing.T) {
	InitFortTracker(60, 1)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(0x123abc)
	id := "ambiguous_fort"
	now := time.Now().UnixMilli()

	result := ft.ProcessCellUpdate(cellId, []string{id}, []string{id}, now)
	if result == nil {
		t.Fatal("ProcessCellUpdate returned nil")
	}

	ft.mu.RLock()
	_, inGyms := ft.cells[cellId].gyms[id]
	_, inStops := ft.cells[cellId].pokestops[id]
	info := ft.forts[id]
	ft.mu.RUnlock()

	if !inGyms || inStops {
		t.Fatalf("expected fort to live only in gyms set on tie, got gyms=%v pokestops=%v", inGyms, inStops)
	}
	if info == nil || !info.isGym {
		t.Fatalf("expected fort info isGym=true, got: %+v", info)
	}
}

// §6.2: with minMissCount=2, a single missing scan past the time threshold
// is not enough; require two consecutive cell-scan misses.
func TestProcessCellUpdate_MissCountGuard(t *testing.T) {
	InitFortTracker(1, 2)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(0x456)
	stopId := "miss_count_stop"
	now := time.Now().UnixMilli()

	ft.mu.Lock()
	cell := ft.getOrCreateCellLocked(cellId)
	cell.lastSeen = now - 10000
	cell.pokestops[stopId] = struct{}{}
	ft.forts[stopId] = &FortTrackerLastSeen{cellId: cellId, lastSeen: now - 9000, isGym: false}
	ft.mu.Unlock()

	// First missing scan: time threshold met but missCount=1 < 2.
	result1 := ft.ProcessCellUpdate(cellId, nil, nil, now-5000)
	if result1 == nil || len(result1.StalePokestops) != 0 {
		t.Fatalf("expected no stale on first miss with minMissCount=2, got: %+v", result1)
	}

	// Second missing scan: missCount=2, now stale.
	result2 := ft.ProcessCellUpdate(cellId, nil, nil, now)
	if result2 == nil || len(result2.StalePokestops) != 1 {
		t.Fatalf("expected stale after second miss, got: %+v", result2)
	}
}

// §6.2: a sighting between two missing scans must reset missCount.
func TestProcessCellUpdate_MissCountResetOnSighting(t *testing.T) {
	InitFortTracker(1, 2)
	defer resetTracker()
	ft := GetFortTracker()

	cellId := uint64(0x789)
	stopId := "flapping_stop"
	now := time.Now().UnixMilli()

	ft.mu.Lock()
	cell := ft.getOrCreateCellLocked(cellId)
	cell.lastSeen = now - 10000
	cell.pokestops[stopId] = struct{}{}
	ft.forts[stopId] = &FortTrackerLastSeen{cellId: cellId, lastSeen: now - 9000, isGym: false}
	ft.mu.Unlock()

	// Miss, then sighting (resets), then miss again. Should not be stale yet.
	if r := ft.ProcessCellUpdate(cellId, nil, nil, now-6000); r == nil || len(r.StalePokestops) != 0 {
		t.Fatalf("first miss: %+v", r)
	}
	if r := ft.ProcessCellUpdate(cellId, []string{stopId}, nil, now-4000); r == nil {
		t.Fatal("sighting GMO returned nil")
	}
	if r := ft.ProcessCellUpdate(cellId, nil, nil, now-2000); r == nil || len(r.StalePokestops) != 0 {
		t.Fatalf("post-sighting miss must reset count, got: %+v", r)
	}
}
