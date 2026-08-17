package decoder

import (
	"testing"

	"golbat/stats_collector"
)

// TestStatsCollectorSeedIsInTheVariableInitializer pins where the noop seed
// lives, not just that it exists. Seeding from init() would leave a window:
// package-level variable initializers all run before init(), so a var in this
// package whose initializer reached getStatsCollector would have found a nil
// interface. Seeding inside statsCollector's own initializer closes that,
// because Go's initialization dependency analysis follows function calls and
// orders any such variable after this one.
//
// newSeededStatsCollector is called directly rather than reading the package
// variable, which init_test.go has already overwritten by the time any test
// runs.
func TestStatsCollectorSeedIsInTheVariableInitializer(t *testing.T) {
	p := newSeededStatsCollector()
	if p == nil {
		t.Fatal("newSeededStatsCollector returned nil")
	}
	seeded := p.Load()
	if seeded == nil || *seeded == nil {
		t.Fatal("newSeededStatsCollector stored no collector")
	}
	// A non-nil interface holding a nil pointer would still panic here.
	(*seeded).IncFieldClamped("test")
}

// TestSetStatsCollectorRefusesNil pins the immediate failure. Storing a nil
// collector would put a nil interface behind the atomic.Pointer that
// getStatsCollector dereferences unchecked, so it would surface as a panic on
// whichever decode goroutine recorded a stat first — arbitrarily far from the
// call that caused it.
func TestSetStatsCollectorRefusesNil(t *testing.T) {
	previous := statsCollector.Load()
	t.Cleanup(func() { statsCollector.Store(previous) })

	defer func() {
		if recover() == nil {
			t.Error("SetStatsCollector(nil) did not panic")
		}
		if getStatsCollector() == nil {
			t.Error("SetStatsCollector(nil) left getStatsCollector() nil")
		}
	}()
	SetStatsCollector(nil)
}

// TestSetStatsCollectorMarksTheOrderingFlag covers the input to
// InitWriteBehindQueue's ordering guard. The queues take the collector by
// value, so calling InitWriteBehindQueue first hands them the noop seed
// permanently and every write-behind metric silently reads zero for the life
// of the process; the guard turns that into a boot panic. It is not called
// here — doing so with the flag forced false would build the real queues if
// the guard were ever removed.
func TestSetStatsCollectorMarksTheOrderingFlag(t *testing.T) {
	previous := statsCollector.Load()
	previousSet := statsCollectorSet.Load()
	t.Cleanup(func() {
		statsCollector.Store(previous)
		statsCollectorSet.Store(previousSet)
	})

	statsCollectorSet.Store(false)
	SetStatsCollector(stats_collector.NewNoopStatsCollector())
	if !statsCollectorSet.Load() {
		t.Error("SetStatsCollector did not mark statsCollectorSet; InitWriteBehindQueue's ordering guard would fire on a correct boot")
	}
}
