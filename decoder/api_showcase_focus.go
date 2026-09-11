package decoder

import (
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// ApiShowcaseFocus is the canonical JSON representation Golbat persists for a
// showcase focus. It remains a comparable string internally so the maintained
// availability index can use it as a key, while MarshalJSON exposes the focus
// as a native object on the API.
type ApiShowcaseFocus string

// MarshalJSON emits the canonical focus object rather than a quoted JSON
// string. ApiShowcaseFocus values are created by parseShowcaseFocus, but keep a
// validity check here so a zero or manually-constructed invalid value can never
// corrupt an API response.
func (f ApiShowcaseFocus) MarshalJSON() ([]byte, error) {
	if f == "" {
		return []byte("null"), nil
	}
	raw := []byte(f)
	if !json.Valid(raw) || raw[0] != '{' {
		return nil, fmt.Errorf("showcase focus must be a JSON object")
	}
	return raw, nil
}

// Schema documents the stored flat focus object. Different contest focus
// types carry different attributes, so additional properties are intentional.
func (ApiShowcaseFocus) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:                 huma.TypeObject,
		Description:          "Structured showcase focus. For Buddy showcases, type is buddy and min_level is the required Buddy level.",
		AdditionalProperties: true,
		Properties: map[string]*huma.Schema{
			"type": {
				Type:        huma.TypeString,
				Description: "Focus type (pokemon, type, alignment, class, family, buddy, generation, hatched, mega, or shiny)",
			},
			"min_level": {
				Type:        huma.TypeInteger,
				Format:      "int32",
				Description: "Minimum Buddy level when type is buddy",
			},
		},
		Required: []string{"type"},
		Examples: []any{
			map[string]any{"type": "buddy", "min_level": 3},
		},
	}
}

// parseShowcaseFocus validates and canonicalizes the stored focus once when a
// Pokestop enters the fort lookup. The returned Buddy projection is the only
// focus data needed on the hot scan path; the canonical object is retained only
// by the much smaller active-showcase availability index.
func parseShowcaseFocus(raw string) (*ApiShowcaseFocus, int8, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, 0, fmt.Errorf("decode showcase focus: %w", err)
	}
	if value == nil {
		return nil, 0, fmt.Errorf("showcase focus must be an object")
	}

	var focusType string
	if typeRaw, ok := value["type"]; !ok {
		return nil, 0, fmt.Errorf("showcase focus is missing type")
	} else if err := json.Unmarshal(typeRaw, &focusType); err != nil || focusType == "" {
		return nil, 0, fmt.Errorf("showcase focus type must be a non-empty string")
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, 0, fmt.Errorf("encode showcase focus: %w", err)
	}
	focus := ApiShowcaseFocus(canonical)

	if contestFocusType(focusType) != focusBuddy {
		return &focus, 0, nil
	}

	var minLevel int8
	levelRaw, ok := value["min_level"]
	if !ok {
		return nil, 0, fmt.Errorf("buddy showcase focus is missing min_level")
	}
	if err := json.Unmarshal(levelRaw, &minLevel); err != nil || minLevel <= 0 {
		return nil, 0, fmt.Errorf("buddy showcase min_level must be a positive int8")
	}
	return &focus, minLevel, nil
}
