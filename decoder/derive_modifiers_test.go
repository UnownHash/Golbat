package decoder

import (
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
	p := c.Parent(10)
	for range n {
		if c = c.Next(); c == p.ChildEndAtLevel(deriveEventsCellLevel) {
			c = p.ChildBeginAtLevel(deriveEventsCellLevel)
		}
	}
	return c
}

// modSpreadCells returns n cells ~55 km apart: eight or more are spread.
func modSpreadCells(n int) []s2.CellID {
	out := make([]s2.CellID, n)
	for i := range out {
		out[i] = modTestCell(48.0+0.5*float64(i), 2.0)
	}
	return out
}

// modWideCells returns n cells of modZone ~5 km apart on a line east of the
// named cells and distinct from them: eight or more are spread.
func modWideCells(n int) []s2.CellID {
	out := make([]s2.CellID, n)
	for i := range out {
		out[i] = modTestCell(51.30+0.045*float64(i), -0.02)
	}
	return out
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
	if m.ZoneWide || m.Covers(modZone, cellA) {
		t.Fatal("a candidate from one cell covers something")
	}

	// Seven more across the zone: ten matches from eight cells, spread.
	trs = nil
	wide := modWideCells(7)
	for i := range 7 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Minute+time.Duration(i)*time.Second), wide[i], withCharged(494)), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modConfirmed || trs[0].Mod.Observations != 10 {
		t.Fatalf("after 10 deviations: %v, want confirmed with 10 observations", states(trs))
	}
	if m := trs[0].Mod; !m.ZoneWide || !m.Covers(modZone, cellA) || !m.Covers(modZone, unseenCell) {
		t.Fatalf("zone-wide %v, want the zone covered", m.ZoneWide)
	}

	// The base move in half of the record ends it.
	for i := range 8 {
		trs = l.feed(modCheck(modT0.Add(2*time.Minute+time.Duration(i)*time.Second), modVenueCell14(cellD, i)), modZone)
		if i < 7 && len(trs) != 0 {
			t.Fatalf("agreement %d: %v, want no transition", i+1, states(trs))
		}
	}
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "agreement" {
		t.Fatalf("after 8 agreements: %v, want ended by agreement", states(trs))
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

// A ticketed venue: its cells deviate while the rest of the zone agrees. It
// is listed with its cells, and covers nothing: a candidate.
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
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	m := onlyActive(t, l)
	if m.ZoneWide {
		t.Fatal("venue modifier went zone-wide")
	}
	if len(m.Cells) != 3 {
		t.Fatalf("cells %v, want the venue cells A, B and C listed", m.Cells)
	}
	for _, c := range []s2.CellID{cellA, cellB, cellC, cellD, unseenCell, 0} {
		if m.Covers(modZone, c) {
			t.Fatalf("venue modifier covers cell %v", c)
		}
	}
}

func TestModifierGlobalGoesZoneWide(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
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

// Zone-wide coverage needs the effect in more than half of the zone
// record's fresh entries and at least confirm of them.
func TestModifierZoneWideNeedsRecordMajority(t *testing.T) {
	spread := modSpreadCells(16)
	rec := func(matches, agreements int) *modRecord {
		var r modRecord
		for i := range matches {
			r.push(spread[i], 494, false, 100)
		}
		for i := range agreements {
			r.push(spread[matches+i], 268, true, 100)
		}
		return &r
	}
	for _, c := range []struct {
		matches, agreements int
		on                  bool
	}{{9, 0, false}, {10, 0, true}, {8, 2, true}, {5, 5, false}, {9, 7, true}, {8, 8, false}, {16, 0, true}} {
		if got := rec(c.matches, c.agreements).judge(494, true, 10, 100).on; got != c.on {
			t.Errorf("%d matches, %d agreements: zone-wide %v, want %v", c.matches, c.agreements, got, c.on)
		}
	}
	// Entries before the cut do not count: six aged matches leave ten fresh
	// entries, four of them matches.
	r := rec(10, 0)
	for i := range 4 {
		r.buf[i].unix = 99
	}
	if v := r.judge(494, true, 10, 100); v.on || !v.thin {
		t.Errorf("six fresh matches of ten entries: %+v, want off and thin", v)
	}
	r = rec(6, 0)
	for i := range 6 {
		r.buf[i].unix = 99
	}
	for i := range 4 {
		r.push(spread[6+i], 494, false, 100)
	}
	for i := range 6 {
		r.push(spread[10+i], 268, true, 100)
	}
	if v := r.judge(494, true, 10, 100); v.on || v.thin || v.lost() != "agreement" {
		t.Errorf("four fresh matches and six fresh agreements: %+v, want off by agreement", v)
	}
	if v := r.judge(494, true, 10, 101); !v.thin || v.lost() != "unseen" {
		t.Errorf("every entry aged: %+v, want thin", v)
	}
}

// TestModifierZoneWideExcludesDisprovenCell: a cell whose latest check did
// not show the effect — it agreed, or showed another effect — is excluded
// from a zone-wide modifier, and readmitted by a match there.
func TestModifierZoneWideExcludesDisprovenCell(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
	trs := feedAll(l, modZone,
		modCheck(modT0.Add(time.Second), cellD),
		modCheck(modT0.Add(2*time.Second), cellE, withCharged(300)))
	if len(trs) != 0 {
		t.Fatalf("an agreement in cell D, a 300 in cell E: %v, want no transition", states(trs))
	}
	m := modifierWith(t, l, 494)
	if _, d := m.Excluded[cellD]; !m.ZoneWide || len(m.Excluded) != 2 || !d {
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
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
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
// gets no check for unseen stops being zone-wide and ends; a new one needs
// fresh checks.
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
	for i := range 59 { // a sighting a minute, where it covers
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i+1)*time.Minute), cells[10], asSighting), modZone)...)
	}
	if len(trs) != 0 || !onlyActive(t, l).ZoneWide {
		t.Fatalf("59 minutes of sightings: %v, want still zone-wide", states(trs))
	}
	trs = l.feed(modCheck(modT0.Add(61*time.Minute), cells[10], asSighting), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" || trs[0].Mod.ZoneWide {
		t.Fatalf("61 minutes without a check: %v, want ended unseen, no longer zone-wide", states(trs))
	}
	// The aged record does not count toward a new majority.
	for i := range 3 {
		l.feed(modCheck(modT0.Add(62*time.Minute+time.Duration(i)*time.Second), cells[i], withLevelDelta(5)), modZone)
	}
	if m := onlyActive(t, l); m.Status != modCandidate || m.ZoneWide {
		t.Fatalf("three fresh matches: status %v zone-wide %v, want a candidate, not zone-wide", m.Status, m.ZoneWide)
	}
}

// TestModifierVenueCellExpires: a venue cell with no match for unseen is
// forgotten, and a venue-only candidate with no match for unseen ends.
func TestModifierVenueCellExpires(t *testing.T) {
	// A lone match older than unseen does not count toward listing a cell.
	l := newModifierLearner(defaultModifierParams())
	for i := range 3 {
		l.feed(modCheck(modT0.Add(time.Duration(i-2)*time.Second), cellA, withCharged(494)), modZone)
	}
	l.feed(modCheck(modT0.Add(time.Second), cellA2, withCharged(494)), modZone)
	l.feed(modCheck(modT0.Add(2*time.Second), cellA3, withCharged(494)), modZone)
	l.feed(modCheck(modT0.Add(3*time.Second), cellA3, withCharged(494)), modZone)
	if m := onlyActive(t, l); len(m.Cells) != 2 {
		t.Fatalf("cells %v, want cells A and A3 listed", m.Cells)
	}
	for i := range 7 { // keep cell A matched: one match every 10 minutes
		l.feed(modCheck(modT0.Add(time.Duration(i+1)*10*time.Minute), cellA, withCharged(494)), modZone)
	}
	l.feed(modCheck(modT0.Add(71*time.Minute), cellA2, withCharged(494)), modZone)
	m := onlyActive(t, l)
	if _, a := m.Cells[cellA]; !a || len(m.Cells) != 1 {
		t.Fatalf("cells %v, want cell A only: cell A3 had no match for unseen, cell A2's two are 71 minutes apart", m.Cells)
	}

	trs := l.sweep(modT0.Add(132 * time.Minute))
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "unseen" {
		t.Fatalf("sweep 61 minutes after the last match: %v, want the candidate ended unseen", states(trs))
	}
}

func TestModifierSweepPrunesNoise(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	for i := range 5000 {
		ev := modCheck(modT0, cellA, withCharged(int32(1000+i%50)), withSpecies(int32(1+i/50), 0))
		if trs := l.feed(ev, modZone); len(trs) != 0 {
			t.Fatalf("noise made a transition: %v", states(trs))
		}
	}
	if n := l.trackCount(); n != 5000 {
		t.Fatalf("tracks %d before sweep, want 5000", n)
	}
	if len(l.scopes) != 100 {
		t.Fatalf("scopes %d before sweep, want 100", len(l.scopes))
	}
	l.sweep(modT0.Add(31 * time.Minute))
	if n := l.trackCount(); n != 0 || len(l.scopes) != 0 {
		t.Fatalf("tracks %d scopes %d after sweep, want 0 (zone records pruned with them)", n, len(l.scopes))
	}
}

func TestModifierEventCompact(t *testing.T) {
	if s := unsafe.Sizeof(modifierEvent{}); s > 64 {
		t.Fatalf("modifierEvent is %d bytes, want <= 64", s)
	}
	l15 := s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5, -0.12)).Parent(15)
	ev := deriveEventCheck{
		Time:        modT0,
		EncounterID: 0xfeedface, // not the learner's business
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
		got.SizeRoll != 0.97 || got.Agree != ev.Agree || !got.Strong || got.Sighting {
		t.Fatalf("event %+v", got)
	}
	sighting := ev
	sighting.Sighting = true
	if !newModifierEvent(sighting).Sighting {
		t.Fatal("sighting not marked")
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

// TestModifierLearnerDirty: the learner reports a change that shrinks a
// confirmed modifier's lookup coverage (published at once) apart from one
// that grows it or is evidence only (both rate-limited), and an event that
// changes nothing reports nothing.
func TestModifierLearnerDirty(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
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
	feed(cellA, withCharged(494))
	check("match in a new cell", evidence)
	feed(cellA, withCharged(494))
	check("second match: a venue cell listed", evidence)
	feed(cellD)
	check("cell excluded", shrink)
	feed(cellD)
	check("repeated agreement in an excluded cell", modDirty{})
	feed(cellD, withCharged(300))
	check("another effect in an excluded cell", modDirty{})
	feed(cellD, withCharged(494))
	check("excluded cell readmitted", growth)
	feed(cellE, withCharged(300))
	check("another effect in a covered cell", shrink)
	feed(cellA)
	check("agreement in a listed venue cell", shrink)
	if m := modifierWith(t, l, 494); len(m.Cells) != 0 || len(m.Excluded) != 2 {
		t.Fatalf("cells %v excluded %v, want no venue cell and cells A and E excluded", m.Cells, m.Excluded)
	}

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
	feed(cellA3)
	check("candidate's excluded cell", modDirty{})
}

// Every non-transition change bumps the modifier's version; an event that
// changes nothing leaves it.
func TestModifierLearnerVersion(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
	m := l.activeTracks[0].mod
	v := m.version
	l.feed(modCheck(modT0.Add(time.Second), cellD), modZone)
	if m.version == v {
		t.Fatal("an excluded cell left the version unchanged")
	}
	v = m.version
	l.feed(modCheck(modT0.Add(2*time.Second), cellD), modZone)
	if m.version != v {
		t.Fatalf("a repeated agreement in an excluded cell bumped the version %d → %d", v, m.version)
	}
	l.feed(modCheck(modT0.Add(3*time.Second), cellA, withCharged(494)), modZone)
	if m.version == v {
		t.Fatal("an observation left the version unchanged")
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

// TestModifierSupersededPinEnds: a confirmed pin whose zone starts deviating
// to another move loses each cell at that cell's first such check, and ends
// as contradicted once the other move is half of the zone record, instead of
// masking it.
func TestModifierSupersededPinEnds(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modWideCells(10)
	confirmPin(t, l, modZone, modT0, cells...)
	var ended, confirmed int
	for i := range 10 {
		trs := l.feed(modCheck(modT0.Add(time.Duration(1+i)*time.Second), cells[i], withCharged(300)), modZone)
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
			m := modifierWith(t, l, 494)
			if !m.ZoneWide || m.Contradictions != 3 || len(m.Excluded) != 3 || m.Covers(modZone, cells[0]) || !m.Covers(modZone, cells[3]) {
				t.Fatalf("494 after a 300 in three cells: zone-wide %v contradictions %d excluded %v, want still zone-wide, 3, those three cells",
					m.ZoneWide, m.Contradictions, m.Excluded)
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

// TestModifierDisjointVenues: two venues pinning different moves, one
// checked twice as often as the other and nothing else in the zone checked.
// The busier pin is more than half of the zone record, but from three
// cells: neither is spread, so neither goes zone-wide or covers anything.
// Each lists its own cells.
func TestModifierDisjointVenues(t *testing.T) {
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
	if p494.Status != modCandidate || p777.Status != modCandidate {
		t.Fatalf("statuses %v %v, want both candidates", p494.Status, p777.Status)
	}
	if p494.ZoneWide || p777.ZoneWide {
		t.Fatalf("zone-wide 494=%v 777=%v, want neither", p494.ZoneWide, p777.ZoneWide)
	}
	for i := range 3 {
		_, in1 := p494.Cells[venue1[i]]
		_, in2 := p777.Cells[venue2[i]]
		if !in1 || !in2 || len(p494.Cells) != 3 || len(p777.Cells) != 3 {
			t.Fatalf("cells %v and %v, want each venue's own three", p494.Cells, p777.Cells)
		}
		if p494.Covers(modZone, venue1[i]) || p777.Covers(modZone, venue2[i]) {
			t.Fatal("a venue's pin covers a cell")
		}
	}
}

// TestModifierAgreementBeforeDeviationsNotCovered: a cell that agreed
// before any deviation is excluded from the zone-wide pin that then forms.
func TestModifierAgreementBeforeDeviationsNotCovered(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	if trs := l.feed(modCheck(modT0.Add(-time.Minute), cellD), modZone); len(trs) != 0 {
		t.Fatalf("agreement: %v", states(trs))
	}
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
	if m := onlyActive(t, l); !m.ZoneWide || m.Covers(modZone, cellD) || !m.Covers(modZone, unseenCell) {
		t.Fatalf("zone-wide %v excluded %v, want zone-wide less cellD", m.ZoneWide, m.Excluded)
	}
}

// TestModifierVenueCellEndsAlone: a base result in one listed venue cell
// removes that cell at once and leaves the others; matches elsewhere do not
// bring it back.
func TestModifierVenueCellEndsAlone(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	for i := range 6 {
		l.feed(modCheck(modT0.Add(time.Duration(i-5)*time.Second), []s2.CellID{cellA, cellA2}[i%2], withCharged(494)), modZone)
	}
	listed := func(c s2.CellID) bool {
		_, ok := onlyActive(t, l).Cells[c]
		return ok
	}
	if !listed(cellA) || !listed(cellA2) {
		t.Fatalf("cells %v, want cells A and A2 listed", onlyActive(t, l).Cells)
	}
	if trs := l.feed(modCheck(modT0.Add(time.Second), cellA), modZone); len(trs) != 0 {
		t.Fatalf("agreement in cell A: %v, want no transition", states(trs))
	}
	if listed(cellA) || !listed(cellA2) {
		t.Fatalf("cells %v, want the other cell only", onlyActive(t, l).Cells)
	}
	// Matches in the other cell do not bring cell A back; it needs two of
	// its own.
	for i := range 5 {
		l.feed(modCheck(modT0.Add(time.Duration(2+i)*time.Second), cellA2, withCharged(494)), modZone)
	}
	if listed(cellA) {
		t.Fatal("matches in another cell listed the disproven cell A")
	}
	l.feed(modCheck(modT0.Add(10*time.Second), cellA, withCharged(494)), modZone)
	if listed(cellA) {
		t.Fatal("one match listed the disproven cell A")
	}
	l.feed(modCheck(modT0.Add(11*time.Second), cellA, withCharged(494)), modZone)
	if !listed(cellA) {
		t.Fatal("two fresh matches did not list cell A again")
	}
}

func TestModifierZoneUnknownNeverZoneWide(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	cells := modWideCells(10)
	var trs []modifierTransition
	for i := range 16 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cells[i%10], withCharged(494)), "")...)
	}
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	if m := onlyActive(t, l); m.ZoneWide || m.Covers("", unseenCell) || m.Covers("", cells[0]) {
		t.Fatal("a modifier in the unknown zone must cover nothing")
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
// to hit one cell twice list that cell but do not confirm.
func TestModifierNoiseDoubleHitDoesNotConfirm(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 12 { // twelve neighbouring cells: never spread
		ts := modT0.Add(time.Duration(i) * time.Minute)
		trs = append(trs, l.feed(modCheck(ts, modVenueCell14(cellA, i), withIVsDisagree), modZone)...)
	}
	trs = append(trs, l.feed(modCheck(modT0.Add(12*time.Minute), cellA, withIVsDisagree), modZone)...)
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("transitions %v, want only the candidate", states(trs))
	}
	if m := onlyActive(t, l); m.Status != modCandidate || m.Observations != 13 || len(m.Cells) != 1 {
		t.Fatalf("status %v observations %d cells %v, want a candidate with 13 and one listed cell",
			m.Status, m.Observations, m.Cells)
	}
}

// TestModifierPinEqualAgreementNeutral: a check whose derived move is the
// pinned move agrees with both: it neither excludes its cell nor counts
// against the pin in the zone record.
func TestModifierPinEqualAgreementNeutral(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, modWideCells(10)...)
	pinEqual := func(ev *modifierEvent) { ev.ObsCharged = 494 }
	var trs []modifierTransition
	trs = append(trs, l.feed(modCheck(modT0.Add(time.Second), cellD, pinEqual), modZone)...)
	for i := range 10 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(2+i)*time.Second), cellA, pinEqual), modZone)...)
	}
	if len(trs) != 0 {
		t.Fatalf("transitions %v, want none", states(trs))
	}
	if m := onlyActive(t, l); len(m.Excluded) != 0 || !m.ZoneWide {
		t.Fatalf("excluded %v zone-wide %v, want none excluded, zone-wide", m.Excluded, m.ZoneWide)
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
	cells := modWideCells(10)
	ts := modT0
	next := func() time.Time { ts = ts.Add(time.Second); return ts }
	var trs []modifierTransition
	for i := range 3 {
		trs = append(trs, l.feed(modCheck(next(), cells[i], withCharged(494)), modZone)...)
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
		trs = l.feed(modCheck(next(), cells[3+i], withCharged(494)), modZone)
		if want := i == 6; want != (len(trs) == 1 && trs[0].State == modConfirmed) {
			t.Fatalf("located match %d: %v", 4+i, states(trs))
		}
	}
}

// Growth that can reduce what is applied is reported as shrink: an
// unexplained modifier's (wider suppression).
func TestModifierLearnerDirtyGrowthAsShrink(t *testing.T) {
	shrink := modDirty{changed: true, shrink: true}
	l := newModifierLearner(defaultModifierParams())
	cells := modWideCells(10)
	for i := range 10 {
		l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cells[i], withIVsDisagree), modZone)
	}
	if m := onlyActive(t, l); m.Status != modConfirmed || m.Type != modUnexplained || !m.ZoneWide {
		t.Fatalf("modifier %+v, want confirmed zone-wide unexplained", m)
	}
	l.feed(modCheck(modT0.Add(10*time.Second), cellD), modZone)
	if got := l.takeDirty(); got != shrink || onlyActive(t, l).Covers(modZone, cellD) {
		t.Fatalf("agreement in cell D: %+v, want %+v and the cell excluded", got, shrink)
	}
	l.feed(modCheck(modT0.Add(11*time.Second), cellD, withIVsDisagree), modZone)
	if got := l.takeDirty(); got != shrink || !onlyActive(t, l).Covers(modZone, cellD) {
		t.Fatalf("unexplained readmitted cell: %+v, want %+v and the cell covered", got, shrink)
	}
}
