package db

import "testing"

// TestStatsCollectorIsSeeded pins the invariant the nil checks in timing.go
// used to stand in for: statsCollector is never nil, so db/pokestop.go and
// db/stats.go can call straight through the way they already did.
func TestStatsCollectorIsSeeded(t *testing.T) {
	if statsCollector == nil {
		t.Fatal("db.statsCollector is nil at package load; the noop seed is gone")
	}
	// Exercising it is the point — a non-nil interface holding a nil pointer
	// would still panic here.
	statsCollector.IncDbQuery("test", nil)
}

// TestSetStatsCollectorRefusesNil pins the immediate failure. Storing a nil
// collector would replace the seed with something every call site
// dereferences unchecked, so the panic would land on whichever caller
// recorded a stat first rather than on the mistake.
func TestSetStatsCollectorRefusesNil(t *testing.T) {
	previous := statsCollector
	t.Cleanup(func() { statsCollector = previous })

	defer func() {
		if recover() == nil {
			t.Error("db.SetStatsCollector(nil) did not panic")
		}
		if statsCollector == nil {
			t.Error("db.SetStatsCollector(nil) left statsCollector nil")
		}
	}()
	SetStatsCollector(nil)
}
