package decoder

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"golbat/config"
)

// ApiDerivationModifiers is the shareable snapshot of the active derivation
// modifiers (design 2026-11-15 §6, plus region).
type ApiDerivationModifiers struct {
	Version     int                     `json:"version" doc:"Format version (1)"`
	GeneratedAt string                  `json:"generated_at" doc:"RFC 3339 UTC time the snapshot was taken"`
	Modifiers   []ApiDerivationModifier `json:"modifiers" doc:"Active (candidate and confirmed) modifiers sorted by id; empty when the learner is off"`
}

type ApiDerivationModifier struct {
	ID       string              `json:"id" doc:"<start date>-<type>-<scope>-<effect>"`
	Scope    ApiModifierScope    `json:"scope"`
	Window   ApiModifierWindow   `json:"window"`
	Effect   map[string]any      `json:"effect" doc:"level_offset {type, delta}, move_pin {type, slot, move} or size_cutoffs {type, boundaries: {cut: [lo, hi]}} with null for an unbounded end, or unexplained {type}"`
	Status   string              `json:"status" doc:"candidate or confirmed"`
	Evidence ApiModifierEvidence `json:"evidence"`
	Region   ApiModifierRegion   `json:"region"`
}

type ApiModifierScope struct {
	Strong  bool                 `json:"strong"`
	Pokemon []ApiModifierPokemon `json:"pokemon,omitempty" doc:"Absent for a modifier covering every pokemon of the strong class"`
}

type ApiModifierPokemon struct {
	ID   int32 `json:"id"`
	Form int32 `json:"form"`
}

type ApiModifierWindow struct {
	Start     string  `json:"start" doc:"RFC 3339 UTC"`
	End       *string `json:"end" doc:"Always null: ended modifiers are not listed"`
	TimeBasis string  `json:"time_basis"`
}

type ApiModifierEvidence struct {
	Observations   int `json:"observations"`
	Contradictions int `json:"contradictions"`
}

// ApiModifierRegion is where the modifier applies: a zone, and its level-14
// cells unless it covers the whole zone (cells null).
type ApiModifierRegion struct {
	Zone  string   `json:"zone"`
	Cells []string `json:"cells" doc:"Level-14 S2 cell tokens, sorted; null when the modifier is zone-wide"`
}

const apiModifierTimeFormat = "2006-01-02T15:04:05Z"

func apiModifierID(m *derivationModifier) string {
	var scope string
	if m.Scope.AllStrong {
		scope = "all"
		if m.Scope.Strong {
			scope = "all-strong"
		}
	} else {
		scope = fmt.Sprintf("%d-%d", m.Scope.PokemonID, m.Scope.Form)
		if m.Scope.Strong {
			scope = "strong-" + scope
		}
	}
	effect := ""
	switch m.Type {
	case modMovePin:
		effect = fmt.Sprintf("%s-%d", m.Effect.Slot, m.Effect.Move)
	case modLevelOffset:
		effect = fmt.Sprintf("delta%+d", m.Effect.Delta)
	}
	parts := []string{m.Start.UTC().Format("2006-01-02"), m.Type.String(), scope}
	if effect != "" {
		parts = append(parts, effect)
	}
	return strings.Join(parts, "-")
}

func apiModifierEffect(m *derivationModifier) map[string]any {
	e := map[string]any{"type": m.Type.String()}
	switch m.Type {
	case modLevelOffset:
		e["delta"] = m.Effect.Delta
	case modMovePin:
		e["slot"] = m.Effect.Slot
		e["move"] = m.Effect.Move
	case modSizeCutoffs:
		b := make(map[string][2]*float64, len(m.Boundaries))
		for name, sb := range m.Boundaries {
			var lo, hi *float64
			if sb.HasLo {
				v := sb.Lo
				lo = &v
			}
			if sb.HasHi {
				v := sb.Hi
				hi = &v
			}
			b[name] = [2]*float64{lo, hi}
		}
		e["boundaries"] = b
	}
	return e
}

// GetApiDerivationModifiers lists the active modifiers as of now. With the
// learner off it returns an empty list.
func GetApiDerivationModifiers(now time.Time) *ApiDerivationModifiers {
	out := &ApiDerivationModifiers{Version: 1, GeneratedAt: now.UTC().Format(apiModifierTimeFormat),
		Modifiers: []ApiDerivationModifier{}}
	zoneWide := config.Config.DerivationModifiers.ZoneWideCells
	if r := derivationModifierRuntime; r != nil {
		zoneWide = r.zoneWideCells
	}
	for _, m := range activeDerivationModifiers() {
		am := ApiDerivationModifier{
			ID:     apiModifierID(m),
			Scope:  ApiModifierScope{Strong: m.Scope.Strong},
			Window: ApiModifierWindow{Start: m.Start.UTC().Format(apiModifierTimeFormat), TimeBasis: "utc"},
			Effect: apiModifierEffect(m),
			Status: m.Status.String(),
			Evidence: ApiModifierEvidence{Observations: m.Observations,
				Contradictions: m.Contradictions},
			Region: ApiModifierRegion{Zone: m.Zone},
		}
		if !m.Scope.AllStrong {
			am.Scope.Pokemon = []ApiModifierPokemon{{ID: m.Scope.PokemonID, Form: m.Scope.Form}}
		}
		if !m.ZoneWide(zoneWide) {
			am.Region.Cells = make([]string, 0, len(m.InCells))
			for c := range m.InCells {
				am.Region.Cells = append(am.Region.Cells, c.ToToken())
			}
			slices.Sort(am.Region.Cells)
		}
		out.Modifiers = append(out.Modifiers, am)
	}
	slices.SortFunc(out.Modifiers, func(a, b ApiDerivationModifier) int { 
		return cmp.Or(strings.Compare(a.ID, b.ID), strings.Compare(a.Region.Zone, b.Region.Zone))
	})
	return out
}
