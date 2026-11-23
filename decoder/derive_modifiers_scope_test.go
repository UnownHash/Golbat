package decoder

import (
	"reflect"
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

// Tests of design §9: only spread zone-wide coverage applies, record entries
// age one by one, a disproven cell stays excluded, and venue cells are
// evidence.

// modStraddleVenue is two adjacent level-14 cells on either side of a
// level-10 cell edge: a venue ~600 m across under two level-10 parents.
func modStraddleVenue(t *testing.T) [2]s2.CellID {
	t.Helper()
	p := cellA.Parent(10)
	for c := p.ChildBeginAtLevel(deriveEventsCellLevel); c != p.ChildEndAtLevel(deriveEventsCellLevel); c = c.Next() {
		for _, n := range c.EdgeNeighbors() {
			if n.Parent(10) != p {
				return [2]s2.CellID{c, n}
			}
		}
	}
	t.Fatal("no level-14 cell on the level-10 edge")
	return [2]s2.CellID{}
}

// A two-cell venue straddling a level-10 edge is not spread: it never goes
// zone-wide and never confirms.
func TestModifierVenueAcrossLevel10EdgeNeverZoneWide(t *testing.T) {
	venue := modStraddleVenue(t)
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 10 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		trs = append(trs, l.feed(modCheck(ts, venue[i%2], withLevelDelta(5)), modZone)...)
		if i%3 == 0 { // the base level elsewhere in the zone
			trs = append(trs, l.feed(modCheck(ts.Add(time.Second), modSpreadCells(4)[i/3]), modZone)...)
		}
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	m := onlyActive(t, l)
	if m.ZoneWide || m.Covers(modZone, unseenCell) || m.Covers(modZone, venue[0]) {
		t.Fatalf("zone-wide %v: a two-cell venue must cover nothing", m.ZoneWide)
	}
	if len(m.Cells) != 2 {
		t.Fatalf("cells %v, want the two venue cells listed", m.Cells)
	}
}

// Zone-wide needs matches from 8 distinct level-14 cells spanning 10 km.
func TestModifierZoneWideNeedsSpread(t *testing.T) {
	far := modSpreadCells(8) // ~55 km apart
	near := make([]s2.CellID, 8)
	for i := range near {
		near[i] = modVenueCell14(cellA, i) // one level-10 cell: under 10 km
	}
	for _, c := range []struct {
		name  string
		cells []s2.CellID
		on    bool
	}{
		{"seven far cells", far[:7], false},
		{"eight cells within 10 km", near, false},
		{"eight far cells", far, true},
	} {
		l := newModifierLearner(defaultModifierParams())
		for i := range 16 {
			l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), c.cells[i%len(c.cells)], withCharged(494)), modZone)
		}
		if m := onlyActive(t, l); m.ZoneWide != c.on || (m.Status == modConfirmed) != c.on {
			t.Errorf("%s: zone-wide %v status %v, want zone-wide and confirmed %v", c.name, m.ZoneWide, m.Status, c.on)
		}
	}
}

// spreadPin feeds 10 charged-494 deviations ending at end, each in another
// of ten cells ~55 km apart, and fails unless the pin confirmed zone-wide.
func spreadPin(t *testing.T, l *modifierLearner, end time.Time) {
	t.Helper()
	confirmPin(t, l, modZone, end, modSpreadCells(10)...)
	if m := modifierWith(t, l, 494); !m.ZoneWide {
		t.Fatal("spread pin not zone-wide")
	}
}

// A cell whose latest check was not the effect stays excluded after its
// entry has left the 16-entry zone record, until a match in that cell.
func TestModifierDisproofLasts(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	spreadPin(t, l, modT0)
	l.feed(modCheck(modT0.Add(time.Second), cellD), modZone)
	if modifierWith(t, l, 494).Covers(modZone, cellD) {
		t.Fatal("cell D covered after its agreement")
	}
	cells := modSpreadCells(16)
	for i := range 16 {
		l.feed(modCheck(modT0.Add(time.Duration(2+i)*time.Second), cells[i], withCharged(494)), modZone)
	}
	if m := modifierWith(t, l, 494); !m.ZoneWide || m.Covers(modZone, cellD) || !m.Covers(modZone, unseenCell) {
		t.Fatalf("zone-wide %v covers D %v: want zone-wide, cell D still excluded after 16 matches elsewhere",
			m.ZoneWide, m.Covers(modZone, cellD))
	}
	l.feed(modCheck(modT0.Add(20*time.Second), cellD, withCharged(494)), modZone)
	if !modifierWith(t, l, 494).Covers(modZone, cellD) {
		t.Fatal("a match in cell D did not readmit it")
	}
}

// Past 256 excluded cells a modifier is no longer zone-wide, whatever the
// record's majority.
func TestModifierExcludedOverflowEndsZoneWide(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	spreadPin(t, l, modT0)
	spread := modSpreadCells(16)
	ts := modT0
	var ended []string
	fed := 0
	for i := 0; i < 300 && len(ended) == 0; i++ {
		evs := []modifierEvent{
			modCheck(ts.Add(time.Second), modTestCell(40.0+0.02*float64(i), -3.0)),
			modCheck(ts.Add(2*time.Second), spread[(2*i)%16], withCharged(494)),
			modCheck(ts.Add(3*time.Second), spread[(2*i+1)%16], withCharged(494)),
		}
		ts = ts.Add(3 * time.Second)
		for _, tr := range feedAll(l, modZone, evs...) {
			if tr.State == modEnded {
				ended = append(ended, tr.Mod.EndReason)
			}
		}
		fed = i + 1
	}
	if len(ended) != 1 || ended[0] != "agreement" || fed != 257 {
		t.Fatalf("ended %v after %d disproven cells, want by agreement at the 257th", ended, fed)
	}
}

// Record entries age one by one: a zone-wide modifier whose matches are
// older than unseen is no longer zone-wide, though a sparse match kept the
// record in use and nothing agreed.
func TestModifierZoneWideMatchesAge(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modSpreadCells(12)
	for i := range 10 {
		l.feed(modCheck(modT0.Add(time.Duration(i-9)*time.Second), cells[i], withLevelDelta(5)), modZone)
	}
	if m := onlyActive(t, l); m.Status != modConfirmed || !m.ZoneWide {
		t.Fatalf("status %v zone-wide %v, want a confirmed zone-wide +5", m.Status, m.ZoneWide)
	}
	if trs := l.feed(modCheck(modT0.Add(50*time.Minute), cells[10], withLevelDelta(5)), modZone); len(trs) != 0 {
		t.Fatalf("a match at +50m: %v, want no transition", states(trs))
	}
	trs := l.sweep(modT0.Add(62 * time.Minute))
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" || trs[0].Mod.ZoneWide {
		t.Fatalf("sweep at +62m with one fresh match: %v, want ended unseen", states(trs))
	}
}

// sizeSpreadEvent returns a learner with a confirmed zone-wide Rattata size
// modifier listing 1/2 = [0.008, 0.033] and 2/3 = [0.033, ..): XXS seen up
// to 0.008 and XS up to 0.033, six of each over twelve cells ~55 km apart,
// one roll every five minutes ending at end.
func sizeSpreadEvent(t *testing.T, end time.Time) *modifierLearner {
	t.Helper()
	l := newModifierLearner(defaultModifierParams())
	cells := modSpreadCells(12)
	for i := range 12 {
		ts := end.Add(time.Duration(i-11) * 5 * time.Minute)
		opt := withSize(0.008, 1)
		if i%2 == 1 {
			opt = withSize(0.033, 2)
		}
		l.feed(modCheck(ts, cells[i], opt), modZone)
	}
	m := onlyActive(t, l)
	if m.Type != modSizeCutoffs || m.Status != modConfirmed || !m.ZoneWide || len(m.Boundaries) != 2 {
		t.Fatalf("modifier %v %v zone-wide %v boundaries %v, want a confirmed zone-wide size modifier with 1/2 and 2/3",
			m.Type, m.Status, m.ZoneWide, m.Boundaries)
	}
	return l
}

// A zone-wide size modifier ends within unseen of its event though a sparse
// informative base roll (one per 50 minutes) keeps refreshing its record:
// its old matches no longer hold the majority once aged.
func TestModifierSizeEndsAfterEventWithSparseBaseRolls(t *testing.T) {
	l := sizeSpreadEvent(t, modT0)
	var ended time.Time
	for min := 1; min <= 120; min++ {
		ts := modT0.Add(time.Duration(min) * time.Minute)
		var trs []modifierTransition
		if min%50 == 0 { // a base roll in the disputed band, in another cell
			trs = l.feed(modCheck(ts, modTestCell(45.0+0.01*float64(min), 7.0), withSize(0.006, 2)), modZone)
		}
		// Uninformative rolls and sightings of the scope keep arriving.
		trs = append(trs, l.feed(modCheck(ts.Add(time.Second), modTestCell(44.0+0.01*float64(min), 7.0), withSize(0.5, 3)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts.Add(2*time.Second), unseenCell, withSize(0.5, 3), asSighting), modZone)...)
		trs = append(trs, l.sweep(ts.Add(3*time.Second))...)
		for _, tr := range trs {
			if tr.State == modEnded && ended.IsZero() {
				ended = ts
			}
		}
	}
	if ended.IsZero() || ended.After(modT0.Add(62*time.Minute)) {
		t.Fatalf("size modifier ended at %v, want within unseen of its last match at %v", ended, modT0)
	}
	for _, m := range l.active() {
		if m.Status == modConfirmed {
			t.Fatalf("a confirmed size modifier is still published two hours after the event: %v", m.Boundaries)
		}
	}
}

// Size exclusion is per boundary: a cell where 2/3 was disproved is not
// readmitted by a match across 1/2 there, only by one across 2/3.
func TestModifierSizeReadmissionPerBoundary(t *testing.T) {
	l := sizeSpreadEvent(t, modT0)
	ts := modT0
	feed := func(roll float64, obs uint8) {
		ts = ts.Add(time.Second)
		if trs := l.feed(modCheck(ts, cellD, withSize(roll, obs)), modZone); len(trs) != 0 {
			t.Fatalf("roll %v class %d: %v, want no transition", roll, obs, states(trs))
		}
	}
	feed(0.032, 3) // an M under the moved 2/3 cut: the base cut holds in D
	if onlyActive(t, l).Covers(modZone, cellD) {
		t.Fatal("cell D covered after a base roll in the 2/3 band")
	}
	feed(0.008, 1) // a match across 1/2 says nothing about 2/3
	if onlyActive(t, l).Covers(modZone, cellD) {
		t.Fatal("a match across 1/2 readmitted cell D, where 2/3 was disproved: a 0.032 roll there would be made XS")
	}
	feed(0.033, 2)
	if !onlyActive(t, l).Covers(modZone, cellD) {
		t.Fatal("a match across 2/3 did not readmit cell D")
	}
}

// A modifier covered only by venue cells is listed, never confirmed and so
// never in the index's applied entries.
func TestModifierVenueOnlyNeverApplied(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 20 {
		ts := modT0.Add(time.Duration(i) * time.Second)
		trs = append(trs, l.feed(modCheck(ts, []s2.CellID{cellA, cellA2}[i%2], withLevelDelta(5)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	m := onlyActive(t, l)
	if m.Status != modCandidate || len(m.Cells) != 2 || m.Covers(modZone, cellA) {
		t.Fatalf("status %v cells %v covers A %v, want a candidate listing two venue cells and covering nothing",
			m.Status, m.Cells, m.Covers(modZone, cellA))
	}
	if idx := buildModifierIndex(l.active()); idx != nil && len(idx.confirmed) != 0 {
		t.Fatalf("index applies %d scopes, want none", len(idx.confirmed))
	}
}

// A candidate has 2 × unseen from its start to confirm; then it ends
// unconfirmed, with its matches and venue counts, and a new one forms from
// fresh deviations. Scattered noise cannot bank matches indefinitely.
func TestModifierCandidateDeadlineTwiceUnseen(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 24 { // a mismatch every five minutes, each in another cell
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*5*time.Minute), modVenueCell14(cellA, i), withIVsDisagree), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("first 115 minutes: %v, want only the candidate", states(trs))
	}
	trs = l.feed(modCheck(modT0.Add(125*time.Minute), modVenueCell14(cellA, 24), withIVsDisagree), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unconfirmed" || trs[0].Mod.Observations != 24 {
		t.Fatalf("after 2 × unseen: %v, want the candidate ended unconfirmed with 24 observations", states(trs))
	}
	if len(l.active()) != 0 {
		t.Fatal("the mismatch that found the candidate ended must start a new cycle as pending")
	}
	trs = feedAll(l, modZone,
		modCheck(modT0.Add(130*time.Minute), modVenueCell14(cellA, 25), withIVsDisagree),
		modCheck(modT0.Add(135*time.Minute), modVenueCell14(cellA, 26), withIVsDisagree))
	if len(trs) != 1 || trs[0].State != modCandidate || trs[0].Mod.Observations != 3 {
		t.Fatalf("fresh deviations: %v, want a new candidate with 3 observations", states(trs))
	}
}

// The learner keeps no encounter-id map: the decode site's first-encounter
// gate is the only dedupe (TestAddEncounterPokemonFeedsLearnerOncePerSpawn).
func TestModifierLearnerHasNoIDMap(t *testing.T) {
	if _, ok := reflect.TypeOf(modifierEvent{}).FieldByName("EncounterID"); ok {
		t.Error("modifierEvent carries an encounter id")
	}
	if _, ok := reflect.TypeOf(modifierLearner{}).FieldByName("checked"); ok {
		t.Error("modifierLearner keeps a checked-id map")
	}
}
