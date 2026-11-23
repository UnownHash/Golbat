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
	pin := &derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 959, Form: 3272, Strong: true},
		Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Zone: "Europe/London", Status: modConfirmed,
		Start: start, Observations: 14, InCells: zoneWideIn(), Outside: map[s2.CellID]struct{}{}}
	size := &derivationModifier{Type: modSizeCutoffs, Scope: modifierScope{PokemonID: 1},
		Boundaries: map[string]sizeBoundary{"1/2": {Lo: 0.5, HasLo: true}},
		Zone:       "Europe/London", Status: modCandidate, Start: start.Add(time.Hour),
		InCells: cellSet(adjCellA.Next(), adjCellA), Outside: cellSet(adjCellA.Prev())}
	mods := []*derivationModifier{size, pin}
	derivationModifierSnapshot.Store(&mods)

	res := GetApiDerivationModifiers(time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC))
	if res.Version != 1 || res.GeneratedAt != "2026-11-17T12:00:00Z" || len(res.Modifiers) != 2 {
		t.Fatalf("got %+v", res)
	}
	p, s := res.Modifiers[0], res.Modifiers[1]
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
