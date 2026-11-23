package decoder

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang/geo/s2"
)

func TestApiDerivationModifiersShape(t *testing.T) {
	defer derivationModifierSnapshot.Store(nil)

	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	pin := withSpread(&derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 959, Form: 3272, Strong: true},
		Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Zone: "Europe/London", Status: modConfirmed,
		Start: start, Observations: 14, Cells: zoneWideIn()})
	size := &derivationModifier{Type: modSizeCutoffs, Scope: modifierScope{PokemonID: 1},
		Boundaries: map[string]sizeBoundary{"1/2": {Lo: 0.5, HasLo: true}},
		Zone:       "Europe/London", Status: modCandidate, Start: start.Add(time.Hour),
		Cells: cellSet(adjCellA.Next(), adjCellA)}
	unexp := withSpread(&derivationModifier{Type: modUnexplained, Scope: modifierScope{PokemonID: 2, Form: 5, Strong: true},
		Zone: "Europe/London", Status: modCandidate, Start: start, Cells: zoneWideIn()})
	mods := []*derivationModifier{size, pin, unexp}
	storeModifierSnapshot(mods)

	res := GetApiDerivationModifiers(time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC))
	if res.Version != 1 || res.GeneratedAt != "2026-11-17T12:00:00Z" || len(res.Modifiers) != 3 {
		t.Fatalf("got %+v", res)
	}
	p, s, u := res.Modifiers[0], res.Modifiers[1], res.Modifiers[2]
	if u.ID != "2026-11-15-unexplained-strong-2-5" || len(u.Effect) != 1 || u.Effect["type"] != "unexplained" {
		t.Fatalf("unexplained %+v", u)
	}
	if p.ID != "2026-11-15-move_pin-strong-959-3272-charged-494" || !p.Region.ZoneWide || p.Status != "confirmed" {
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
			Start: start, Cells: zoneWideIn()})
	}
	mods := []*derivationModifier{mk("Europe/Paris"), mk("America/New_York"), mk("Europe/London")}
	storeModifierSnapshot(mods)
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

// applied is true only for a confirmed modifier the derive path applies:
// candidates and all-strong level offsets are listed but not applied.
func TestApiDerivationModifiersApplied(t *testing.T) {
	t.Cleanup(func() { derivationModifierSnapshot.Store(nil) })
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	species := levelMod(modifierScope{PokemonID: 1}, 5, "Europe/London", cellSet(adjCellA))
	all := levelMod(allStrongScope, 5, "Europe/London", cellSet(adjCellA))
	cand := levelMod(modifierScope{PokemonID: 2}, 5, "Europe/London", cellSet(adjCellA))
	cand.Status = modCandidate
	for _, m := range []*derivationModifier{species, all, cand} {
		m.Start = start
	}
	mods := []*derivationModifier{species, all, cand}
	storeModifierSnapshot(mods)
	got := map[string]bool{}
	for _, m := range GetApiDerivationModifiers(start).Modifiers {
		got[m.ID] = m.Applied
	}
	want := map[string]bool{
		"2026-11-15-level_offset-1-0-delta+5":        true,
		"2026-11-15-level_offset-all-strong-delta+5": false,
		"2026-11-15-level_offset-2-0-delta+5":        false,
	}
	for id, w := range want {
		if a, ok := got[id]; !ok || a != w {
			t.Errorf("%s applied = %v (listed %v), want %v", id, a, ok, w)
		}
	}
	b, _ := json.Marshal(GetApiDerivationModifiers(start))
	var back struct {
		Modifiers []map[string]any `json:"modifiers"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back.Modifiers[0]["applied"]; !ok {
		t.Errorf("applied missing from JSON: %s", b)
	}
}

// region: the zone, whether zone-wide, the covered venue cells and the
// excluded cells, both sorted and never null.
func TestApiDerivationModifiersRegion(t *testing.T) {
	t.Cleanup(func() { derivationModifierSnapshot.Store(nil) })
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	wide := wideLevelMod(5, []s2.CellID{adjCellA, adjCellA.Next()}, adjCellFar.Next(), adjCellFar)
	wide.Start = start
	venue := levelMod(modifierScope{PokemonID: 2}, 3, adjZoneA, cellSet(adjCellA))
	venue.Start = start
	storeModifierSnapshot([]*derivationModifier{wide, venue})
	b, err := json.Marshal(GetApiDerivationModifiers(start))
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Modifiers []struct {
			ID     string         `json:"id"`
			Region map[string]any `json:"region"`
		} `json:"modifiers"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	toks := func(cs ...s2.CellID) []any {
		out := make([]any, len(cs))
		for i, c := range cs {
			out[i] = c.ToToken()
		}
		slices.SortFunc(out, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
		return out
	}
	want := map[string]map[string]any{
		"2026-11-15-level_offset-1-0-delta+5": {"zone": adjZoneA, "zone_wide": true,
			"cells": toks(adjCellFar, adjCellFar.Next()), "excluded": toks(adjCellA, adjCellA.Next())},
		"2026-11-15-level_offset-2-0-delta+3": {"zone": adjZoneA, "zone_wide": false,
			"cells": toks(adjCellA), "excluded": []any{}},
	}
	if len(back.Modifiers) != len(want) {
		t.Fatalf("modifiers %s", b)
	}
	for _, m := range back.Modifiers {
		if !reflect.DeepEqual(m.Region, want[m.ID]) {
			t.Errorf("%s region %v, want %v", m.ID, m.Region, want[m.ID])
		}
	}
}

// Two strong species at the same level offset in one zone are summarised as
// one all-strong entry, never applied; it goes when one of them ends.
func TestApiDerivationModifiersAllStrongSummary(t *testing.T) {
	t.Cleanup(func() { derivationModifierSnapshot.Store(nil) })
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	mk := func(species int32, delta int32, status modifierStatus, strong bool, at time.Time) *derivationModifier {
		m := levelMod(modifierScope{PokemonID: species, Strong: strong}, delta, adjZoneA, cellSet(adjCellA))
		m.Status, m.Start, m.Observations = status, at, 10
		return m
	}
	a := mk(1, 5, modConfirmed, true, start.Add(time.Hour))
	b := mk(2, 5, modCandidate, true, start)
	others := []*derivationModifier{
		mk(3, 3, modConfirmed, true, start),  // another delta
		mk(4, 5, modConfirmed, false, start), // not strong
	}
	summary := func() []ApiDerivationModifier {
		var out []ApiDerivationModifier
		for _, m := range GetApiDerivationModifiers(start).Modifiers {
			if m.Scope.Pokemon == nil {
				out = append(out, m)
			}
		}
		return out
	}

	storeModifierSnapshot(append([]*derivationModifier{a, b}, others...))
	got := summary()
	if len(got) != 1 {
		t.Fatalf("summaries %+v, want one", got)
	}
	s := got[0]
	if s.ID != "2026-11-15-level_offset-all-strong-delta+5" || s.Applied || !s.Scope.Strong ||
		s.Status != "candidate" || s.Evidence.Observations != 20 || s.Region.Zone != adjZoneA {
		t.Errorf("summary %+v, want all-strong +5 candidate (one confirmed member), 20 observations, not applied", s)
	}

	b.Status = modConfirmed
	storeModifierSnapshot(append([]*derivationModifier{a, b}, others...))
	if got := summary(); len(got) != 1 || got[0].Status != "confirmed" {
		t.Errorf("summaries %+v, want one confirmed (two confirmed members)", got)
	}

	storeModifierSnapshot(append([]*derivationModifier{a}, others...))
	if got := summary(); len(got) != 0 {
		t.Errorf("summaries %+v after one member ended, want none", got)
	}
}
