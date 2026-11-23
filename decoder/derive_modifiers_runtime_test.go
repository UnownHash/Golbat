package decoder

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"

	"golbat/config"
	"golbat/db"
	"golbat/pogo"
)

// withModifierRuntime installs r as the process-wide learner (switched on)
// for one test, restoring the previous runtime, flag, index and snapshot.
func withModifierRuntime(t *testing.T, r *modifierRuntime) {
	t.Helper()
	prevRuntime, prevOn := derivationModifierRuntime, derivationModifierLearnerOn
	derivationModifierRuntime, derivationModifierLearnerOn = r, r != nil
	t.Cleanup(func() {
		derivationModifierRuntime, derivationModifierLearnerOn = prevRuntime, prevOn
		derivationModifierIndex.Store(nil)
		derivationModifierSnapshot.Store(nil)
	})
}

// drainModifierRuntime handles every queued event on the test goroutine, in
// place of the worker.
func drainModifierRuntime(r *modifierRuntime) {
	for len(r.events) > 0 {
		r.handle(<-r.events)
	}
}

func TestDerivationModifiersRuntimeConfirmAndEnd(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)

	for i, c := range modWideCells(10) {
		submitDerivationModifierEvent(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withCharged(494)))
	}
	drainModifierRuntime(r)
	idx := derivationModifierIndex.Load()
	if idx == nil {
		t.Fatal("no index after 10 deviations")
	}
	adj := idx.lookup(959, 3272, true, cellA, func(s2.CellID) string { return modZone })
	if adj.Charged != 494 {
		t.Fatalf("cellA adjust %+v, want charged pin 494", adj)
	}
	snap := activeDerivationModifiers()
	if len(snap) != 1 || snap[0].Status != modConfirmed || snap[0].Zone != modZone {
		t.Fatalf("snapshot %+v, want one confirmed modifier in %s", snap, modZone)
	}

	for i := range 20 {
		submitDerivationModifierEvent(modCheck(modT0.Add(time.Duration(20+i)*time.Second), cellA))
	}
	drainModifierRuntime(r)
	if idx := derivationModifierIndex.Load(); idx != nil {
		t.Fatalf("index %+v after 20 agreements, want nil (base values again)", idx)
	}
	if snap := activeDerivationModifiers(); len(snap) != 0 {
		t.Errorf("snapshot %d modifiers after end, want 0", len(snap))
	}
}

// Transitions are logged at Info, but a candidate's coming and its
// unconfirmed end at Debug: scattered noise makes those all day.
func TestDerivationModifiersRuntimeLogsTransitions(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)
	hook := logtest.NewGlobal()
	level := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() {
		hook.Reset()
		log.SetLevel(level)
		log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	})
	type line struct {
		level log.Level
		msg   string
	}
	lines := func() []line {
		var out []line
		for _, e := range hook.AllEntries() {
			if strings.HasPrefix(e.Message, "[DERIVE_MODIFIER]") {
				out = append(out, line{e.Level, e.Message})
			}
		}
		hook.Reset()
		return out
	}
	for i, c := range modWideCells(10) {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withCharged(494)))
	}
	want := "[DERIVE_MODIFIER] confirmed move_pin charged=494 scope=959/3272/strong zone=Europe/London region=zone-wide obs=10 contra=0"
	got := lines()
	if len(got) != 2 || got[0].level != log.DebugLevel || !strings.Contains(got[0].msg, " candidate move_pin ") ||
		got[1] != (line{log.InfoLevel, want}) {
		t.Fatalf("log lines %v, want a candidate at Debug then at Info %q", got, want)
	}
	for i := range 8 {
		r.handle(modCheck(modT0.Add(time.Duration(10+i)*time.Second), cellD))
	}
	if got := lines(); len(got) != 1 || got[0].level != log.InfoLevel || !strings.HasSuffix(got[0].msg, " reason=agreement") {
		t.Fatalf("log lines %v, want the end by agreement at Info", got)
	}
	// A venue-only candidate ends unconfirmed, at Debug.
	for i := range 26 {
		r.handle(modCheck(modT0.Add(time.Hour+time.Duration(i)*5*time.Minute), cellA, withCharged(494)))
	}
	got = lines()
	if len(got) != 2 || !strings.HasSuffix(got[1].msg, " reason=unconfirmed") {
		t.Fatalf("log lines %v, want a candidate and its unconfirmed end", got)
	}
	for _, l := range got {
		if l.level != log.DebugLevel {
			t.Fatalf("log lines %v, want all at Debug", got)
		}
	}

	wide := modifierTransition{State: modConfirmed, Mod: &derivationModifier{Type: modLevelOffset,
		Scope: modifierScope{PokemonID: 1}, Effect: modifierEffect{Delta: 5}, Zone: modZone, ZoneWide: true,
		Excluded: excludedSet(cellD), Cells: cellSet(cellA, cellB), Observations: 12}}
	want = "[DERIVE_MODIFIER] confirmed level_offset delta=+5 scope=1/0 zone=Europe/London " +
		"region=zone-wide less 1 plus 2 cells obs=12 contra=0"
	if got := formatModifierTransition(wide); got != want {
		t.Fatalf("zone-wide line %q, want %q", got, want)
	}
}

func TestDerivationModifiersZoneCellZero(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 1)
	if z := r.zone(0); z != "" {
		t.Errorf("zone(0) = %q, want empty", z)
	}
	if z := r.zone(cellA); z != modZone {
		t.Errorf("zone(cellA) = %q, want %s", z, modZone)
	}
	if z := r.zone(cellB); z != modZone || len(r.zoneNames) != 2 {
		t.Errorf("zone(cellB) = %q, names %q: want %s sharing one name entry", z, r.zoneNames, modZone)
	}
}

// setModifierConfig enables the learner with the default thresholds.
func setModifierConfig() {
	m := &config.Config.DerivationModifiers
	m.Enabled = true
	m.Start, m.Confirm = 3, 10
	m.WindowSeconds, m.UnseenSeconds = 1800, 3600
}

func TestDerivationModifiersInitDisabledWithoutDeriveIv(t *testing.T) {
	prevCfg := config.Config
	t.Cleanup(func() { config.Config = prevCfg })
	withModifierRuntime(t, nil)
	setModifierConfig()
	config.Config.DeriveIv = false

	if err := InitDerivationModifiers(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if derivationModifierRuntime != nil || derivationModifierLearnerOn {
		t.Fatal("learner started without derive_iv")
	}
	FlushDerivationModifiers() // no-op
}

func TestDerivationModifiersInitStartsAndFlushes(t *testing.T) {
	prevCfg := config.Config
	t.Cleanup(func() { config.Config = prevCfg })
	withModifierRuntime(t, nil)
	setModifierConfig()
	config.Config.DeriveIv = true

	if err := InitDerivationModifiers(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if derivationModifierRuntime == nil || !derivationModifierLearnerOn {
		t.Fatal("learner not started")
	}
	submitDerivationModifierEvent(modCheck(modT0, cellA, withCharged(494)))
	FlushDerivationModifiers()
	submitDerivationModifierEvent(modCheck(modT0, cellA)) // after shutdown: must not panic
	FlushDerivationModifiers()                            // idempotent
}

// Confirm is judged on the 16-entry zone record, so start and confirm above
// 16 could never be met.
func TestDerivationModifiersInitRejectsBadThresholds(t *testing.T) {
	prevCfg := config.Config
	t.Cleanup(func() { config.Config = prevCfg })
	withModifierRuntime(t, nil)
	config.Config.DeriveIv = true
	m := &config.Config.DerivationModifiers
	for name, set := range map[string]func(){
		"confirm below start": func() { m.Confirm = 2 },
		"start 0":             func() { m.Start = 0 },
		"start 17":            func() { m.Start, m.Confirm = 17, 17 },
		"confirm 17":          func() { m.Confirm = 17 },
		"window 0":            func() { m.WindowSeconds = 0 },
		"unseen 0":            func() { m.UnseenSeconds = 0 },
	} {
		setModifierConfig()
		set()
		if err := InitDerivationModifiers(); err == nil {
			t.Errorf("%s: Init accepted it", name)
		}
		if derivationModifierRuntime != nil || derivationModifierLearnerOn {
			t.Fatalf("%s: learner started with bad thresholds", name)
		}
	}
	setModifierConfig()
	m.Start, m.Confirm = 16, 16
	if _, err := modifierParamsFromConfig(); err != nil {
		t.Errorf("start 16 confirm 16 rejected: %v", err)
	}
}

func TestSubmitDerivationModifierEventDropsWhenFull(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 1) // no reader
	withModifierRuntime(t, r)
	deriveModifierDrops.Reset()
	hook := logtest.NewGlobal()
	t.Cleanup(func() {
		hook.Reset()
		log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 3 {
			submitDerivationModifierEvent(modCheck(modT0, cellA))
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("submit blocked on a full queue")
	}
	if len(r.events) != 1 {
		t.Fatalf("queued = %d, want 1", len(r.events))
	}
	var logged bool
	for _, e := range hook.AllEntries() {
		logged = logged || strings.Contains(e.Message, "[DERIVE_MODIFIER] dropped 1 events")
	}
	if !logged {
		t.Fatal("drop was not reported")
	}
}

func TestSubmitDerivationModifierEventOffIsNoop(t *testing.T) {
	withModifierRuntime(t, nil)
	submitDerivationModifierEvent(modCheck(modT0, cellA))
	FlushDerivationModifiers()
}

// saveNewModifierTestPokemon saves a new strong 959/3272 record as a first
// sighting at modT0 (no DB: memory-only, webhooks to a sink).
func saveNewModifierTestPokemon(t *testing.T, id uint64) {
	t.Helper()
	saveNewModifierTestPokemonAt(t, id, modT0.Unix())
}

// saveNewModifierTestPokemonAt is saveNewModifierTestPokemon with the save
// stamped at (the sighting's map timestamp).
func saveNewModifierTestPokemonAt(t *testing.T, id uint64, at int64) {
	t.Helper()
	freshPokemonRecord(t, id)
	p, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, id, "t")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	defer unlock()
	p.SetPokemonId(959)
	p.SetForm(null.IntFrom(3272))
	p.SetIsStrong(null.BoolFrom(true))
	p.Lat, p.Lon = 51.50, -0.12
	p.SetSeenType(SeenTypeCodeWild)
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, at, "t")
}

func TestDerivationModifierSightingGated(t *testing.T) {
	lureTestSetup(t)
	r := newModifierRuntime(defaultModifierParams(), 8) // not started: events stay queued
	withModifierRuntime(t, r)

	saveNewModifierTestPokemon(t, 990001)
	if n := len(r.events); n != 0 {
		t.Fatalf("nil index: %d sightings sent, want 0", n)
	}

	pin := &derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 959, Form: 3272, Strong: true},
		Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Zone: modZone, Status: modCandidate,
		ZoneWide: true}
	derivationModifierIndex.Store(buildModifierIndex([]*derivationModifier{pin}))

	saveNewModifierTestPokemon(t, 990002)
	if n := len(r.events); n != 1 {
		t.Fatalf("active scope: %d sightings sent, want 1", n)
	}
	ev := <-r.events
	if !ev.Sighting || ev.PokemonID != 959 || ev.Form != 3272 || !ev.Strong || ev.Cell != cellA {
		t.Errorf("sighting %+v, want strong 959/3272 in cellA", ev)
	}
	// Stamped with the server's clock at submit, not the save's map time.
	if d := time.Now().Unix() - ev.Unix; d < 0 || d > 1 {
		t.Errorf("sighting Unix %d, want now (%d)", ev.Unix, time.Now().Unix())
	}
}

// A sighting whose map timestamp is hours ahead reaches the learner on the
// server's clock: one future-stamped sighting once ended every modifier.
func TestDerivationModifierSightingUsesServerClock(t *testing.T) {
	lureTestSetup(t)
	r := newModifierRuntime(defaultModifierParams(), 8) // not started: events stay queued
	withModifierRuntime(t, r)
	pin := &derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 959, Form: 3272, Strong: true},
		Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Zone: modZone, Status: modCandidate,
		ZoneWide: true}
	derivationModifierIndex.Store(buildModifierIndex([]*derivationModifier{pin}))

	saveNewModifierTestPokemonAt(t, 990003, time.Now().Add(3*time.Hour).Unix())
	if n := len(r.events); n != 1 {
		t.Fatalf("%d sightings sent, want 1", n)
	}
	ev := <-r.events
	if d := time.Now().Unix() - ev.Unix; d < 0 || d > 1 {
		t.Errorf("sighting Unix %d, want within a second of now (%d)", ev.Unix, time.Now().Unix())
	}
}

// waitModifierIndex polls until the published index is (non-)nil, failing
// after a bound.
func waitModifierIndex(t *testing.T, wantNil bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for (derivationModifierIndex.Load() == nil) != wantNil {
		if time.Now().After(deadline) {
			t.Fatalf("index nil=%v after %s, want nil=%v", !wantNil, within, wantNil)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestDerivationModifiersWorkerSweepEndsUnseen drives run itself: submitted
// deviations confirm a pin on the worker goroutine, and its ticker sweep
// later ends it as unseen, clearing the index.
func TestDerivationModifiersWorkerSweepEndsUnseen(t *testing.T) {
	p := defaultModifierParams()
	p.Unseen = 5 * time.Second
	r := newModifierRuntime(p, 64)
	r.sweepInterval = 20 * time.Millisecond
	withModifierRuntime(t, r)
	r.start()
	t.Cleanup(r.shutdown)

	// One timestamp for all ten, so no feed ends the pin as unseen; it lives
	// at least Unseen minus under a second of wall time before a sweep can.
	now := time.Now()
	for _, c := range modWideCells(10) {
		submitDerivationModifierEvent(modCheck(now, c, withCharged(494)))
	}
	waitModifierIndex(t, false, 4*time.Second)
	waitModifierIndex(t, true, 10*time.Second)
	if snap := activeDerivationModifiers(); len(snap) != 0 {
		t.Errorf("snapshot %d modifiers after unseen end, want 0", len(snap))
	}
}

// TestDerivationModifiersFlushDrainsQueue: events still queued when Flush is
// called are fed before it returns.
func TestDerivationModifiersFlushDrainsQueue(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)
	// Ten spread matches confirm a zone-wide pin: only that publishes an
	// index (a venue-only candidate has nothing to apply or watch).
	for i, c := range modWideCells(10) {
		submitDerivationModifierEvent(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withCharged(494)))
	}
	r.start() // started after queueing, so Flush finds the events still queued or in flight
	FlushDerivationModifiers()
	if derivationModifierIndex.Load() == nil {
		t.Fatal("Flush returned before the queued deviations were fed")
	}
	if len(r.events) != 0 {
		t.Errorf("%d events left queued after Flush", len(r.events))
	}
}

func modZoneOf(s2.CellID) string { return modZone }

// TestDerivationModifiersRuntimeRepublishesExclusion: an agreement in a
// cell of a zone-wide pin excludes that cell without a transition; the
// shrink is republished at once.
func TestDerivationModifiersRuntimeRepublishesExclusion(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	for i, c := range modWideCells(10) {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withCharged(494)))
	}
	if adj := derivationModifierIndex.Load().lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 494 {
		t.Fatalf("zone-wide pin: cellD adjust %+v, want charged 494", adj)
	}

	r.handle(modCheck(modT0.Add(10*time.Second), cellD))
	snap := activeDerivationModifiers()
	if len(snap) != 1 {
		t.Fatalf("snapshot %d modifiers, want 1", len(snap))
	}
	m := snap[0]
	if _, d := m.Excluded[cellD]; !m.ZoneWide || !d || len(m.Excluded) != 1 {
		t.Fatalf("snapshot zone-wide %v excluded %v, want zone-wide less cellD at once", m.ZoneWide, m.Excluded)
	}
	if m.Covers(modZone, cellD) || !m.Covers(modZone, unseenCell) || !m.Covers(modZone, cellA) {
		t.Error("published pin must cover the zone but not the agreeing cellD")
	}
	idx := derivationModifierIndex.Load()
	if adj := idx.lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 0 {
		t.Errorf("index: cellD adjust %+v, want none at once", adj)
	}
	if adj := idx.lookup(959, 3272, true, unseenCell, modZoneOf); adj.Charged != 494 {
		t.Errorf("index: unseen cell adjust %+v, want charged 494", adj)
	}
}

// TestDerivationModifiersRuntimeRepublishesReadmission: a match in an
// excluded cell readmits it; growth is published with evidence, at most once
// per deriveModifiersEvidenceInterval.
func TestDerivationModifiersRuntimeRepublishesReadmission(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	for i, c := range modWideCells(10) {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withCharged(494)))
	}
	r.handle(modCheck(modT0.Add(10*time.Second), cellD))
	if adj := derivationModifierIndex.Load().lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 0 {
		t.Fatalf("excluded cell adjust %+v, want none", adj)
	}
	r.handle(modCheck(modT0.Add(11*time.Second), cellD, withCharged(494)))
	if adj := derivationModifierIndex.Load().lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 0 {
		t.Fatalf("growth right after the shrink: cell adjust %+v, want none yet (rate-limited)", adj)
	}
	clock = clock.Add(deriveModifiersEvidenceInterval)
	r.handle(modCheck(modT0.Add(12*time.Second), cellA, withSpecies(1, 0))) // changes nothing itself
	if adj := derivationModifierIndex.Load().lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 494 {
		t.Fatalf("after the interval: cell adjust %+v, want charged 494", adj)
	}
}

// TestDerivationModifiersRuntimeZoneWideMatchNoRebuild: a match in a new
// cell of a zone-wide modifier changes no lookup, so it does not rebuild the
// index before the evidence interval.
func TestDerivationModifiersRuntimeZoneWideMatchNoRebuild(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	for i, c := range modWideCells(10) {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withCharged(494)))
	}
	idx := derivationModifierIndex.Load()
	if adj := idx.lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 494 {
		t.Fatalf("zone-wide pin: cellD adjust %+v, want charged 494", adj)
	}
	r.handle(modCheck(modT0.Add(10*time.Second), cellE, withCharged(494)))
	if derivationModifierIndex.Load() != idx {
		t.Fatal("a match in a new cell of a zone-wide modifier rebuilt the index at once")
	}
}

// TestDerivationModifiersRuntimeClonesChangedOnly: a publish clones only the
// modifiers changed since the last one, each at most once.
func TestDerivationModifiersRuntimeClonesChangedOnly(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	cells := modWideCells(10)
	ts := modT0
	const n = 30
	for sp := range n {
		for i := range 10 {
			r.handle(modCheck(ts, cells[i], withCharged(494), withSpecies(int32(1000+sp), 0)))
			ts = ts.Add(time.Second)
		}
	}
	before := activeDerivationModifiers()
	if len(before) != n {
		t.Fatalf("%d active modifiers, want %d", len(before), n)
	}
	for _, m := range before {
		if m.Status != modConfirmed || !m.ZoneWide {
			t.Fatalf("modifier %+v, want confirmed zone-wide", m)
		}
	}
	idx, clones := derivationModifierIndex.Load(), r.clones

	// Matches in new cells on the first ten modifiers: evidence only.
	for sp := range 10 {
		for _, c := range []s2.CellID{cellD, cellE, cellF, unseenCell} {
			r.handle(modCheck(ts, c, withCharged(494), withSpecies(int32(1000+sp), 0)))
		}
	}
	if derivationModifierIndex.Load() != idx || r.clones != clones {
		t.Fatalf("published before the interval (clones %d → %d)", clones, r.clones)
	}
	clock = clock.Add(deriveModifiersEvidenceInterval)
	r.handle(modCheck(ts, cellA, withSpecies(1, 0))) // changes nothing itself
	if derivationModifierIndex.Load() == idx {
		t.Fatal("evidence not published after the interval")
	}
	if got := r.clones - clones; got != 10 {
		t.Errorf("publish cloned %d modifiers, want the 10 changed ones", got)
	}
	after := map[int32]*derivationModifier{}
	for _, m := range activeDerivationModifiers() {
		after[m.Scope.PokemonID] = m
	}
	for _, m := range before {
		changed := m.Scope.PokemonID < 1010
		if reused := after[m.Scope.PokemonID] == m; reused == changed {
			t.Errorf("modifier %d: reused %v, want %v", m.Scope.PokemonID, reused, !changed)
		}
	}
}

// TestDerivationModifiersRuntimeRepublishesSizeBoundaries: a confirmed size
// modifier's narrowed interval reaches the index at once.
func TestDerivationModifiersRuntimeRepublishesSizeBoundaries(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)
	evs := []modifierEvent{
		modCheck(modT0, cellA, withSize(0.008, 1)),
		modCheck(modT0.Add(time.Second), cellB, withSize(0.009, 1)),
		modCheck(modT0.Add(2*time.Second), cellC, withSize(0.011, 1)),
		modCheck(modT0.Add(3*time.Second), cellA, withSize(0.015, 2)), // an agreement
	}
	for i, c := range modWideCells(7) {
		evs = append(evs, modCheck(modT0.Add(time.Duration(4+i)*time.Second), c, withSize(0.009, 1)))
	}
	for _, ev := range evs {
		r.handle(ev)
	}
	snap := activeDerivationModifiers()
	if len(snap) != 1 || snap[0].Status != modConfirmed {
		t.Fatalf("snapshot %+v, want one confirmed size modifier", snap)
	}
	if b := derivationModifierIndex.Load().lookup(19, 0, false, cellA, modZoneOf).Size["1/2"]; b.Lo != 0.011 {
		t.Fatalf("1/2 = %+v, want lo 0.011", b)
	}
	r.handle(modCheck(modT0.Add(time.Minute), cellA, withSize(0.013, 1)))
	b := derivationModifierIndex.Load().lookup(19, 0, false, cellA, modZoneOf).Size["1/2"]
	if b != (sizeBoundary{Lo: 0.013, Hi: 0.015, HasLo: true, HasHi: true}) {
		t.Fatalf("after a narrowing roll: 1/2 = %+v, want [0.013, 0.015]", b)
	}
}

// TestDerivationModifiersRuntimeEvidenceRateLimited: an evidence-only change
// (another observation) is republished at most once per
// deriveModifiersEvidenceInterval, and not lost in between.
func TestDerivationModifiersRuntimeEvidenceRateLimited(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	for i, c := range modWideCells(10) {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withCharged(494)))
	}
	r.handle(modCheck(modT0.Add(10*time.Second), cellA, withCharged(494)))
	if obs := activeDerivationModifiers()[0].Observations; obs != 10 {
		t.Fatalf("observations %d right after the confirm, want 10 (rate-limited)", obs)
	}
	clock = clock.Add(deriveModifiersEvidenceInterval)
	r.handle(modCheck(modT0.Add(11*time.Second), cellA, withSpecies(1, 0))) // changes nothing itself
	if obs := activeDerivationModifiers()[0].Observations; obs != 11 {
		t.Fatalf("observations %d after the interval, want 11", obs)
	}
}

// Each spawn counts once: the learner gets a record's first encounter only,
// however often (or under whatever weather) the spawn is encountered again,
// while the events log still gets every check.
func TestAddEncounterPokemonFeedsLearnerOncePerSpawn(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 8) // not started: events stay queued
	withModifierRuntime(t, r)
	w := newTestDeriveEventsWriter(t, t.TempDir(), &testClock{time.Now()})
	w.events = make(chan deriveEventCheck, 8) // no reader
	prevWriter := derivationEventsWriter
	derivationEventsWriter = w
	t.Cleanup(func() { derivationEventsWriter = prevWriter })

	// cpm 0.5 is level 14; 0.5755 is 19, the same spawn boosted (a
	// boosted scan at the unboosted level would confirm a Ditto, which
	// sends no check at all).
	encounterAt := func(p *Pokemon, cpm float32) {
		p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
			PokemonId: 1, IndividualAttack: 10, IndividualDefense: 10, IndividualStamina: 10,
			CpMultiplier: cpm, PokemonDisplay: &pogo.PokemonDisplayProto{},
		}, "")
	}
	encounterOnce := func(p *Pokemon) { encounterAt(p, 0.5) }

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(777888999112)}}
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	encounterOnce(p)
	if len(r.events) != 1 || len(w.events) != 1 {
		t.Fatalf("first encounter: learner %d, events log %d; want 1 and 1", len(r.events), len(w.events))
	}
	encounterOnce(p)
	if len(r.events) != 1 || len(w.events) != 2 {
		t.Errorf("re-encounter: learner %d, events log %d; want 1 and 2", len(r.events), len(w.events))
	}
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_RAINY))) // a weather flip: same spawn
	encounterAt(p, 0.5755)
	if p.IsDitto {
		t.Fatal("setup: the boosted re-encounter confirmed a Ditto")
	}
	if len(r.events) != 1 || len(w.events) != 3 {
		t.Errorf("weather-flip re-encounter: learner %d, events log %d; want 1 and 3", len(r.events), len(w.events))
	}

	// A record reloaded from the database without its scan history: the
	// seen type it was loaded with records the earlier encounter.
	q := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(777888999113)}}
	q.SetSeenType(SeenTypeCodeEncounter)
	q.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	q.snapshotOldValues()
	encounterOnce(q)
	if len(r.events) != 1 || len(w.events) != 4 {
		t.Errorf("reloaded encountered record: learner %d, events log %d; want 1 and 4", len(r.events), len(w.events))
	}
}

// firstEncounterTestRig installs a queued learner and events writer (no
// readers) for one test.
func firstEncounterTestRig(t *testing.T) (*modifierRuntime, *deriveEventsWriter) {
	t.Helper()
	r := newModifierRuntime(defaultModifierParams(), 8)
	withModifierRuntime(t, r)
	w := newTestDeriveEventsWriter(t, t.TempDir(), &testClock{time.Now()})
	w.events = make(chan deriveEventCheck, 8)
	prevWriter := derivationEventsWriter
	derivationEventsWriter = w
	t.Cleanup(func() { derivationEventsWriter = prevWriter })
	return r, w
}

func encounterBulbasaur(p *Pokemon) {
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
		PokemonId: 1, IndividualAttack: 10, IndividualDefense: 10, IndividualStamina: 10,
		CpMultiplier: 0.5, PokemonDisplay: &pogo.PokemonDisplayProto{},
	}, "")
}

// The usual life of a spawn: sighted wild, then encountered. The encounter is
// its first and reaches the learner once.
func TestAddEncounterPokemonFeedsLearnerAfterWildSighting(t *testing.T) {
	r, w := firstEncounterTestRig(t)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(777888999114)}}
	p.SetPokemonId(1)
	p.SetSeenType(SeenTypeCodeWild)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.snapshotOldValues()
	p.SetSeenType(SeenTypeCodeEncounter) // as updatePokemonFromEncounterProto does
	encounterBulbasaur(p)
	if len(r.events) != 1 || len(w.events) != 1 {
		t.Errorf("learner %d, events log %d; want 1 and 1", len(r.events), len(w.events))
	}
}

// With pokemon_internal_to_db a roll-marked sighting writes GolbatInternal
// with the mark and no scans. Those bytes are not an encounter: the record's
// first encounter still reaches the learner (here the disguise has since
// changed, clearing the mark, so the encounter is checked).
func TestAddEncounterPokemonFeedsLearnerWithRollMarkOnlyInternal(t *testing.T) {
	r, w := firstEncounterTestRig(t)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(777888999115)}}
	p.SetIsDitto(true)
	p.dittoRollMarked = true
	p.rewriteGolbatInternal()
	if len(p.GolbatInternal) == 0 || len(p.scanHistory) != 0 {
		t.Fatalf("setup: GolbatInternal %d bytes, history %d; want a mark-only blob", len(p.GolbatInternal), len(p.scanHistory))
	}
	p.SetIsDitto(false) // the display changed; the stored bytes outlive the flag
	p.SetPokemonId(1)
	p.SetSeenType(SeenTypeCodeWild)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.snapshotOldValues()
	encounterBulbasaur(p)
	if len(r.events) != 1 || len(w.events) != 1 {
		t.Errorf("learner %d, events log %d; want 1 and 1", len(r.events), len(w.events))
	}
}

// TestDerivationModifiersRuntimeUnexplainedGrowthAtOnce: a confirmed
// unexplained modifier's growth (here a readmitted cell) widens suppression,
// so it is published at once, not with evidence.
func TestDerivationModifiersRuntimeUnexplainedGrowthAtOnce(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	for i, c := range modWideCells(10) {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), c, withIVsDisagree))
	}
	if !derivationModifierIndex.Load().lookup(959, 3272, true, cellB, modZoneOf).Suppress {
		t.Fatal("confirmed zone-wide unexplained: cellB not suppressed")
	}
	r.handle(modCheck(modT0.Add(10*time.Second), cellB))
	if derivationModifierIndex.Load().lookup(959, 3272, true, cellB, modZoneOf).Suppress {
		t.Fatal("cellB suppressed after its agreement")
	}
	r.handle(modCheck(modT0.Add(11*time.Second), cellB, withIVsDisagree))
	if !derivationModifierIndex.Load().lookup(959, 3272, true, cellB, modZoneOf).Suppress {
		t.Error("unexplained growth back into cellB not published at once")
	}
}

// TestDerivationModifiersRuntimeSharesUnchangedCells: a republished copy of
// a modifier whose venue cells or excluded cells did not change shares the
// previous copy's set; a change gives it a new one and leaves the published
// one alone.
func TestDerivationModifiersRuntimeSharesUnchangedCells(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	clock := modT0
	r.clock = func() time.Time { return clock }
	withModifierRuntime(t, r)
	ts := modT0
	feed := func(cell s2.CellID, opts ...func(*modifierEvent)) {
		r.handle(modCheck(ts, cell, opts...))
		ts = ts.Add(time.Second)
	}
	match := func(cell s2.CellID) { feed(cell, withCharged(494)) }
	for _, c := range modWideCells(10) {
		match(c)
	}
	feed(cellD) // excluded, published at once
	first := activeDerivationModifiers()
	if len(first) != 1 || first[0].Status != modConfirmed || !first[0].ZoneWide || len(first[0].Excluded) != 1 {
		t.Fatalf("snapshot %+v, want one confirmed zone-wide modifier less one cell", first)
	}
	cellsID := func(m *derivationModifier) uintptr { return reflect.ValueOf(m.Cells).Pointer() }
	exclID := func(m *derivationModifier) uintptr { return reflect.ValueOf(m.Excluded).Pointer() }

	clock = clock.Add(deriveModifiersEvidenceInterval)
	match(cellA) // evidence only
	second := activeDerivationModifiers()
	if second[0] == first[0] || second[0].Observations != 11 {
		t.Fatalf("evidence not republished: %+v", second[0])
	}
	if cellsID(second[0]) != cellsID(first[0]) || exclID(second[0]) != exclID(first[0]) {
		t.Error("unchanged cells or excluded cells were cloned")
	}

	clock = clock.Add(deriveModifiersEvidenceInterval)
	match(cellA) // lists cellA
	third := activeDerivationModifiers()
	if _, ok := third[0].Cells[cellA]; !ok || cellsID(third[0]) == cellsID(second[0]) {
		t.Errorf("new venue cell: cells %v, shared %v; want cellA in a new set",
			third[0].Cells, cellsID(third[0]) == cellsID(second[0]))
	}
	if exclID(third[0]) != exclID(second[0]) {
		t.Error("unchanged excluded cells were cloned")
	}
	if _, ok := second[0].Cells[cellA]; ok {
		t.Error("a published cell set was modified")
	}

	feed(cellE) // excluded, published at once
	fourth := activeDerivationModifiers()
	if _, ok := fourth[0].Excluded[cellE]; !ok || exclID(fourth[0]) == exclID(third[0]) {
		t.Errorf("new excluded cell: excluded %v, shared %v; want cellE in a new set",
			fourth[0].Excluded, exclID(fourth[0]) == exclID(third[0]))
	}
	if _, ok := third[0].Excluded[cellE]; ok || len(third[0].Excluded) != 1 {
		t.Error("a published excluded set was modified")
	}
	if cellsID(fourth[0]) != cellsID(third[0]) {
		t.Error("unchanged cells were cloned")
	}
}

// TestDerivationModifiersRuntimePublishesAllStrongSummary: the runtime
// publishes the all-strong summary with the snapshot, never in the index.
func TestDerivationModifiersRuntimePublishesAllStrongSummary(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)
	ts := modT0
	for _, sp := range []int32{1000, 1001} {
		for _, c := range modWideCells(10) {
			r.handle(modCheck(ts, c, withLevelDelta(5), withSpecies(sp, 0)))
			ts = ts.Add(time.Second)
		}
	}
	var summary []ApiDerivationModifier
	for _, m := range GetApiDerivationModifiers(ts).Modifiers {
		if m.Scope.Pokemon == nil {
			summary = append(summary, m)
		}
	}
	if len(summary) != 1 || summary[0].Status != "confirmed" || summary[0].Applied {
		t.Fatalf("summaries %+v, want one confirmed, unapplied", summary)
	}
	if idx := derivationModifierIndex.Load(); idx.wantsSighting(635, 0, true) {
		t.Error("the summary reached the index")
	}
	if n := len(activeDerivationModifiers()); n != 2 {
		t.Errorf("%d active modifiers, want the 2 species", n)
	}
}

// The learner's cell → zone map is cleared before an insert would take it
// past deriveZoneCacheMax; the zone names survive.
func TestDerivationModifiersRuntimeZoneCacheCapped(t *testing.T) {
	withZoneCacheMax(t, 3)
	r := newModifierRuntime(defaultModifierParams(), 1)
	for i, c := range zoneCacheCells(7) {
		if z := r.zone(c); z != adjZoneA {
			t.Fatalf("cell %d zone %q, want %s", i, z, adjZoneA)
		}
		if len(r.zoneOf) > 3 {
			t.Fatalf("after %d cells: %d entries, want ≤ 3", i+1, len(r.zoneOf))
		}
		if _, ok := r.zoneOf[c]; !ok {
			t.Fatalf("cell %d not cached", i)
		}
	}
	if len(r.zoneNames) != 2 {
		t.Errorf("zone names %v, want \"\" and %s once", r.zoneNames, adjZoneA)
	}
}
