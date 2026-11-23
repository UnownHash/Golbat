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
)

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

	trs = nil
	for i := range 20 {
		trs = append(trs, l.feed(modCheck(modT0.Add(2*time.Minute+time.Duration(i)*time.Second), cellA), modZone)...)
	}
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "agreement" {
		t.Fatalf("after 20 agreements: %v, want ended by agreement", states(trs))
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

func TestModifierVenueStaysLocal(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	cells := []s2.CellID{cellA, cellB, cellC}
	for i := range 10 {
		ts := modT0.Add(time.Duration(i) * 10 * time.Second)
		trs = append(trs, l.feed(modCheck(ts, cells[i%3], withCharged(494)), modZone)...)
		trs = append(trs, l.feed(modCheck(ts.Add(time.Second), cellD), modZone)...)
	}
	if len(trs) != 2 || trs[1].State != modConfirmed {
		t.Fatalf("transitions %v, want candidate then confirmed", states(trs))
	}
	m := onlyActive(t, l)
	if m.ZoneWide(3) {
		t.Fatal("venue modifier went zone-wide")
	}
	if !m.Covers(modZone, cellA, 3) {
		t.Fatal("does not cover in-cell A")
	}
	if m.Covers(modZone, cellD, 3) {
		t.Fatal("covers the agreeing cell D")
	}
	if m.Covers(modZone, unseenCell, 3) {
		t.Fatal("local modifier covers an unseen cell")
	}
	if _, ok := m.Outside[cellD]; !ok || len(m.InCells) != 3 {
		t.Fatalf("in-cells %d, outside %v", len(m.InCells), m.Outside)
	}
}

func TestModifierGlobalGoesZoneWide(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	m := onlyActive(t, l)
	if !m.ZoneWide(3) {
		t.Fatalf("not zone-wide: in-cells %d outside %d", len(m.InCells), len(m.Outside))
	}
	if !m.Covers(modZone, unseenCell, 3) {
		t.Fatal("zone-wide modifier does not cover an unseen cell of its zone")
	}
	if m.Covers("America/New_York", cellA, 3) {
		t.Fatal("covers another zone")
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

func TestModifierAllStrongLevelNeedsTwoSpecies(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	var trs []modifierTransition
	for i := range 3 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withLevelDelta(5)), modZone)...)
	}
	if len(trs) != 1 || trs[0].Mod.Type != modLevelOffset || trs[0].Mod.Scope.AllStrong {
		t.Fatalf("one species: %v, want only a species-scope level candidate", states(trs))
	}
	if trs[0].Mod.Effect.Delta != 5 {
		t.Fatalf("delta %d, want 5", trs[0].Mod.Effect.Delta)
	}
	trs = l.feed(modCheck(modT0.Add(3*time.Second), cellA, withLevelDelta(5), withSpecies(635, 2297)), modZone)
	if len(trs) != 1 || trs[0].State != modCandidate {
		t.Fatalf("second species: %v, want one candidate", states(trs))
	}
	m := trs[0].Mod
	if want := (modifierScope{Strong: true, AllStrong: true}); m.Scope != want || m.Observations != 4 {
		t.Fatalf("scope %+v obs %d, want all-strong with 4", m.Scope, m.Observations)
	}
}

func TestModifierContradictionBlocksConfirm(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	trs := feedAll(l, modZone,
		modCheck(modT0, cellA, withCharged(494)),
		modCheck(modT0.Add(time.Second), cellA, withCharged(494)),
		modCheck(modT0.Add(2*time.Second), cellA, withCharged(494)))
	if len(trs) != 1 {
		t.Fatalf("transitions %v, want a candidate", states(trs))
	}
	l.feed(modCheck(modT0.Add(3*time.Second), cellA, withCharged(300)), modZone)
	pin := func() *derivationModifier {
		for _, m := range l.active() {
			if m.Effect.Move == 494 {
				return m
			}
		}
		t.Fatal("pin 494 not active")
		return nil
	}
	if c := pin().Contradictions; c != 1 {
		t.Fatalf("contradictions %d, want 1", c)
	}
	for i := range 20 {
		trs = append(trs, l.feed(modCheck(modT0.Add(time.Duration(4+i)*time.Second), cellA, withCharged(494)), modZone)...)
	}
	if m := pin(); m.Status != modCandidate || m.Observations != 23 {
		t.Fatalf("status %v obs %d, want a candidate with 23", m.Status, m.Observations)
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

	l = newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	if trs := l.feed(modCheck(modT0.Add(30*time.Minute), cellA, asSighting), modZone); len(trs) != 0 {
		t.Fatalf("sighting: %v", states(trs))
	}
	if trs := l.sweep(modT0.Add(61 * time.Minute)); len(trs) != 0 {
		t.Fatalf("sweep at +61m after a sighting at +30m: %v, want nothing", states(trs))
	}
	trs = l.sweep(modT0.Add(91 * time.Minute))
	if len(trs) != 1 || !trs[0].At.Equal(modT0.Add(30*time.Minute)) || !trs[0].Mod.End.Equal(modT0.Add(30*time.Minute)) {
		t.Fatalf("sweep at +91m: %+v, want ended at +30m", trs)
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
	l.sweep(modT0.Add(31 * time.Minute))
	if n := l.trackCount(); n != 0 {
		t.Fatalf("tracks %d after sweep, want 0", n)
	}
}

func TestModifierEventCompact(t *testing.T) {
	if s := unsafe.Sizeof(modifierEvent{}); s > 64 {
		t.Fatalf("modifierEvent is %d bytes, want <= 64", s)
	}
	l15 := s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5, -0.12)).Parent(15)
	ev := deriveEventCheck{
		Time:      modT0,
		PokemonID: 959, Form: 3272, Strong: true,
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
		got.SizeRoll != 0.97 || got.Agree != ev.Agree || !got.Strong {
		t.Fatalf("event %+v", got)
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

func TestModifierZoneWideDemotedByOutsideAgreement(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	if !onlyActive(t, l).ZoneWide(3) {
		t.Fatal("not zone-wide after three in-cells")
	}
	if trs := l.feed(modCheck(modT0.Add(time.Second), cellD), modZone); len(trs) != 0 {
		t.Fatalf("outside agreement: %v, want no transition", states(trs))
	}
	m := onlyActive(t, l)
	if m.ZoneWide(3) {
		t.Fatal("still zone-wide after an agreement in cell D")
	}
	if _, ok := m.Outside[cellD]; !ok {
		t.Fatalf("outside %v, want cellD", m.Outside)
	}
	if !m.Covers(modZone, cellA, 3) || m.Covers(modZone, cellD, 3) || m.Covers(modZone, unseenCell, 3) {
		t.Fatal("demoted modifier must cover its in-cells only")
	}
}

func TestModifierOutsideCellRemovedByDeviation(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA, cellB, cellC)
	l.feed(modCheck(modT0.Add(time.Second), cellD), modZone)
	l.feed(modCheck(modT0.Add(2*time.Second), cellD, withCharged(494)), modZone)
	m := onlyActive(t, l)
	if _, out := m.Outside[cellD]; out || len(m.Outside) != 0 {
		t.Fatalf("outside %v, want cellD removed", m.Outside)
	}
	if _, in := m.InCells[cellD]; !in || !m.ZoneWide(3) {
		t.Fatalf("in-cells %d zone-wide %v, want cellD an in-cell and zone-wide again", len(m.InCells), m.ZoneWide(3))
	}
}

func TestModifierDeviationResetsEndCount(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	ts := modT0
	next := func() time.Time { ts = ts.Add(time.Second); return ts }
	var trs []modifierTransition
	for range 19 {
		trs = append(trs, l.feed(modCheck(next(), cellA), modZone)...)
	}
	trs = append(trs, l.feed(modCheck(next(), cellA, withCharged(494)), modZone)...)
	for range 19 {
		trs = append(trs, l.feed(modCheck(next(), cellA), modZone)...)
	}
	if len(trs) != 0 {
		t.Fatalf("transitions %v, want none: the deviation reset the end count", states(trs))
	}
	trs = l.feed(modCheck(next(), cellA), modZone)
	if len(trs) != 1 || trs[0].State != modEnded || trs[0].Mod.EndReason != "agreement" {
		t.Fatalf("20th agreement after the reset: %v, want ended by agreement", states(trs))
	}
}

func TestModifierLearnerDirty(t *testing.T) {
	l := newModifierLearner(defaultModifierParams())
	confirmPin(t, l, modZone, modT0, cellA)
	l.takeDirty()
	check := func(what string, wantDirty, wantRegion bool) {
		t.Helper()
		dirty, region := l.takeDirty()
		if dirty != wantDirty || region != wantRegion {
			t.Fatalf("%s: dirty=%v region=%v, want %v %v", what, dirty, region, wantDirty, wantRegion)
		}
	}
	l.feed(modCheck(modT0.Add(time.Second), cellA), modZone)
	check("in-cell agreement", false, false)
	l.feed(modCheck(modT0.Add(2*time.Second), cellA, withCharged(494)), modZone)
	check("deviation in an in-cell", true, false)
	l.feed(modCheck(modT0.Add(3*time.Second), cellB, withCharged(494)), modZone)
	check("deviation in a new cell", true, true)
	l.feed(modCheck(modT0.Add(4*time.Second), cellD), modZone)
	check("outside agreement of a local modifier", true, false)
	l.feed(modCheck(modT0.Add(5*time.Second), cellD), modZone)
	check("repeated outside agreement", false, false)
	l.feed(modCheck(modT0.Add(6*time.Second), cellA, withCharged(300)), modZone)
	check("contradiction", true, false)
}
