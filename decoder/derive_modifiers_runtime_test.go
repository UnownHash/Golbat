package decoder

import (
	"context"
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

	for i := range 10 {
		submitDerivationModifierEvent(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withCharged(494)))
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
	if adj := idx.lookup(959, 3272, true, cellB, func(s2.CellID) string { return modZone }); adj.Charged != 0 {
		t.Errorf("cellB adjust %+v, want none (local modifier)", adj)
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

func TestDerivationModifiersRuntimeLogsTransitions(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)
	hook := logtest.NewGlobal()
	t.Cleanup(func() {
		hook.Reset()
		log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	})
	for i := range 10 {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withCharged(494)))
	}
	var lines []string
	for _, e := range hook.AllEntries() {
		if strings.HasPrefix(e.Message, "[DERIVE_MODIFIER]") {
			lines = append(lines, e.Message)
		}
	}
	want := "[DERIVE_MODIFIER] confirmed move_pin charged=494 scope=959/3272/strong zone=Europe/London region=1 cells obs=10 contra=0"
	if len(lines) != 2 || lines[1] != want {
		t.Fatalf("log lines %q, want candidate then %q", lines, want)
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
	m.Start, m.Confirm, m.End, m.ZoneWideCells = 3, 10, 20, 3
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
	if got := derivationModifierRuntime.zoneWideCells; got != 3 {
		t.Errorf("zoneWideCells = %d, want 3", got)
	}
	submitDerivationModifierEvent(modCheck(modT0, cellA, withCharged(494)))
	FlushDerivationModifiers()
	submitDerivationModifierEvent(modCheck(modT0, cellA)) // after shutdown: must not panic
	FlushDerivationModifiers()                            // idempotent
}

func TestDerivationModifiersInitRejectsBadThresholds(t *testing.T) {
	prevCfg := config.Config
	t.Cleanup(func() { config.Config = prevCfg })
	withModifierRuntime(t, nil)
	setModifierConfig()
	config.Config.DerivationModifiers.Confirm = 2 // below start
	config.Config.DeriveIv = true

	if err := InitDerivationModifiers(); err == nil {
		t.Fatal("Init accepted confirm < start")
	}
	if derivationModifierRuntime != nil || derivationModifierLearnerOn {
		t.Fatal("learner started with bad thresholds")
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
		InCells: cellSet(cellA), Outside: cellSet()}
	derivationModifierIndex.Store(buildModifierIndex([]*derivationModifier{pin}, 3))

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
		InCells: cellSet(cellA), Outside: cellSet()}
	derivationModifierIndex.Store(buildModifierIndex([]*derivationModifier{pin}, 3))

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
	for range 10 {
		submitDerivationModifierEvent(modCheck(now, cellA, withCharged(494)))
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
	for i := range 10 {
		submitDerivationModifierEvent(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withCharged(494)))
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

// TestDerivationModifiersRuntimeRepublishesDemotion: a pin confirmed
// zone-wide that turns local without a transition (an agreement in another
// cell) is republished at once: the index stops covering the zone.
func TestDerivationModifiersRuntimeRepublishesDemotion(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)
	cells := []s2.CellID{cellA, cellB, cellC}
	for i := range 10 {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), cells[i%3], withCharged(494)))
	}
	if adj := derivationModifierIndex.Load().lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 494 {
		t.Fatalf("zone-wide pin: cellD adjust %+v, want charged 494", adj)
	}

	r.handle(modCheck(modT0.Add(10*time.Second), cellD))
	idx := derivationModifierIndex.Load()
	if adj := idx.lookup(959, 3272, true, cellD, modZoneOf); adj.Charged != 0 {
		t.Errorf("after the outside agreement: cellD adjust %+v, want none", adj)
	}
	if adj := idx.lookup(959, 3272, true, unseenCell, modZoneOf); adj.Charged != 0 {
		t.Errorf("after the outside agreement: unseen cell adjust %+v, want none", adj)
	}
	if adj := idx.lookup(959, 3272, true, cellA, modZoneOf); adj.Charged != 494 {
		t.Errorf("after the outside agreement: cellA adjust %+v, want charged 494", adj)
	}
	snap := activeDerivationModifiers()
	if len(snap) != 1 {
		t.Fatalf("snapshot %d modifiers, want 1", len(snap))
	}
	if _, ok := snap[0].Outside[cellD]; !ok || len(snap[0].Outside) != 1 {
		t.Errorf("snapshot outside %v, want cellD", snap[0].Outside)
	}
}

// TestDerivationModifiersRuntimeRepublishesNewInCell: a deviation in a new
// cell extends a local confirmed pin's index coverage at once.
func TestDerivationModifiersRuntimeRepublishesNewInCell(t *testing.T) {
	r := newModifierRuntime(defaultModifierParams(), 64)
	withModifierRuntime(t, r)
	for i := range 10 {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withCharged(494)))
	}
	if adj := derivationModifierIndex.Load().lookup(959, 3272, true, cellB, modZoneOf); adj.Charged != 0 {
		t.Fatalf("local pin: cellB adjust %+v, want none", adj)
	}
	r.handle(modCheck(modT0.Add(10*time.Second), cellB, withCharged(494)))
	if adj := derivationModifierIndex.Load().lookup(959, 3272, true, cellB, modZoneOf); adj.Charged != 494 {
		t.Fatalf("after a deviation in cellB: adjust %+v, want charged 494", adj)
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
	for i := range 7 {
		evs = append(evs, modCheck(modT0.Add(time.Duration(4+i)*time.Second), cellB, withSize(0.009, 1)))
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
	for i := range 10 {
		r.handle(modCheck(modT0.Add(time.Duration(i)*time.Second), cellA, withCharged(494)))
	}
	r.handle(modCheck(modT0.Add(10*time.Second), cellA, withCharged(494)))
	if obs := activeDerivationModifiers()[0].Observations; obs != 10 {
		t.Fatalf("observations %d right after the confirm, want 10 (rate-limited)", obs)
	}
	clock = clock.Add(deriveModifiersEvidenceInterval)
	r.handle(modCheck(modT0.Add(11*time.Second), cellA)) // changes nothing itself
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
