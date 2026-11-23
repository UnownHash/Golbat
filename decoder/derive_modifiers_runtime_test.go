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
// sighting (no DB: memory-only, webhooks to a sink).
func saveNewModifierTestPokemon(t *testing.T, id uint64) {
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
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, modT0.Unix(), "t")
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
	if !ev.Sighting || ev.PokemonID != 959 || ev.Form != 3272 || !ev.Strong || ev.Cell != cellA ||
		ev.Unix != modT0.Unix() {
		t.Errorf("sighting %+v, want strong 959/3272 in cellA at modT0", ev)
	}
}
