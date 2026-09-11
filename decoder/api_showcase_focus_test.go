package decoder

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseShowcaseFocusCanonicalizesAndProjectsBuddyLevel(t *testing.T) {
	focus, level, err := parseShowcaseFocus(`{ "type": "buddy", "min_level": 3 }`)
	if err != nil {
		t.Fatalf("parse focus: %v", err)
	}
	if level != 3 {
		t.Fatalf("Buddy level = %d, want 3", level)
	}
	if got, want := string(*focus), `{"min_level":3,"type":"buddy"}`; got != want {
		t.Fatalf("canonical focus = %s, want %s", got, want)
	}

	reordered, reorderedLevel, err := parseShowcaseFocus(`{"min_level":3,"type":"buddy"}`)
	if err != nil {
		t.Fatalf("parse reordered focus: %v", err)
	}
	if *reordered != *focus || reorderedLevel != level {
		t.Fatalf("equivalent objects diverged: (%s, %d) vs (%s, %d)", *focus, level, *reordered, reorderedLevel)
	}

	raw, err := json.Marshal(focus)
	if err != nil {
		t.Fatalf("marshal focus: %v", err)
	}
	if len(raw) == 0 || raw[0] != '{' || strings.Contains(string(raw), `"{\"`) {
		t.Fatalf("focus must marshal as a native object, got %s", raw)
	}
}

func TestParseShowcaseFocusPreservesNonBuddyFocus(t *testing.T) {
	focus, level, err := parseShowcaseFocus(`{"pokemon_id":25,"type":"pokemon"}`)
	if err != nil {
		t.Fatalf("parse focus: %v", err)
	}
	if focus == nil || level != 0 {
		t.Fatalf("non-Buddy focus = (%v, %d), want a focus with no Buddy projection", focus, level)
	}
}

func TestParseShowcaseFocusRejectsMalformedValues(t *testing.T) {
	tests := map[string]string{
		"invalid JSON":        `{`,
		"null":                `null`,
		"array":               `[]`,
		"missing type":        `{"min_level":3}`,
		"non-string type":     `{"type":3,"min_level":3}`,
		"missing Buddy level": `{"type":"buddy"}`,
		"zero Buddy level":    `{"type":"buddy","min_level":0}`,
		"fractional level":    `{"type":"buddy","min_level":3.5}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if focus, _, err := parseShowcaseFocus(raw); err == nil || focus != nil {
				t.Fatalf("parseShowcaseFocus(%s) = (%v, %v), want nil + error", raw, focus, err)
			}
		})
	}
}

func TestPokestopSelectColumnsIncludesShowcaseFocus(t *testing.T) {
	if !strings.Contains(pokestopSelectColumns, "description, showcase_focus, showcase_pokemon_id") {
		t.Fatalf("pokestopSelectColumns must load showcase_focus for cache misses and preload: %s", pokestopSelectColumns)
	}
}
