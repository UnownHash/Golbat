package decoder

import (
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/golang/geo/s2"

	"golbat/derive/encounter"
)

const modZone = "Europe/London"

func modTestCell(lat, lon float64) s2.CellID {
	return s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lon)).Parent(deriveEventsCellLevel)
}

// Distinct level-14 cells (~600 m across), a few km apart.
var (
	cellA      = modTestCell(51.50, -0.12)
	cellB      = modTestCell(51.52, -0.08)
	cellC      = modTestCell(51.47, -0.16)
	cellD      = modTestCell(51.56, -0.22)
	unseenCell = modTestCell(51.40, -0.30)
	// cellE and cellF lie under level-10 cells other than cellD's.
	cellE = modTestCell(51.70, -0.12)
	cellF = modTestCell(51.62, -0.40)
	// cellA2 and cellA3 share cellA's level-10 cell: a venue around cellA.
	cellA2 = modVenueCell14(cellA, 1)
	cellA3 = modVenueCell14(cellA, 2)
)

// modVenueCell14 is the n-th level-14 cell after c under c's level-10 cell
// (wrapping inside it), so it is never c for 0 < n < 256.
func modVenueCell14(c s2.CellID, n int) s2.CellID {
	p := c.Parent(modZoneWideSpreadLevel)
	for range n {
		if c = c.Next(); c == p.ChildEndAtLevel(deriveEventsCellLevel) {
			c = p.ChildBeginAtLevel(deriveEventsCellLevel)
		}
	}
	return c
}

// modSpreadCells returns n cells under n different level-10 cells.
func modSpreadCells(n int) []s2.CellID {
	out := make([]s2.CellID, n)
	for i := range out {
		out[i] = modTestCell(48.0+0.5*float64(i), 2.0)
	}
	return out
}

func withID(id uint64) func(*modifierEvent) {
	return func(ev *modifierEvent) { ev.EncounterID = id }
}

var modT0 = time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

// modCheck is an encounter check of strong 959/3272 at ts in cell with every
// dimension agreeing (derived charged move 268), changed by opts.
func modCheck(ts time.Time, cell s2.CellID, opts ...func(*modifierEvent)) modifierEvent {
	ev := modifierEvent{
		Unix:       ts.Unix(),
		Cell:       cell,
		SizeRoll:   0.9,
		PokemonID:  959,
		Form:       3272,
		ObsQuick:   200,
		ObsCharged: 268,
		ObsSize:    4,
		Agree:      dimAgreement{IVs: dimAgree, Level: dimAgree, Size: dimAgree, Quick: dimAgree, Charged: dimAgree},
		Strong:     true,
	}
	for _, o := range opts {
		o(&ev)
	}
	return ev
}

func withCharged(move int32) func(*modifierEvent) {
	return func(ev *modifierEvent) { ev.ObsCharged, ev.Agree.Charged = move, dimDisagree }
}

func withLevelDelta(d int8) func(*modifierEvent) {
	return func(ev *modifierEvent) { ev.LevelDelta, ev.Agree.Level = d, dimDisagree }
}

func withSpecies(id, form int32) func(*modifierEvent) {
	return func(ev *modifierEvent) { ev.PokemonID, ev.Form = id, form }
}

func withIVsDisagree(ev *modifierEvent) { ev.Agree.IVs = dimDisagree }

func asSighting(ev *modifierEvent) {
	*ev = modifierEvent{Unix: ev.Unix, Cell: ev.Cell, PokemonID: ev.PokemonID, Form: ev.Form,
		Strong: ev.Strong, Sighting: true}
}

func feedAll(l *modifierLearner, zone string, evs ...modifierEvent) []modifierTransition {
	var out []modifierTransition
	for _, ev := range evs {
		out = append(out, l.feed(ev, zone)...)
	}
	return out
}

func states(trs []modifierTransition) []modifierStatus {
	out := make([]modifierStatus, len(trs))
	for i, tr := range trs {
		out[i] = tr.State
	}
	return out
}

// confirmPin feeds 10 charged-494 deviations, one per second ending at end,
// spread round-robin over cells, and fails unless the pin confirmed.
func confirmPin(t *testing.T, l *modifierLearner, zone string, end time.Time, cells ...s2.CellID) {
	t.Helper()
	var trs []modifierTransition
	for i := range 10 {
		ts := end.Add(time.Duration(i-9) * time.Second)
		trs = append(trs, l.feed(modCheck(ts, cells[i%len(cells)], withCharged(494)), zone)...)
	}
	if len(trs) != 2 || trs[1].State != modConfirmed {
		t.Fatalf("transitions %v, want candidate then confirmed", states(trs))
	}
}

func onlyActive(t *testing.T, l *modifierLearner) *derivationModifier {
	t.Helper()
	act := l.active()
	if len(act) != 1 {
		t.Fatalf("active modifiers = %d, want 1", len(act))
	}
	return act[0]
}

func TestModifierMovePinLifecycle(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 3 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*20*time.Second), cellA, withCharged(494)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("after 3 deviations: %v, want one candidate", states(trs))
	}
	m := trs[0].Mod
	if m.Type != modMovePin || m.Effect != (modifierEffect{Slot: "charged", Move: 494}) {
		t.Fatalf("candidate %v %+v, want move_pin charged=494", m.Type, m.Effect)
	}
	if want := (modifierScope{PokemonID: 959, Form: 3272, Strong: true}); m.Scope != want {
		t.Fatalf("scope %+v, want %+v", m.Scope, want)
	}
	if !m.Start.Equal(modT0) || m.Zone != modZone {
		t.Fatalf("start %v zone %q", m.Start, m.Zone)
	}

	trs = nil
	for i := range 7 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Minute+time.Duration(i)*time.Second), cellA, withCharged(494)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modConfirmed || trs[0].Mod.Observations != 10 {
		t.Fatalf("after 10 deviations: %v, want confirmed with 10 observations", states(trs))
	}
	if m := trs[0].Mod; m.ZoneWide || !m.Covers(modZone, cellA) || len(m.Cells) != 1 {
		t.Fatalf("zone-wide %v cells %v, want the venue cell A only", m.ZoneWide, m.Cells)
	}

	// One base result in its only cell disproves it.
	trs = l.feed(modCheck(modT0.Add(2*time.Minute), cellA), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "agreement" {
		t.Fatalf("after an agreement in cell A: %v, want ended by agreement", states(trs))
	}
	if len(l.active()) != 0 {
		t.Fatal("ended modifier still active")
	}
}

func TestModifierAgreementOtherCellDoesNotGate(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 3 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		trs = append(trs, l.feed(modCheck(ts.Add(-time.Second), cellB), modZone)...)
		trs = append(trs, l.feed(modCheck(ts, cellA, withCharged(494)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("agreements in cellB: %v, want a candidate", states(trs))
	}

	l = newModifierLearner(defaultModifierParams())
	trs = nil
	for i := range 3 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		if i > 0 {
			trs = append(trs, l.feed(modCheck(ts.Add(-time.Second), cellA), modZone)...)
		}
		trs = append(trs, l.feed(modCheck(ts, cellA, withCharged(494)), modZone)...)
	}
	if len(trs) != 0 {
		t.Fatalf("agreements in cellA: %v, want no candidate", states(trs))
	}
}

// A ticketed venue: its cells deviate while the rest of the zone agrees, so
// the zone record never has a majority and the pin covers its cells only.
func TestModifierVenueStaysLocal(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	cells := []s2.CellID{cellA, cellB, cellC}
	for i := range 10 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		trs = append(trs, l.feed(modCheck(ts, cells[i%3], withCharged(494)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts.Add(time.Second), cellD), modZone)...)
		trs = append(trs, l.feed(modCheck(ts.Add(2*time.Second), cellE), modZone)...)
	}
	if len(trs) != 2 || trs[1].State != modConfirmed {
		t.Fatalf("transitions %v, want candidate then confirmed", states(trs))
	}
	m := onlyActive(t, l)
	if m.ZoneWide {
		t.Fatal("venue modifier went zone-wide")
	}
	if !m.Covers(modZone, cellA) || !m.Covers(modZone, cellB) || !m.Covers(modZone, cellC) || len(m.Cells) != 3 {
		t.Fatalf("cells %v, want the venue cells A, B and C", m.Cells)
	}
	if m.Covers(modZone, cellD) || m.Covers(modZone, unseenCell) || m.Covers(modZone, 0) {
		t.Fatal("venue modifier covers a cell outside the venue")
	}
}

func TestModifierGlobalGoesZoneWide(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	m := onlyActive(t, l)
	if !m.ZoneWide {
		t.Fatalf("not zone-wide: cells %d", len(m.Cells))
	}
	if !m.Covers(modZone, unseenCell) || !m.Covers(modZone, 0) {
		t.Fatal("zone-wide modifier does not cover an unseen cell of its zone")
	}
	if m.Covers("America/New_York", cellA) {
		t.Fatal("covers another zone")
	}
}

// Zone-wide coverage needs the effect in more than half of the zone record
// and a record of at least confirm checks (so at least 6 of 10).
func TestModifierZoneWideNeedsRecordMajority(t *testing.T) {
	spread := modSpreadCells(16)
	rec := func(matches, agreements int) modVerdict {
		var r modRecord
		for i := range matches {
			r.push(spread[i], 494, false, 1)
		}
		for i := range agreements {
			r.push(spread[matches+i], 268, true, 1)
		}
		return r.judge(494, true, 10)
	}
	for _, c := range []struct {
		matches, agreements int
		on                  bool
	}{{9, 0, false}, {10, 0, true}, {6, 4, true}, {5, 5, false}, {9, 7, true}, {8, 8, false}, {16, 0, true}} {
		if got := rec(c.matches, c.agreements).on; got != c.on {
			t.Errorf("%d matches, %d agreements: zone-wide %v, want %v", c.matches, c.agreements, got, c.on)
		}
	}
}

// TestModifierZoneWideExcludesDisprovenCell: a cell whose latest check did
// not show the effect — it agreed, or showed another effect — is excluded
// from a zone-wide modifier, and readmitted by a match there.
func TestModifierZoneWideExcludesDisprovenCell(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	trs := feedAll(l, modZone,
		modCheck(modT0.Add(time.Second), cellD),
		modCheck(modT0.Add(2*time.Second), cellE, withCharged(300)))
	if len(trs) != 0 {
		t.Fatalf("an agreement in cell D, a 300 in cell E: %v, want no transition", states(trs))
	}
	m := modifierWith(t, l, 494)
	want := []s2.CellID{cellD, cellE}
	slices.Sort(want)
	if !m.ZoneWide || !slices.Equal(m.Excluded, want) {
		t.Fatalf("zone-wide %v excluded %v, want zone-wide less cells D and E", m.ZoneWide, m.Excluded)
	}
	if m.Covers(modZone, cellD) || m.Covers(modZone, cellE) || !m.Covers(modZone, cellA) || !m.Covers(modZone, unseenCell) {
		t.Fatal("want every cell of the zone covered but cells D and E")
	}
	feedAll(l, modZone,
		modCheck(modT0.Add(3*time.Second), cellD, withCharged(494)),
		modCheck(modT0.Add(4*time.Second), cellE, withCharged(494)))
	if m := modifierWith(t, l, 494); len(m.Excluded) != 0 || !m.Covers(modZone, cellD) || !m.Covers(modZone, cellE) {
		t.Fatalf("excluded %v after a match in each, want none", m.Excluded)
	}
}

// TestModifierZoneWideEndsWithRecordMajority: a zone-wide modifier with no
// venue cell (every match in another cell, as under sampling) ends as soon
// as its effect is no longer more than half of the record: by agreement, or
// contradicted when another effect displaced it.
func TestModifierZoneWideEndsWithRecordMajority(t *testing.T) {
	cells := modSpreadCells(26)
	for _, c := range []struct {
		reason string
		opt    func(*modifierEvent)
	}{{"agreement", func(*modifierEvent) {}}, {"contradicted", withCharged(300)}} {
		l := newModifierLearner(defaultModifierParams())
		confirmPin(t, l, modZone, modT0, cells[:10]...)
		if m := onlyActive(t, l); !m.ZoneWide || len(m.Cells) != 0 {
			t.Fatalf("zone-wide %v cells %v, want zone-wide with no venue cell", m.ZoneWide, m.Cells)
		}
		for i := range 8 {
			trs := l.feed(modCheck(modT0.Add(time.Duration(1+i)*time.Second), cells[10+i], c.opt), modZone)
			var ended []string
			for _, tr := range trs {
				if tr.State == modEnded {
					ended = append(ended, tr.Mod.EndReason)
				}
			}
			if i < 7 && len(ended) != 0 {
				t.Fatalf("%s: ended after %d checks, want 8", c.reason, i+1)
			}
			if i == 7 && (len(ended) != 1 || ended[0] != c.reason) {
				t.Fatalf("8th check: ended %v, want %s", ended, c.reason)
			}
		}
	}
}

func TestModifierZonesSeparate(t *testing.T) {
	const zone2 = "America/New_York"
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 3 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		trs = append(trs, l.feed(modCheck(ts, cellA, withCharged(494)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts, cellA, withCharged(494)), zone2)...)
	}
	if len(trs) != 2 || trs[0].Mod.Zone == trs[1].Mod.Zone {
		t.Fatalf("transitions %v, want one candidate per zone", states(trs))
	}

	l = newModifierLearner(defaultModifierParams())
	trs = feedAll(l, modZone,
		modCheck(modT0, cellA, withCharged(494)),
		modCheck(modT0.Add(time.Second), cellA, withCharged(494)))
	trs = append(trs, l.feed(modCheck(modT0.Add(2*time.Second), cellA), zone2)...)
	trs = append(trs, l.feed(modCheck(modT0.Add(3*time.Second), cellA, withCharged(494)), modZone)...)
	if len(trs) != 1 || trs[0].Mod.Zone != modZone {
		t.Fatalf("transitions %v, want a candidate in %s despite the agreement in %s", states(trs), modZone, zone2)
	}
}

// All-strong is not tracked (design §8): two strong species at +5 give two
// species-scope candidates and nothing else.
func TestModifierAllStrongNotTracked(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 3 {
		ts := modT0.Add(time.Duration(i) * time.Second)
		trs = append(trs, l.feed(modCheck(ts, cellA, withLevelDelta(5)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts, cellA, withLevelDelta(5), withSpecies(635, 2297)), modZone)...)
	}
	if len(trs) != 2 || l.trackCount() != 2 {
		t.Fatalf("transitions %v tracks %d, want two candidates on two tracks", states(trs), l.trackCount())
	}
	for _, tr := range trs {
		if m := tr.Mod; m.Type != modLevelOffset || m.Scope.AllStrong || m.Effect.Delta != 5 {
			t.Fatalf("candidate %v %+v %+v, want a species-scope +5", m.Type, m.Scope, m.Effect)
		}
	}
}

func TestModifierUnexplainedOnIVDisagree(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 3 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA,
			withIVsDisagree, withLevelDelta(5), withCharged(494)), modZone)...)
	}
	if len(trs) != 1 || trs[0].Mod.Type != modUnexplained {
		t.Fatalf("transitions %v, want one unexplained candidate", states(trs))
	}
	if n := l.trackCount(); n != 1 {
		t.Fatalf("tracks %d, want only the unexplained one", n)
	}
}

func TestModifierUnseenEndsAtLastSeen(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	if trs := l.sweep(modT0.Add(59 * time.Minute)); len(trs) != 0 {
		t.Fatalf("sweep at +59m: %v, want nothing", states(trs))
	}
	trs := l.sweep(modT0.Add(61 * time.Minute))
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" || !trs[0].At.Equal(modT0) {
		t.Fatalf("sweep at +61m: %+v, want ended unseen at t", trs)
	}

}

// TestModifierZoneWideAgesWithoutChecks: sightings show the species still
// spawns, not that the effect still holds. A zone-wide +5 whose zone record
// gets no check for unseen stops being zone-wide and, with no venue cell,
// ends; zone-wide then needs fresh checks.
func TestModifierZoneWideAgesWithoutChecks(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modSpreadCells(12)
	var trs []modifierTransition
	for i := range 10 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i-9)*time.Second), cells[i], withLevelDelta(5)), modZone)...)
	}
	if m := onlyActive(t, l); len(trs) != 2 || m.Status != modConfirmed || !m.ZoneWide || len(m.Cells) != 0 {
		t.Fatalf("transitions %v, want a confirmed zone-wide +5 with no venue cell", states(trs))
	}
	trs = nil
	for i := range 60 { // a sighting a minute, where it covers
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i+1)*time.Minute), cells[10], asSighting), modZone)...)
	}
	if len(trs) != 0 || !onlyActive(t, l).ZoneWide {
		t.Fatalf("60 minutes of sightings: %v, want still zone-wide", states(trs))
	}
	trs = l.feed(modCheck(modT0.Add(61*time.Minute), cells[10], asSighting), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" || trs[0].Mod.ZoneWide {
		t.Fatalf("61 minutes without a check: %v, want ended unseen, no longer zone-wide", states(trs))
	}

	// With a venue cell still matched, only the zone-wide coverage goes; the
	// old record does not count toward a new majority.
	l = newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	st := l.scopes[modScopeKey{modifierScope{PokemonID: 959, Form: 3272, Strong: true}, modZone}]
	st.rec[modDimCharged].last = modT0.Add(-2 * time.Hour).Unix() // as if its checks were two hours old
	if trs := l.sweep(modT0.Add(time.Minute)); len(trs) != 0 {
		t.Fatalf("sweep: %v, want no transition (venue cells keep the pin)", states(trs))
	}
	if m := onlyActive(t, l); m.ZoneWide || len(m.Cells) != 3 || m.Covers(modZone, unseenCell) {
		t.Fatalf("zone-wide %v cells %d, want the three venue cells only", m.ZoneWide, len(m.Cells))
	}
	l.feed(modCheck(modT0.Add(2*time.Minute), cellD, withCharged(494)), modZone)
	if onlyActive(t, l).ZoneWide {
		t.Fatal("one fresh match brought zone-wide back from the stale record")
	}
}

// A candidate that has not confirmed within unseen of its start ends
// unconfirmed, with its matches and venue counts; a new one forms from fresh
// deviations. Scattered noise cannot bank matches indefinitely.
func TestModifierCandidateEndsUnconfirmed(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 13 { // a mismatch every five minutes, each in another cell
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*5*time.Minute), modVenueCell14(cellA, i), withIVsDisagree), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("first hour: %v, want only the candidate", states(trs))
	}
	trs = l.feed(modCheck(modT0.Add(65*time.Minute), modVenueCell14(cellA, 13), withIVsDisagree), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unconfirmed" || trs[0].Mod.Observations != 13 {
		t.Fatalf("after unseen: %v, want the candidate ended unconfirmed with 13 observations", states(trs))
	}
	if len(l.active()) != 0 {
		t.Fatal("the mismatch that found the candidate ended must start a new cycle as pending")
	}
	trs = feedAll(l, modZone,
		modCheck(modT0.Add(70*time.Minute), modVenueCell14(cellA, 14), withIVsDisagree),
		modCheck(modT0.Add(75*time.Minute), modVenueCell14(cellA, 15), withIVsDisagree))
	if len(trs) != 1 || trs[0].State != modCandidate || trs[0].Mod.Observations != 3 {
		t.Fatalf("fresh deviations: %v, want a new candidate with 3 observations", states(trs))
	}
}

// TestModifierVenueCellExpires: a venue cell with no match for unseen is
// forgotten, whatever is sighted there (under sampled encounters a covered
// cell may never be checked again); a confirmed modifier left with no
// coverage ends.
func TestModifierVenueCellExpires(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	l.feed(modCheck(modT0.Add(30*time.Minute), cellA, asSighting), modZone)
	if trs := l.sweep(modT0.Add(59 * time.Minute)); len(trs) != 0 {
		t.Fatalf("sweep at +59m: %v, want nothing", states(trs))
	}
	trs := l.sweep(modT0.Add(61 * time.Minute))
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" {
		t.Fatalf("sweep at +61m: %v, want ended unseen", states(trs))
	}

	// A lone match older than unseen does not count toward covering a cell.
	l = newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	l.feed(modCheck(modT0.Add(time.Second), cellA2, withCharged(494)), modZone)
	for i := range 7 { // keep cell A matched: one match every 10 minutes
		l.feed(modCheck(modT0.Add(time.Duration(i+1)*10*time.Minute), cellA, withCharged(494)), modZone)
	}
	l.feed(modCheck(modT0.Add(71*time.Minute), cellA2, withCharged(494)), modZone)
	if m := onlyActive(t, l); m.Covers(modZone, cellA2) || !m.Covers(modZone, cellA) {
		t.Fatalf("cells %v, want cell A only: the two matches in the other cell are 71 minutes apart", m.Cells)
	}
}

func TestModifierSweepPrunesNoise(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	for i := range 5000 {
		ev := modCheck(modT0, cellA, withCharged(int32(1000+i%50)), withSpecies(int32(1+i/50), 0), withID(uint64(1+i)))
		if trs := l.feed(ev, modZone); len(trs) != 0 {
			t.Fatalf("noise made a transition: %v", states(trs))
		}
	}
	if n := l.trackCount(); n != 5000 {
		t.Fatalf("tracks %d before sweep, want 5000", n)
	}
	if len(l.scopes) != 100 || len(l.checked) != 5000 {
		t.Fatalf("scopes %d ids %d before sweep, want 100 and 5000", len(l.scopes), len(l.checked))
	}
	l.sweep(modT0.Add(31 * time.Minute))
	if n := l.trackCount(); n != 0 || len(l.scopes) != 0 {
		t.Fatalf("tracks %d scopes %d after sweep, want 0 (zone records pruned with them)", n, len(l.scopes))
	}
	if len(l.checked) != 5000 {
		t.Fatalf("ids %d after 31 minutes, want 5000 kept for unseen", len(l.checked))
	}
	l.sweep(modT0.Add(61 * time.Minute))
	if len(l.checked) != 0 {
		t.Fatalf("ids %d after unseen, want 0", len(l.checked))
	}
}

func TestModifierEventCompact(t *testing.T) {
	if s := unsafe.Sizeof(modifierEvent{}); s > 64 {
		t.Fatalf("modifierEvent is %d bytes, want <= 64", s)
	}
	l15 := s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5, -0.12)).Parent(15)
	ev := deriveEventCheck{
		Time:        modT0,
		EncounterID: 0xfeedface,
		PokemonID:   959, Form: 3272, Strong: true,
		CellID: int64(l15),
		Check: derivationCheck{
			Observed: encounter.Values{Level: 25, Size: 4},
			Derived:  encounter.Values{Level: 20, Size: 4},
			SizeRoll: 0.97,
			Move1:    200, Move2: 494,
		},
		Agree: dimAgreement{IVs: dimAgree, Level: dimDisagree, Charged: dimDisagree},
	}
	got := newModifierEvent(ev)
	if got.Cell != l15.Parent(14) {
		t.Fatalf("cell %v, want the level-14 parent %v", got.Cell, l15.Parent(14))
	}
	if got.Unix != modT0.Unix() || got.LevelDelta != 5 || got.ObsCharged != 494 || got.ObsSize != 4 ||
		got.SizeRoll != 0.97 || got.Agree != ev.Agree || !got.Strong || got.EncounterID != 0xfeedface {
		t.Fatalf("event %+v", got)
	}
	sighting := ev
	sighting.Sighting = true
	if id := newModifierEvent(sighting).EncounterID; id != 0 {
		t.Fatalf("sighting encounter id %d, want 0", id)
	}

	noCell := ev
	noCell.CellID, noCell.Lat, noCell.Lon = 0, 51.5, -0.12
	if c := newModifierEvent(noCell).Cell; c != l15.Parent(14) {
		t.Fatalf("lat/lon fallback cell %v, want %v", c, l15.Parent(14))
	}
	noCell.Lat, noCell.Lon = 0, 0
	if c := newModifierEvent(noCell).Cell; c != 0 {
		t.Fatalf("no location: cell %v, want 0", c)
	}

	var sink modifierEvent
	if n := testing.AllocsPerRun(100, func() { sink = newModifierEvent(ev) }); n != 0 {
		t.Fatalf("newModifierEvent allocates %v times", n)
	}
	_ = sink
}

// TestModifierRepeatedEncounterCountsOnce: the learner drops a check whose
// encounter id it has already checked, for unseen.
func TestModifierRepeatedEncounterCountsOnce(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 10 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withCharged(494), withID(77)), modZone)...)
	}
	if len(trs) != 0 || len(l.active()) != 0 {
		t.Fatalf("one spawn checked ten times: %v, want no candidate", states(trs))
	}
	trs = feedAll(l, modZone,
		modCheck(modT0.Add(10*time.Second), cellA, withCharged(494), withID(78)),
		modCheck(modT0.Add(11*time.Second), cellA, withCharged(494), withID(79)))
	if len(trs) != 1 || trs[0].State != modCandidate || trs[0].Mod.Observations != 3 {
		t.Fatalf("three spawns: %v, want a candidate with 3 observations", states(trs))
	}
	// Agreements repeat too: the repeated one does not disprove the cell.
	l.feed(modCheck(modT0.Add(12*time.Second), cellA, withID(77)), modZone)
	if m := onlyActive(t, l); !m.Covers(modZone, cellA) {
		t.Fatal("a repeated encounter's agreement removed the cell")
	}
	// Forgotten unseen after its first check (at t): the id counts again (as
	// a pending deviation: the candidate has ended unconfirmed by then).
	l.feed(modCheck(modT0.Add(time.Hour+5*time.Second), cellA, withCharged(494), withID(77)), modZone)
	for _, tr := range l.tracks {
		if len(tr.pending) != 1 {
			t.Fatalf("pending %d, want 1: id 77 counts again after unseen", len(tr.pending))
		}
	}
}

// TestModifierLearnerDirty: the learner reports a change that shrinks a
// confirmed modifier's lookup coverage (published at once) apart from one
// that grows it or is evidence only (both rate-limited), and an event that
// changes nothing reports nothing.
func TestModifierLearnerDirty(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	l.takeDirty()
	ts := modT0
	feed := func(cell s2.CellID, opts ...func(*modifierEvent)) {
		ts = ts.Add(time.Second)
		l.feed(modCheck(ts, cell, opts...), modZone)
	}
	check := func(what string, want modDirty) {
		t.Helper()
		if got := l.takeDirty(); got != want {
			t.Fatalf("%s: %+v, want %+v", what, got, want)
		}
	}
	evidence := modDirty{changed: true}
	growth := modDirty{changed: true, growth: true}
	shrink := modDirty{changed: true, shrink: true}
	feed(cellA3)
	check("agreement outside the venue", modDirty{})
	feed(cellA, withCharged(494))
	check("match in a covered cell", evidence)
	feed(cellA2, withCharged(494))
	check("first match in a new cell", evidence)
	feed(cellA2, withCharged(494))
	check("second match in a new cell", growth)
	feed(cellA3, withCharged(300))
	check("another effect outside the venue", modDirty{})
	feed(cellA2, withCharged(300))
	check("another effect in a covered cell", shrink)
	feed(cellA2)
	check("agreement in a cell no longer covered", modDirty{})

	l = newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellA2)
	l.takeDirty()
	ts = modT0
	feed(cellB, withCharged(494))
	if !onlyActive(t, l).ZoneWide {
		t.Fatal("a match under a second level-10 cell: want zone-wide")
	}
	check("zone-wide gained", growth)
	feed(cellE, withCharged(494))
	check("match in a new cell of a zone-wide modifier", evidence)
	feed(cellD)
	check("cell excluded from a zone-wide modifier", shrink)
	feed(cellD)
	check("repeated agreement in an excluded cell", modDirty{})
	feed(cellD, withCharged(494))
	check("excluded cell readmitted", growth)
	feed(cellA)
	check("covered cell removed", shrink)

	// A candidate's changes are evidence only: the index's lookup holds
	// confirmed modifiers alone.
	l = newModifierLearner(defaultModifierParams())
	ts = modT0
	feed(cellA, withCharged(494))
	feed(cellA, withCharged(494))
	feed(cellA, withCharged(494))
	l.takeDirty()
	feed(cellA2, withCharged(494))
	feed(cellA2, withCharged(494))
	check("candidate's new cell", evidence)
	feed(cellA2)
	check("candidate's cell removed", evidence)
}

// Every non-transition change bumps the modifier's version; an event that
// changes nothing leaves it.
func TestModifierLearnerVersion(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellA2)
	m := l.activeTracks[0].mod
	v := m.version
	l.feed(modCheck(modT0.Add(time.Second), cellA3), modZone)
	if m.version != v {
		t.Fatalf("an agreement outside the venue bumped the version %d → %d", v, m.version)
	}
	l.feed(modCheck(modT0.Add(2*time.Second), cellA, withCharged(494)), modZone)
	if m.version == v {
		t.Fatal("an observation left the version unchanged")
	}
	v = m.version
	l.feed(modCheck(modT0.Add(3*time.Second), cellA2), modZone)
	if m.version == v {
		t.Fatal("a removed cell left the version unchanged")
	}
}

func modifierWith(t *testing.T, l *modifierLearner, move int32) *derivationModifier {
	t.Helper()
	for _, m := range l.active() {
		if m.Effect.Move == move {
			return m
		}
	}
	t.Fatalf("pin %d not active", move)
	return nil
}

// TestModifierSupersededPinEnds: a confirmed pin whose cells start deviating
// to another move loses each cell at that cell's first such check, and ends
// as contradicted once the other move is half of the zone record, instead of
// masking it.
func TestModifierSupersededPinEnds(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := []s2.CellID{cellA, cellB, cellC}
	confirmPin(t, l, modZone, modT0, cells...)
	var ended, confirmed int
	for i := range 10 {
		trs := l.feed(modCheck(modT0.Add(time.Duration(1+i)*time.Second), cells[i%3], withCharged(300)), modZone)
		for _, tr := range trs {
			switch {
			case tr.State == modEnded && tr.Mod.Effect.Move == 494:
				if tr.Mod.EndReason != "contradicted" {
					t.Fatalf("494 ended %q, want contradicted", tr.Mod.EndReason)
				}
				ended = i + 1
			case tr.State == modConfirmed && tr.Mod.Effect.Move == 300:
				confirmed = i + 1
			}
		}
		if i == 2 {
			if m := modifierWith(t, l, 494); len(m.Cells) != 0 || !m.ZoneWide || m.Contradictions != 3 {
				t.Fatalf("494 after a 300 in each cell: cells %v zone-wide %v contradictions %d, want no venue cell, still zone-wide, 3",
					m.Cells, m.ZoneWide, m.Contradictions)
			}
		}
	}
	// 494 holds 8 of the record's 16 at the 8th 300.
	if ended != 8 || confirmed != 10 {
		t.Fatalf("494 ended at deviation %d, 300 confirmed at %d; want 8 and 10", ended, confirmed)
	}
	if m := onlyActive(t, l); m.Effect.Move != 300 || m.Status != modConfirmed {
		t.Fatalf("active %+v, want only the confirmed 300 pin", m.Effect)
	}

	// In one venue cell the first 300 takes the cell from the 494 pin at once.
	l = newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	trs := l.feed(modCheck(modT0.Add(time.Second), cellA, withCharged(300)), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "contradicted" {
		t.Fatalf("a 300 in the 494 pin's only cell: %v, want ended contradicted", states(trs))
	}
}

// TestModifierOtherEffectVoidsPendingCell: a pending deviation whose cell
// then shows another effect still counts toward the candidate, but not as a
// venue match: the cell is not covered.
func TestModifierOtherEffectVoidsPendingCell(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	trs := feedAll(l, modZone,
		modCheck(modT0, cellA, withCharged(494)),
		modCheck(modT0.Add(time.Second), cellA, withCharged(494)),
		modCheck(modT0.Add(2*time.Second), cellA, withCharged(300)),
		modCheck(modT0.Add(3*time.Second), cellA2, withCharged(494)))
	if len(trs) != 1 || trs[0].State != modCandidate || trs[0].Mod.Observations != 3 {
		t.Fatalf("transitions %v, want a 494 candidate with 3 observations", states(trs))
	}
	if m := trs[0].Mod; len(m.Cells) != 0 {
		t.Fatalf("cells %v, want none: cell A showed 300 after its two 494s", m.Cells)
	}
}

// TestModifierDisjointVenuesEvenRates: two venues pinning different moves,
// checked at exactly the same rate, each hold half of the zone record (an
// 8/8 tie), which is not more than half: neither goes zone-wide and each
// covers only its own cells. It pins the tie, not venues in general; see
// TestModifierDisjointVenuesUnevenRates.
func TestModifierDisjointVenuesEvenRates(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	venue1 := []s2.CellID{cellA, cellB, cellC}
	venue2 := []s2.CellID{cellD, cellE, cellF}
	for i := range 12 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		l.feed(modCheck(ts, venue1[i%3], withCharged(494)), modZone)
		l.feed(modCheck(ts.Add(time.Second), venue2[i%3], withCharged(777)), modZone)
	}
	p494, p777 := modifierWith(t, l, 494), modifierWith(t, l, 777)
	if p494.Status != modConfirmed || p777.Status != modConfirmed {
		t.Fatalf("statuses %v %v, want both confirmed", p494.Status, p777.Status)
	}
	if p494.ZoneWide || p777.ZoneWide {
		t.Fatalf("zone-wide 494=%v 777=%v, want both local", p494.ZoneWide, p777.ZoneWide)
	}
	for i := range 3 {
		if p494.Covers(modZone, venue2[i]) || p777.Covers(modZone, venue1[i]) {
			t.Fatal("a venue's pin covers the other venue's cell")
		}
		if !p494.Covers(modZone, venue1[i]) || !p777.Covers(modZone, venue2[i]) {
			t.Fatal("a venue's pin does not cover its own cell")
		}
	}
	if p494.Covers(modZone, unseenCell) || p777.Covers(modZone, unseenCell) {
		t.Fatal("a venue's pin covers a cell of neither venue")
	}
}

// TestModifierDisjointVenuesUnevenRates: when one venue is checked twice as
// often as the other and nothing else in the zone is checked, its pin is
// more than half of the zone record under two level-10 cells, so it goes
// zone-wide: the learner cannot tell it from a global event. It still never
// covers the other venue's cells (their latest checks show the other pin,
// so they are excluded), and the minority pin stays in its own cells.
func TestModifierDisjointVenuesUnevenRates(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	venue1 := []s2.CellID{cellA, cellB, cellC}
	venue2 := []s2.CellID{cellD, cellE, cellF}
	for i := range 12 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		l.feed(modCheck(ts, venue1[i%3], withCharged(494)), modZone)
		l.feed(modCheck(ts.Add(time.Second), venue1[(i+1)%3], withCharged(494)), modZone)
		l.feed(modCheck(ts.Add(2*time.Second), venue2[i%3], withCharged(777)), modZone)
	}
	p494, p777 := modifierWith(t, l, 494), modifierWith(t, l, 777)
	if p494.Status != modConfirmed || p777.Status != modConfirmed {
		t.Fatalf("statuses %v %v, want both confirmed", p494.Status, p777.Status)
	}
	if !p494.ZoneWide || p777.ZoneWide {
		t.Fatalf("zone-wide 494=%v 777=%v, want the majority pin zone-wide, the other local", p494.ZoneWide, p777.ZoneWide)
	}
	for i := range 3 {
		if p494.Covers(modZone, venue2[i]) || p777.Covers(modZone, venue1[i]) {
			t.Fatal("a venue's pin covers the other venue's cell")
		}
		if !p494.Covers(modZone, venue1[i]) || !p777.Covers(modZone, venue2[i]) {
			t.Fatal("a venue's pin does not cover its own cell")
		}
	}
	if !p494.Covers(modZone, unseenCell) || p777.Covers(modZone, unseenCell) {
		t.Fatal("want the zone-wide pin alone to cover a cell of neither venue")
	}
}

// TestModifierAgreementBeforeDeviationsNotCovered: a cell that agreed before
// any deviation is not covered by the venue pin that then forms beside it
// (ten matches in three cells of one level-10 cell).
func TestModifierAgreementBeforeDeviationsNotCovered(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	if trs := l.feed(modCheck(modT0.Add(-time.Minute), cellD), modZone); len(trs) != 0 {
		t.Fatalf("agreement: %v", states(trs))
	}
	confirmPin(t, l, modZone, modT0, cellA, cellA2, cellA3)
	m := onlyActive(t, l)
	if m.ZoneWide || m.Covers(modZone, cellD) || m.Covers(modZone, unseenCell) {
		t.Fatalf("zone-wide %v, covers D %v: want cells A only", m.ZoneWide, m.Covers(modZone, cellD))
	}
	if len(m.Cells) != 3 || !m.Covers(modZone, cellA) {
		t.Fatalf("cells %v, want the three venue cells", m.Cells)
	}
	// The same before a global event: the modifier goes zone-wide, less the
	// cell whose latest check agreed.
	l = newModifierLearner(defaultModifierParams())
	l.feed(modCheck(modT0.Add(-time.Minute), cellD), modZone)
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	if m := onlyActive(t, l); !m.ZoneWide || m.Covers(modZone, cellD) || !m.Covers(modZone, unseenCell) {
		t.Fatalf("zone-wide %v excluded %v, want zone-wide less cellD", m.ZoneWide, m.Excluded)
	}
}

// TestModifierVenueCellEndsAlone: a base result in one covered cell removes
// that cell at once and leaves the others; matches elsewhere do not keep it.
func TestModifierVenueCellEndsAlone(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellA2)
	if trs := l.feed(modCheck(modT0.Add(time.Second), cellA), modZone); len(trs) != 0 {
		t.Fatalf("agreement in cell A: %v, want no transition", states(trs))
	}
	if m := onlyActive(t, l); m.Covers(modZone, cellA) || !m.Covers(modZone, cellA2) || m.Status != modConfirmed {
		t.Fatalf("cells %v status %v, want the other cell only, still confirmed", m.Cells, m.Status)
	}
	// Matches in the other cell do not bring cell A back; it needs two of
	// its own.
	for i := range 5 {
		l.feed(modCheck(modT0.Add(time.Duration(2+i)*time.Second), cellA2, withCharged(494)), modZone)
	}
	if onlyActive(t, l).Covers(modZone, cellA) {
		t.Fatal("matches in another cell covered the disproven cell A")
	}
	l.feed(modCheck(modT0.Add(10*time.Second), cellA, withCharged(494)), modZone)
	if onlyActive(t, l).Covers(modZone, cellA) {
		t.Fatal("one match covered the disproven cell A")
	}
	l.feed(modCheck(modT0.Add(11*time.Second), cellA, withCharged(494)), modZone)
	if !onlyActive(t, l).Covers(modZone, cellA) {
		t.Fatal("two fresh matches did not cover cell A again")
	}
	trs := feedAll(l, modZone, modCheck(modT0.Add(12*time.Second), cellA), modCheck(modT0.Add(13*time.Second), cellA2))
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "agreement" {
		t.Fatalf("agreements in both cells: %v, want ended by agreement", states(trs))
	}
}

func TestModifierZoneWideNeedsTwoLevel10Parents(t *testing.T) {
	for _, c := range []s2.CellID{cellA2, cellA3} {
		if c.Parent(10) != cellA.Parent(10) || c == cellA {
			t.Fatal("test cells must be distinct and share a level-10 parent")
		}
	}
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellA2, cellA3)
	if m := onlyActive(t, l); m.ZoneWide || m.Covers(modZone, unseenCell) {
		t.Fatal("three cells under one level-10 cell went zone-wide")
	}

	if cellA.Parent(10) == cellB.Parent(10) {
		t.Fatal("cellA and cellB must lie under different level-10 cells")
	}
	l = newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellA2, cellB)
	if m := onlyActive(t, l); !m.ZoneWide {
		t.Fatal("matches under two level-10 cells and nothing else in the record: not zone-wide")
	}
}

func TestModifierZoneUnknownNeverZoneWide(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, "", modT0, cellA, cellB, cellC, cellD, cellE)
	if m := onlyActive(t, l); m.ZoneWide || m.Covers("", unseenCell) || !m.Covers("", cellA) {
		t.Fatal("a modifier in the unknown zone must cover its venue cells only")
	}
}

// TestModifierNoiseNeverConfirmsUnexplained: scattered IV mismatches, one
// per five agreements in the same cells, never confirm unexplained.
func TestModifierNoiseNeverConfirmsUnexplained(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := make([]s2.CellID, 10)
	for i := range cells {
		cells[i] = modTestCell(51.40+0.02*float64(i), -0.20+0.01*float64(i))
	}
	ts := modT0
	next := func() time.Time { ts = ts.Add(2 * time.Second); return ts }
	for k := range 60 {
		var trs []modifierTransition
		for j := range 3 {
			trs = append(trs, l.feed(modCheck(next(), cells[(3*k+j)%10], withIVsDisagree), modZone)...)
		}
		for j := range 15 {
			trs = append(trs, l.feed(modCheck(next(), cells[(k+j)%10]), modZone)...)
		}
		for _, tr := range trs {
			if tr.State == modConfirmed {
				t.Fatalf("round %d: noise confirmed %v", k, tr.Mod.Type)
			}
		}
	}
}

// TestModifierNoiseDoubleHitDoesNotConfirm: scattered mismatches that happen
// to hit one cell twice cover that cell but do not confirm: confirming on
// venue cells needs confirm matches inside covered cells.
func TestModifierNoiseDoubleHitDoesNotConfirm(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 12 { // twelve cells of one level-10 cell: never zone-wide
		ts := modT0.Add(time.Duration(i) * time.Minute)
		trs = append(trs, l.feed(modCheck(ts, modVenueCell14(cellA, i), withIVsDisagree), modZone)...)
	}
	trs = append(trs, l.feed(modCheck(modT0.Add(12*time.Minute), cellA, withIVsDisagree), modZone)...)
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	if m := onlyActive(t, l); m.Status != modCandidate || m.Observations != 13 || len(m.Cells) != 1 {
		t.Fatalf("status %v observations %d cells %v, want a candidate with 13 and one covered cell",
			m.Status, m.Observations, m.Cells)
	}
}

// TestModifierPinEqualAgreementNeutral: a check whose derived move is the
// pinned move agrees with both: it neither removes a cell, excludes one, nor
// counts against the pin in the zone record.
func TestModifierPinEqualAgreementNeutral(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	pinEqual := func(ev *modifierEvent) { ev.ObsCharged = 494 }
	var trs []modifierTransition
	trs = append(trs, l.feed(modCheck(modT0.Add(time.Second), cellD, pinEqual), modZone)...)
	for i := range 10 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(2+i)*time.Second), cellA, pinEqual), modZone)...)
	}
	if len(trs) != 0 {
		t.Fatalf("transitions %v, want none", states(trs))
	}
	if m := onlyActive(t, l); len(m.Excluded) != 0 || !m.ZoneWide || len(m.Cells) != 3 {
		t.Fatalf("excluded %v zone-wide %v cells %d, want none excluded, zone-wide, 3 cells", m.Excluded, m.ZoneWide, len(m.Cells))
	}
}

// TestModifierMinorityMatchesNeverConfirm: matches that are each followed
// by agreements in their cell never cover it, so never confirm.
func TestModifierMinorityMatchesNeverConfirm(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	ts := modT0
	next := func() time.Time { ts = ts.Add(time.Second); return ts }
	trs := feedAll(l, modZone,
		modCheck(next(), cellA, withCharged(494)),
		modCheck(next(), cellA, withCharged(494)),
		modCheck(next(), cellA, withCharged(494)))
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want a candidate", states(trs))
	}
	for range 10 {
		trs = append(trs[:0], l.feed(modCheck(next(), cellA, withCharged(494)), modZone)...)
		for range 7 {
			trs = append(trs, l.feed(modCheck(next(), cellA), modZone)...)
		}
		if len(trs) != 0 {
			t.Fatalf("transitions %v, want none", states(trs))
		}
	}
	if m := onlyActive(t, l); m.Status != modCandidate || m.Observations != 13 || len(m.Cells) != 0 {
		t.Fatalf("status %v observations %d cells %v, want a candidate with 13 and no cell", m.Status, m.Observations, m.Cells)
	}
}

// TestModifierCellZeroMatchesDoNotConfirm: only matches with a cell count
// toward confirm.
func TestModifierCellZeroMatchesDoNotConfirm(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	ts := modT0
	next := func() time.Time { ts = ts.Add(time.Second); return ts }
	var trs []modifierTransition
	for range 3 {
		trs = append(trs, l.feed(modCheck(next(), cellA, withCharged(494)), modZone)...)
	}
	for range 7 {
		trs = append(trs, l.feed(modCheck(next(), 0, withCharged(494)), modZone)...)
	}
	if len(trs) != 1 {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	if m := onlyActive(t, l); m.Status != modCandidate || m.Observations != 10 {
		t.Fatalf("status %v observations %d, want a candidate with 10", m.Status, m.Observations)
	}
	for i := range 7 {
		trs = l.feed(modCheck(next(), cellA, withCharged(494)), modZone)
		if want := i == 6; want != (len(trs) == 1 && trs[0].State == modConfirmed) {
			t.Fatalf("located match %d: %v", 4+i, states(trs))
		}
	}
}

// Growth that can reduce what is applied is reported as shrink: an
// unexplained modifier's, and a pin's newly covered cell while another
// confirmed pin of its slot exists in its scope and zone.
func TestModifierLearnerDirtyGrowthAsShrink(t *testing.T) {
	shrink := modDirty{changed: true, shrink: true}
	l := newModifierLearner(defaultModifierParams())
	for i := range 10 {
		l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withIVsDisagree), modZone)
	}
	if m := onlyActive(t, l); m.Status != modConfirmed || m.Type != modUnexplained {
		t.Fatalf("modifier %+v, want confirmed unexplained", m)
	}
	l.takeDirty()
	l.feed(modCheck(modT0.Add(10*time.Second), cellA2, withIVsDisagree), modZone)
	l.feed(modCheck(modT0.Add(11*time.Second), cellA2, withIVsDisagree), modZone)
	if got := l.takeDirty(); got != shrink {
		t.Fatalf("unexplained newly covered cell: %+v, want %+v", got, shrink)
	}

	l = newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	for i := range 10 {
		l.feed(modCheck(modT0.Add(time.Duration(10+i)*time.Second), cellA2, withCharged(300)), modZone)
	}
	if modifierWith(t, l, 300).Status != modConfirmed || modifierWith(t, l, 494).Status != modConfirmed {
		t.Fatal("want both pins confirmed")
	}
	l.takeDirty()
	l.feed(modCheck(modT0.Add(30*time.Second), cellA3, withCharged(494)), modZone)
	l.feed(modCheck(modT0.Add(31*time.Second), cellA3, withCharged(494)), modZone)
	if got := l.takeDirty(); got != shrink {
		t.Fatalf("pin newly covered cell with a confirmed peer: %+v, want %+v", got, shrink)
	}
}
