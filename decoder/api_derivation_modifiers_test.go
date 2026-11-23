package decoder

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/golang/geo/s2"

	"golbat/config"
)

func TestApiDerivationModifiersShape(t *testing.T) {
	prevCfg := config.Config.DerivationModifiers.ZoneWideCells
	config.Config.DerivationModifiers.ZoneWideCells = 3
	defer func() {
		config.Config.DerivationModifiers.ZoneWideCells = prevCfg
		derivationModifierSnapshot.Store(nil)
	}()

	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	pin := withSpread(&derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 959, Form: 3272, Strong: true},
		Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Zone: "Europe/London", Status: modConfirmed,
		Start: start, Observations: 14, InCells: zoneWideIn(), Outside: map[s2.CellID]struct{}{}})
	size := &derivationModifier{Type: modSizeCutoffs, Scope: modifierScope{PokemonID: 1},
		Boundaries: map[string]sizeBoundary{"1/2": {Lo: 0.5, HasLo: true}},
		Zone:       "Europe/London", Status: modCandidate, Start: start.Add(time.Hour),
		InCells: cellSet(adjCellA.Next(), adjCellA), Outside: cellSet(adjCellA.Prev())}
	unexp := withSpread(&derivationModifier{Type: modUnexplained, Scope: modifierScope{PokemonID: 2, Form: 5, Strong: true},
		Zone: "Europe/London", Status: modCandidate, Start: start, InCells: zoneWideIn(), Outside: map[s2.CellID]struct{}{}})
	mods := []*derivationModifier{size, pin, unexp}
	derivationModifierSnapshot.Store(&mods)

	res := GetApiDerivationModifiers(time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC))
	if res.Version != 1 || res.GeneratedAt != "2026-11-17T12:00:00Z" || len(res.Modifiers) != 3 {
		t.Fatalf("got %+v", res)
	}
	p, s, u := res.Modifiers[0], res.Modifiers[1], res.Modifiers[2]
	if u.ID != "2026-11-15-unexplained-strong-2-5" || len(u.Effect) != 1 || u.Effect["type"] != "unexplained" {
		t.Fatalf("unexplained %+v", u)
	}
	if p.ID != "2026-11-15-move_pin-strong-959-3272-charged-494" || p.Region.Cells != nil || p.Status != "confirmed" {
		t.Fatalf("pin %+v", p)
	}
	if s.ID != "2026-11-15-size_cutoffs-1-0" || s.Status != "candidate" || len(s.Region.Cells) != 2 ||
		s.Region.Cells[0] > s.Region.Cells[1] || s.Region.Cells[0] != minTok(adjCellA, adjCellA.Next()) {
		t.Fatalf("size %+v", s)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	ms := back["modifiers"].([]any)
	eff := ms[1].(map[string]any)["effect"].(map[string]any)
	bd := eff["boundaries"].(map[string]any)["1/2"].([]any)
	if eff["type"] != "size_cutoffs" || bd[0] != 0.5 || bd[1] != nil {
		t.Fatalf("size effect %v", eff)
	}
	if ms[0].(map[string]any)["region"].(map[string]any)["cells"] != nil {
		t.Fatalf("zone-wide cells should be null: %s", b)
	}
	if ms[0].(map[string]any)["window"].(map[string]any)["end"] != nil {
		t.Fatalf("window end should be null: %s", b)
	}
}

func minTok(a, b s2.CellID) string {
	x, y := a.ToToken(), b.ToToken()
	if x < y {
		return x
	}
	return y
}

func TestApiDerivationModifiersLearnerOff(t *testing.T) {
	derivationModifierSnapshot.Store(nil)
	res := GetApiDerivationModifiers(time.Now())
	if res.Version != 1 || res.Modifiers == nil || len(res.Modifiers) != 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestApiDerivationModifiersSortByZoneWithinID(t *testing.T) {
	t.Cleanup(func() { derivationModifierSnapshot.Store(nil) })
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	mk := func(zone string) *derivationModifier {
		return withSpread(&derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 959, Form: 3272, Strong: true},
			Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Zone: zone, Status: modConfirmed,
			Start: start, InCells: zoneWideIn(), Outside: map[s2.CellID]struct{}{}})
	}
	mods := []*derivationModifier{mk("Europe/Paris"), mk("America/New_York"), mk("Europe/London")}
	derivationModifierSnapshot.Store(&mods)
	res := GetApiDerivationModifiers(start)
	want := []string{"America/New_York", "Europe/London", "Europe/Paris"}
	if len(res.Modifiers) != 3 {
		t.Fatalf("got %+v", res.Modifiers)
	}
	for i, z := range want {
		if res.Modifiers[i].Region.Zone != z || res.Modifiers[i].ID != res.Modifiers[0].ID {
			t.Fatalf("modifier %d = %s %s, want zone %s", i, res.Modifiers[i].ID, res.Modifiers[i].Region.Zone, z)
		}
	}
}

func TestApiDerivationModifiersRuntimeZoneWideCells(t *testing.T) {
	prevCfg := config.Config.DerivationModifiers.ZoneWideCells
	prevRt, prevOn := derivationModifierRuntime, derivationModifierLearnerOn
	t.Cleanup(func() {
		config.Config.DerivationModifiers.ZoneWideCells = prevCfg
		derivationModifierRuntime, derivationModifierLearnerOn = prevRt, prevOn
		derivationModifierSnapshot.Store(nil)
	})
	config.Config.DerivationModifiers.ZoneWideCells = 3
	derivationModifierRuntime, derivationModifierLearnerOn = &modifierRuntime{zoneWideCells: 2}, true
	m := levelMod(modifierScope{PokemonID: 1}, 5, "Europe/London", cellSet(adjCellA, adjCellFar))
	m.Start = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	mods := []*derivationModifier{m}
	derivationModifierSnapshot.Store(&mods)

	if got := GetApiDerivationModifiers(m.Start).Modifiers[0].Region.Cells; got != nil {
		t.Fatalf("2 in-cells with runtime zoneWideCells 2 should be zone-wide, got cells %v", got)
	}
	derivationModifierRuntime = nil
	if got := GetApiDerivationModifiers(m.Start).Modifiers[0].Region.Cells; len(got) != 2 {
		t.Fatalf("config fallback (3) should list the 2 cells, got %v", got)
	}
}

func TestApiStatusDerivationModifiersFeature(t *testing.T) {
	prev := derivationModifierLearnerOn
	t.Cleanup(func() { derivationModifierLearnerOn = prev })
	derivationModifierLearnerOn = true
	if !GetApiStatus().Features.DerivationModifiers {
		t.Fatal("features.derivation_modifiers should be true when the learner runs")
	}
	derivationModifierLearnerOn = false
	if GetApiStatus().Features.DerivationModifiers {
		t.Fatal("features.derivation_modifiers should be false when the learner is off")
	}
}
