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
// side (SetForm/SetCostume/SetGender) and the comparison side narrows
// silently, so a sighting of an out-of-range display counts one clamp per
// field, not two.
func TestSetPokemonDisplayCountsEachClampOnce(t *testing.T) {
	fake := withClampCountingCollector(t)

	// A fresh record: one clamp per field, from the three setters.
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

	// This second block is the one that catches a fix which buys convergence
	// by routing the comparisons back through the counting clampUint8/16 —
	// do not delete it as redundant. The block above cannot catch that: on a
	// fresh record every `!oldForm.Valid ||` guard short-circuits before its
	// comparison is ever evaluated, so a counting comparison would still
	// total 3 there. From the second sighting on, the fields are Valid, the
	// comparison runs, and the same naive fix totals 9 instead of 6.
	//
	// 6 is the pre-fix rate, unchanged: a repeat sighting stores nothing
	// new, but each setter still clamps its argument on the way in, so the
	// counter advances by one per field per sighting.
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

	// PRE-EXISTING BUG, deliberately left alone here: nearbySignificantUpdate
	// compares pokemon.PokemonId against PokemonDisplay.DisplayId, which is an
	// instance id rather than a pokedex number. updateFromNearby — the work
	// this gate admits — reads NearbyPokemonProto.PokedexNumber instead, so
	// the gate and the update disagree about which field identifies the
	// pokemon. Setting DisplayId here is what it takes to make the gate see
	// "unchanged"; the line exists to hold the wrong-field read still while
	// the narrowing convergence below is asserted, not to record it as
	// intended. Fixing the comparison is a separate change: it would flip the
	// gate's verdict for every nearby sighting whose DisplayId is not equal to
	// its pokedex number, which is most of them.
	display.DisplayId = 25

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
//
// The clamped value is maxIvPerStat, not math.MaxUint8: calculateIv clamps
// at the game's per-stat ceiling rather than the tinyint's, which is what
// bounds Iv inside float(5,2) for every input. See clampIv.
func TestCalculateIvConvergesOnOutOfRangeIv(t *testing.T) {
	p := &Pokemon{}
	p.calculateIv(300, 15, 15)

	if want := null.ValueFrom(uint8(maxIvPerStat)); p.AtkIv != want {
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

// TestRepopulateIvConvergesOnOutOfRangeWeather covers the weather reading
// repopulateIv narrows on the way in. A negative reading is "boosted" read
// raw and NONE once stored, so the guard that asks whether the boost state
// changed never settles: repopulateIv runs on every sighting, fails to find
// a scan matching the boost state it believes in, and clears the IVs, CP
// and PVP it finds there.
//
// Reachable, not theoretical: pogo's enums are open int32 types
// (`type GameplayWeatherProto_WeatherCondition int32`), so protobuf passes
// unknown and negative wire values straight through, and Golbat ingests raw
// protos from third-party scanners.
func TestRepopulateIvConvergesOnOutOfRangeWeather(t *testing.T) {
	display := &pogo.PokemonDisplayProto{
		WeatherBoostedCondition: pogo.GameplayWeatherProto_WeatherCondition(-1),
	}

	p := &Pokemon{}
	// One unboosted scan in history, so locateScan has something to return.
	p.scanHistory = []*pokemonScan{{Weather: 0, Level: 20, Attack: 15, Defense: 15, Stamina: 15}}

	p.setPokemonDisplay(25, display)
	if want := null.ValueFrom(uint8(0)); p.Weather != want {
		t.Fatalf("Weather after a negative reading = %+v, want %+v", p.Weather, want)
	}

	// An encounter enriches the record.
	p.calculateIv(15, 15, 15)
	enrichFromEncounter(p)

	// The same sighting again must not re-run the boost repopulation.
	p.setPokemonDisplay(25, display)

	if !p.Cp.Valid || !p.Pvp.Valid || !p.AtkIv.Valid {
		t.Errorf("repeat sighting of an out-of-range weather cleared encounter data: cp=%+v pvp=%+v atkIv=%+v",
			p.Cp, p.Pvp, p.AtkIv)
	}
}

// TestRepopulateIvConvergesOnOutOfRangeLevel covers the level comparison at
// the end of repopulateIv. A level pushed outside the tinyint saturates at
// 255 in storage while the scan keeps saying 300, so "did the level change?"
// answered yes on every pass and cleared CP and PVP each time.
func TestRepopulateIvConvergesOnOutOfRangeLevel(t *testing.T) {
	p := &Pokemon{}
	// A boosted scan whose computed level lands outside the tinyint. The
	// stored weather stays NONE while the incoming reading is boosted, so
	// repopulateIv's early-return guard never fires and the body runs on
	// every call.
	p.scanHistory = []*pokemonScan{{Weather: 1, Level: 300, Attack: 15, Defense: 15, Stamina: 15}}

	p.repopulateIv(1, false)
	if want := null.ValueFrom(uint8(math.MaxUint8)); p.Level != want {
		t.Fatalf("Level after a level-300 scan = %+v, want %+v", p.Level, want)
	}

	// The first pass cleared CP and PVP legitimately — the level really did
	// change. An encounter refills them.
	enrichFromEncounter(p)

	p.repopulateIv(1, false)
	if !p.Cp.Valid || !p.Pvp.Valid {
		t.Errorf("repeat repopulateIv cleared cp/pvp: cp=%+v pvp=%+v level=%+v", p.Cp, p.Pvp, p.Level)
	}
}

// TestCalculateIvComputesIvFromTheNarrowedValues is the batch-failure
// regression. calculateIv stored clamped atk/def/sta but divided the *raw*
// sum to get Iv, and the convergence fix in the same function made that
// divergence permanent: the narrowed comparison reports "unchanged"
// forever, so the wrong Iv is never recomputed.
//
// iv is float(5,2) unsigned, so it tops out at 999.99. Under MariaDB's
// default STRICT_TRANS_TABLES an out-of-range value fails the *whole*
// multi-row batch upsert, not just the offending row — every other pokemon
// in the batch is lost with it.
//
// The property asserted is therefore the one that maps to the production
// failure and holds for *every* input, not for the inputs a bound happens
// to cover: whatever a, d and s are, the computed Iv fits float(5,2)
// unsigned and the stored IVs are legal IVs. Clamping the inputs at the
// tinyint's 255 was not enough for that — three at the ceiling divide to
// 1700, two alone to 1133 — which is why calculateIv clamps at
// maxIvPerStat. The generator below drives that hard.
func TestCalculateIvComputesIvFromTheNarrowedValues(t *testing.T) {
	// float(5,2) unsigned: five significant digits, two after the point.
	const ivColumnMax = 999.99

	check := func(t *testing.T, a, d, s int64, wantIv float64) {
		t.Helper()
		p := &Pokemon{}
		p.calculateIv(a, d, s)

		if !p.Iv.Valid {
			t.Fatalf("Iv after calculateIv(%d, %d, %d) is invalid", a, d, s)
		}
		got := float64(p.Iv.ValueOrZero())
		if got > ivColumnMax {
			t.Errorf("Iv after calculateIv(%d, %d, %d) = %v, want <= %v — MariaDB rejects this under STRICT_TRANS_TABLES and fails the entire batch upsert",
				a, d, s, got, ivColumnMax)
		}
		// A legal IV is a legal IV whatever arrived. Above maxIvPerStat the
		// iv column stops being a percentage, PokemonLookup's int8 turns 255
		// into the -1 "no IV known" sentinel, and ohbem is handed a value
		// outside its domain.
		for _, f := range []struct {
			name string
			v    null.Value[uint8]
		}{{"AtkIv", p.AtkIv}, {"DefIv", p.DefIv}, {"StaIv", p.StaIv}} {
			if !f.v.Valid {
				t.Errorf("%s after calculateIv(%d, %d, %d) is invalid", f.name, a, d, s)
				continue
			}
			if f.v.ValueOrZero() > maxIvPerStat {
				t.Errorf("%s after calculateIv(%d, %d, %d) = %d, want <= %d (a legal IV)",
					f.name, a, d, s, f.v.ValueOrZero(), maxIvPerStat)
			}
		}
		// The stores and the Iv sum must agree, whatever the inputs were.
		stored := int64OrZero(p.AtkIv) + int64OrZero(p.DefIv) + int64OrZero(p.StaIv)
		if want := float64(stored) / .45; math.Abs(got-want) > 0.01 {
			t.Errorf("Iv = %v but atk+def+sta = %d divides to %v — the stores and the Iv computation disagree",
				got, stored, want)
		}
		if math.Abs(got-wantIv) > 0.01 {
			t.Errorf("Iv after calculateIv(%d, %d, %d) = %v, want %v",
				a, d, s, got, wantIv)
		}
	}

	cases := []struct {
		name    string
		a, d, s int64
		wantIv  float64
	}{
		// The production shape: one field far out of range drags the raw sum
		// past 450 while the stored value saturates.
		{"one field out of range", 1000, 15, 15, 100},
		// Two out of range. Clamped at the tinyint's 255 these would be
		// 510/.45 = 1133, past the column and into a failed batch; this is
		// the case a 255 ceiling cannot cover at all.
		{"two fields out of range", 300, 260, 15, 100},
		// Every field out of range, as far out as the raw sum goes.
		{"all three out of range", 1 << 40, 1 << 40, 1 << 40, 100},
		// The other end of saturate: a negative reading stores 0, so the raw
		// sum is now *lower* than what was stored. Divergence in both
		// directions, one clamp.
		{"one field below zero", -5, 15, 15, (0 + 15 + 15) / .45},
		// Legal but above 15 — inside the tinyint, outside the game. Iv from
		// the raw sum would be 155.6%, which the column accepts and every
		// consumer misreads.
		{"legal byte, illegal IV", 20, 20, 30, 100},
		// A real hundo, and a real nundo, untouched by any of this.
		{"hundo", 15, 15, 15, 100},
		{"nundo", 0, 0, 0, 0},
		{"ordinary", 15, 14, 13, (15 + 14 + 13) / .45},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { check(t, c.a, c.d, c.s, c.wantIv) })
	}

	// And the same property over a wide sweep of the input space, including
	// the whole tinyint range and well past it, so no future ceiling change
	// can quietly reintroduce a band that overflows the column.
	t.Run("sweep", func(t *testing.T) {
		for _, a := range []int64{-1 << 40, -1, 0, 7, 15, 16, 100, 254, 255, 256, 450, 1000, 1 << 40} {
			for _, d := range []int64{0, 15, 255, 1 << 40} {
				for _, s := range []int64{0, 15, 255, 1 << 40} {
					p := &Pokemon{}
					p.calculateIv(a, d, s)
					if got := float64(p.Iv.ValueOrZero()); got > ivColumnMax || got < 0 {
						t.Fatalf("calculateIv(%d, %d, %d) produced Iv = %v, outside float(5,2) unsigned", a, d, s, got)
					}
				}
			}
		}
	})
}

// TestSetChangedSaturatesAndCounts pins the last narrowed setter that was
// still converting bare. changed is a 32-bit unsigned column, so uint32(v)
// wraps rather than saturating — a negative or past-2106 timestamp landed on
// an arbitrary value with no metric behind it.
func TestSetChangedSaturatesAndCounts(t *testing.T) {
	fake := withClampCountingCollector(t)

	p := &Pokemon{}
	p.SetChanged(math.MaxUint32 + 1)
	if p.Changed != math.MaxUint32 {
		t.Errorf("SetChanged(MaxUint32+1) stored %d, want %d", p.Changed, uint32(math.MaxUint32))
	}
	p.SetChanged(-1)
	if p.Changed != 0 {
		t.Errorf("SetChanged(-1) stored %d, want 0", p.Changed)
	}
	if got := fake.count("changed"); got != 2 {
		t.Errorf("clamp count for changed = %d, want 2", got)
	}

	// An in-range timestamp is stored untouched and counts nothing.
	const now = 1700000000
	p.SetChanged(now)
	if p.Changed != now {
		t.Errorf("SetChanged(%d) stored %d, want %d", now, p.Changed, now)
	}
	if got := fake.count("changed"); got != 2 {
		t.Errorf("clamp count for changed after an in-range value = %d, want 2", got)
	}
}
