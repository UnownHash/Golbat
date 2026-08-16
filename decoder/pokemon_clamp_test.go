package decoder

import (
	"math"
	"sync"
	"testing"

	"golbat/stats_collector"

	"github.com/guregu/null/v6"
)

// clampCountingCollector embeds whatever StatsCollector is passed in (the
// noop, in these tests) so every other method keeps its normal no-op
// behavior, and counts IncFieldClamped calls by field — the noop discards
// its argument, so it can't be asserted against directly.
type clampCountingCollector struct {
	stats_collector.StatsCollector
	mu      sync.Mutex
	clamped map[string]int
}

func newClampCountingCollector() *clampCountingCollector {
	return &clampCountingCollector{
		StatsCollector: stats_collector.NewNoopStatsCollector(),
		clamped:        make(map[string]int),
	}
}

func (c *clampCountingCollector) IncFieldClamped(field string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clamped[field]++
}

func (c *clampCountingCollector) count(field string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clamped[field]
}

// withClampCountingCollector swaps the package-level statsCollector for the
// duration of one test and restores it on cleanup, via the shared
// setStatsCollectorForTest helper (init_test.go) — race-free against the
// background stats-aggregation worker/ticker because statsCollector is an
// atomic.Pointer.
func withClampCountingCollector(t *testing.T) *clampCountingCollector {
	t.Helper()
	fake := newClampCountingCollector()
	setStatsCollectorForTest(t, fake)
	return fake
}

// TestSetGenderClampsUint8BoundaryAndCounts pins clampUint8's boundary
// behavior via its simplest caller, SetGender: an out-of-range value lands
// on the tinyint boundary (0 or 255) and increments
// golbat_field_clamped_total exactly once; an in-range value or a null
// input clamps nothing.
func TestSetGenderClampsUint8BoundaryAndCounts(t *testing.T) {
	fake := withClampCountingCollector(t)

	over := &Pokemon{}
	over.SetGender(null.IntFrom(300))
	if want := null.ValueFrom(uint8(math.MaxUint8)); over.Gender != want {
		t.Errorf("Gender after SetGender(300) = %+v, want %+v", over.Gender, want)
	}
	if got := fake.count("gender"); got != 1 {
		t.Errorf("clamp count after over-range SetGender = %d, want 1", got)
	}

	under := &Pokemon{}
	under.SetGender(null.IntFrom(-1))
	if want := null.ValueFrom(uint8(0)); under.Gender != want {
		t.Errorf("Gender after SetGender(-1) = %+v, want %+v", under.Gender, want)
	}
	if got := fake.count("gender"); got != 2 {
		t.Errorf("clamp count after under-range SetGender = %d, want 2 (cumulative)", got)
	}

	inRange := &Pokemon{}
	inRange.SetGender(null.IntFrom(1))
	if want := null.ValueFrom(uint8(1)); inRange.Gender != want {
		t.Errorf("Gender after SetGender(1) = %+v, want %+v (unclamped)", inRange.Gender, want)
	}
	if got := fake.count("gender"); got != 2 {
		t.Errorf("clamp count after in-range SetGender = %d, want still 2 (no new clamp)", got)
	}

	nullInput := &Pokemon{}
	nullInput.SetGender(null.Int{})
	if nullInput.Gender.Valid {
		t.Errorf("Gender after SetGender(null) = %+v, want invalid", nullInput.Gender)
	}
	if got := fake.count("gender"); got != 2 {
		t.Errorf("clamp count after null SetGender = %d, want still 2 (invalid input never clamps)", got)
	}
}

// TestSetCpClampsUint16BoundaryAndCounts pins clampUint16's boundary via
// SetCp.
func TestSetCpClampsUint16BoundaryAndCounts(t *testing.T) {
	fake := withClampCountingCollector(t)

	over := &Pokemon{}
	over.SetCp(null.IntFrom(70000))
	if want := null.ValueFrom(uint16(math.MaxUint16)); over.Cp != want {
		t.Errorf("Cp after SetCp(70000) = %+v, want %+v", over.Cp, want)
	}
	if got := fake.count("cp"); got != 1 {
		t.Errorf("clamp count after over-range SetCp = %d, want 1", got)
	}

	under := &Pokemon{}
	under.SetCp(null.IntFrom(-1))
	if want := null.ValueFrom(uint16(0)); under.Cp != want {
		t.Errorf("Cp after SetCp(-1) = %+v, want %+v", under.Cp, want)
	}
	if got := fake.count("cp"); got != 2 {
		t.Errorf("clamp count after under-range SetCp = %d, want 2 (cumulative)", got)
	}

	inRange := &Pokemon{}
	inRange.SetCp(null.IntFrom(1500))
	if want := null.ValueFrom(uint16(1500)); inRange.Cp != want {
		t.Errorf("Cp after SetCp(1500) = %+v, want %+v (unclamped)", inRange.Cp, want)
	}
	if got := fake.count("cp"); got != 2 {
		t.Errorf("clamp count after in-range SetCp = %d, want still 2 (no new clamp)", got)
	}
}

// TestSetExpireTimestampClampsUint32BoundaryAndCounts pins clampUint32's
// boundary via SetExpireTimestamp.
func TestSetExpireTimestampClampsUint32BoundaryAndCounts(t *testing.T) {
	fake := withClampCountingCollector(t)

	over := &Pokemon{}
	over.SetExpireTimestamp(null.IntFrom(math.MaxUint32 + 100))
	if want := null.ValueFrom(uint32(math.MaxUint32)); over.ExpireTimestamp != want {
		t.Errorf("ExpireTimestamp after over-range Set = %+v, want %+v", over.ExpireTimestamp, want)
	}
	if got := fake.count("expire_timestamp"); got != 1 {
		t.Errorf("clamp count after over-range SetExpireTimestamp = %d, want 1", got)
	}

	under := &Pokemon{}
	under.SetExpireTimestamp(null.IntFrom(-5))
	if want := null.ValueFrom(uint32(0)); under.ExpireTimestamp != want {
		t.Errorf("ExpireTimestamp after under-range Set = %+v, want %+v", under.ExpireTimestamp, want)
	}
	if got := fake.count("expire_timestamp"); got != 2 {
		t.Errorf("clamp count after under-range SetExpireTimestamp = %d, want 2 (cumulative)", got)
	}

	inRange := &Pokemon{}
	inRange.SetExpireTimestamp(null.IntFrom(1700000000))
	if want := null.ValueFrom(uint32(1700000000)); inRange.ExpireTimestamp != want {
		t.Errorf("ExpireTimestamp after in-range Set = %+v, want %+v (unclamped)", inRange.ExpireTimestamp, want)
	}
	if got := fake.count("expire_timestamp"); got != 2 {
		t.Errorf("clamp count after in-range SetExpireTimestamp = %d, want still 2 (no new clamp)", got)
	}
}

// TestSetHeightNeverClamps pins clampFloat32's deliberate lack of range
// checking (weight/height/iv are all far inside float32's range — see
// clampFloat32's doc comment): no input increments golbat_field_clamped_total.
func TestSetHeightNeverClamps(t *testing.T) {
	fake := withClampCountingCollector(t)

	p := &Pokemon{}
	p.SetHeight(null.FloatFrom(1234.5))
	if got := fake.count("height"); got != 0 {
		t.Errorf("clamp count after SetHeight = %d, want 0 (clampFloat32 never clamps)", got)
	}
}
