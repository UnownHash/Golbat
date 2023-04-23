package decoder

import (
	"fmt"
	"github.com/antonmedv/expr"
	"testing"
)

func TestExpertFilter(t *testing.T) {
	defaultEnv := filterEnv{Pokemon: &PokemonLookup{}, Pvp: &emptyPvp}
	tests := []struct {
		filter   string
		env      *filterEnv
		expected bool
	}{
		{"", &defaultEnv, true},
		{"0-100", &defaultEnv, true},
		{"!0", &defaultEnv, false},
		{"a0-1,!a0", &defaultEnv, true},
		{"(a0|a15)&d15&s14-15,0&l1,gl1,ul1,lc1", &defaultEnv, false},
	}
	expertCache := make(expertFilterCache)
	for _, test := range tests {
		t.Run(fmt.Sprintf("ExpertFilter %s", test.filter), func(t *testing.T) {
			compiled := compilePokemonFilter(expertCache, test.filter)
			if compiled == nil {
				t.Errorf("Failed to compile 0-100")
				return
			}
			if output, err := expr.Run(compiled, test.env); err != nil || output.(bool) != test.expected {
				t.Errorf("Failed to match: %v %s", output, err)
			}
		})
	}
}
