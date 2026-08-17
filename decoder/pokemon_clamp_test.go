package decoder

import (
	"math"
	"sync"
	"testing"

	"golbat/pogo"
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

// total reports how many clamps were counted across every field.
func (c *clampCountingCollector) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	sum := 0
	for _, n := range c.clamped {
		sum += n
	}
	return sum
}

// outOfRangeDisplay is a display whose costume, gender and form all sit
// outside the columns they are stored in (tinyint, tinyint, smallint
// unsigned). Costume is the one that matters in practice: the enum's
// highest value today is 87 and Niantic ships new costumes routinely, so
// 255 is a question of when, not whether.
func outOfRangeDisplay() *pogo.PokemonDisplayProto {
	return &pogo.PokemonDisplayProto{
		Costume: pogo.PokemonDisplayProto_Costume(300),
		Gender:  pogo.PokemonDisplayProto_Gender(300),
		Form:    pogo.PokemonDisplayProto_Form(70000),
	}
}

// enrichFromEncounter fills in the fields an encounter contributes — the
// ones setPokemonDisplay's changed-branch wipes.
func enrichFromEncounter(p *Pokemon) {
	p.SetCp(null.IntFrom(1500))
	p.SetMove1(null.IntFrom(216))
	p.SetMove2(null.IntFrom(58))
	p.SetShiny(null.BoolFrom(false))
	p.SetWeight(null.FloatFrom(6.5))
	p.SetHeight(null.FloatFrom(0.6))
	p.SetSize(null.IntFrom(3))
	p.SetPvp(null.StringFrom(`{"great":[]}`))
}

// TestSetPokemonDisplayConvergesOnOutOfRangeDisplay is the regression test
// for the clamped-vs-raw comparison bug: a costume of 300 is stored as 255,
// and comparing the stored 255 against the raw 300 reported a change on
// every later sighting of the same, unchanged pokemon. The changed-branch
// nulls out weight, height, size, moves, CP, shiny, ditto and PVP — so
// every encounter that enriched a pokemon was undone by the next sighting,
// forever, for every pokemon carrying an out-of-range display.
func TestSetPokemonDisplayConvergesOnOutOfRangeDisplay(t *testing.T) {
	display := outOfRangeDisplay()

	p := &Pokemon{}
	// First sighting: the record has no display yet, so the changed-branch
	// firing here is correct. It stores the narrowed values.
	p.setPokemonDisplay(25, display)

	if want := null.ValueFrom(uint8(math.MaxUint8)); p.Costume != want {
		t.Fatalf("Costume after out-of-range sighting = %+v, want %+v", p.Costume, want)
	}
	if want := null.ValueFrom(uint8(math.MaxUint8)); p.Gender != want {
		t.Fatalf("Gender after out-of-range sighting = %+v, want %+v", p.Gender, want)
	}
	if want := null.ValueFrom(uint16(math.MaxUint16)); p.Form != want {
		t.Fatalf("Form after out-of-range sighting = %+v, want %+v", p.Form, want)
	}

	// An encounter arrives and enriches the record.
	enrichFromEncounter(p)

	// A second, identical sighting must leave that enrichment alone.
	p.setPokemonDisplay(25, display)

	if !p.Cp.Valid || !p.Move1.Valid || !p.Move2.Valid || !p.Shiny.Valid ||
		!p.Weight.Valid || !p.Height.Valid || !p.Size.Valid || !p.Pvp.Valid {
		t.Errorf("an unchanged repeat sighting wiped encounter data: "+
			"cp=%+v move1=%+v move2=%+v shiny=%+v weight=%+v height=%+v size=%+v pvp=%+v",
			p.Cp, p.Move1, p.Move2, p.Shiny, p.Weight, p.Height, p.Size, p.Pvp)
	}
}

// TestSetPokemonDisplayCountsEachClampOnce is the other half of the
// regression: the comparisons must converge *without* the fix costing an
// extra golbat_field_clamped_total increment. Counting lives on the write
// side (SetForm/SetCostume/SetGender), the comparison side narrows silently
// — so one sighting of an out-of-range display counts one clamp per field,
// not two. Routing the comparisons back through clampUint8/16 would double
// these numbers.
func TestSetPokemonDisplayCountsEachClampOnce(t *testing.T) {
	fake := withClampCountingCollector(t)

	p := &Pokemon{}
	p.setPokemonDisplay(25, outOfRangeDisplay())

	for _, field := range []string{"costume", "gender", "form"} {
		if got := fake.count(field); got != 1 {
			t.Errorf("clamp count for %q after one sighting = %d, want 1", field, got)
		}
	}
	if got := fake.total(); got != 3 {
		t.Errorf("total clamp count after one sighting = %d, want 3 (costume, gender, form)", got)
	}

	// A repeat sighting stores nothing new, but each setter still clamps its
	// argument on the way in, so the counter advances by exactly one per
	// field per sighting — the pre-fix rate, unchanged.
	p.setPokemonDisplay(25, outOfRangeDisplay())
	if got := fake.total(); got != 6 {
		t.Errorf("total clamp count after two sightings = %d, want 6 (one per field per sighting)", got)
	}
}

// TestSignificantUpdateConvergesOnOutOfRangeDisplay covers the same
// divergence in the two GMO gates. Left unfixed, every wild and nearby
// sighting of a pokemon with an out-of-range display counts as significant,
// so it is re-decoded and re-written forever.
func TestSignificantUpdateConvergesOnOutOfRangeDisplay(t *testing.T) {
	display := outOfRangeDisplay()
	display.DisplayId = 25 // nearbySignificantUpdate reads the id from here

	p := &Pokemon{}
	p.setPokemonDisplay(25, display)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetExpireTimestampVerified(true)

	now := int64(1700000000)

	wild := &pogo.WildPokemonProto{
		Pokemon: &pogo.PokemonProto{PokemonId: 25, PokemonDisplay: display},
	}
	if p.wildSignificantUpdate(wild, now) {
		t.Error("wildSignificantUpdate reported a change for an unchanged out-of-range display")
	}

	nearby := &pogo.NearbyPokemonProto{PokedexNumber: 25, PokemonDisplay: display}
	if p.nearbySignificantUpdate(nearby, now) {
		t.Error("nearbySignificantUpdate reported a change for an unchanged out-of-range display")
	}
}

// TestCalculateIvConvergesOnOutOfRangeIv covers the third comparison site:
// out-of-range IVs are stored clamped, so comparing them against the raw
// proto values re-wrote and re-dirtied the record on every encounter.
func TestCalculateIvConvergesOnOutOfRangeIv(t *testing.T) {
	p := &Pokemon{}
	p.calculateIv(300, 15, 15)

	if want := null.ValueFrom(uint8(math.MaxUint8)); p.AtkIv != want {
		t.Fatalf("AtkIv after calculateIv(300, ...) = %+v, want %+v", p.AtkIv, want)
	}

	p.dirty = false
	p.calculateIv(300, 15, 15)
	if p.dirty {
		t.Error("calculateIv re-stored an unchanged, already-clamped IV set")
	}
}

// TestNarrowSaturatesWithoutCounting pins the non-counting half of the
// clamp/narrow split: same boundaries as clampUint8/16/32, zero metric.
func TestNarrowSaturatesWithoutCounting(t *testing.T) {
	fake := withClampCountingCollector(t)

	cases := []struct {
		name string
		got  int64
		want int64
	}{
		{"narrowUint8 under", narrowUint8(-1), 0},
		{"narrowUint8 over", narrowUint8(300), math.MaxUint8},
		{"narrowUint8 in range", narrowUint8(7), 7},
		{"narrowUint16 under", narrowUint16(-1), 0},
		{"narrowUint16 over", narrowUint16(70000), math.MaxUint16},
		{"narrowUint16 in range", narrowUint16(3357), 3357},
		{"narrowUint32 under", narrowUint32(-1), 0},
		{"narrowUint32 over", narrowUint32(math.MaxUint32 + 100), math.MaxUint32},
		{"narrowUint32 in range", narrowUint32(1700000000), 1700000000},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	if got := fake.total(); got != 0 {
		t.Errorf("narrow* counted %d clamps, want 0 (counting is the clamp* side's job)", got)
	}
}
