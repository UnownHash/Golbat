package decoder

import "testing"

func int64p(v int64) *int64 { return &v }

// The answering side's whole correctness obligation: a record only answers a
// question it actually describes. Encounter ids are reused across spawn
// mutations, and IVs are rolled per boost state, so species, form and weather
// all have to agree.
func TestPeerRecordMatches(t *testing.T) {
	base := func() *ApiPokemonResult {
		return &ApiPokemonResult{PokemonId: 25, Form: int64p(1), Weather: int64p(3)}
	}

	tests := []struct {
		name                    string
		record                  *ApiPokemonResult
		pokemonId, form, weathr int32
		want                    bool
	}{
		{"everything agrees", base(), 25, 1, 3, true},
		{"species moved on", base(), 26, 1, 3, false},
		{"form moved on", base(), 25, 2, 3, false},
		{"boost state differs", base(), 25, 1, 0, false},
		{
			// A record that never learned its form makes no claim about it.
			"unknown form is not a mismatch",
			&ApiPokemonResult{PokemonId: 25, Weather: int64p(3)},
			25, 7, 3, true,
		},
		{
			// Likewise weather: an unset value is absence of a claim, not a
			// claim of NONE. This is what keeps the check safe for a peer
			// that computes an answer rather than holding one.
			"unknown weather is not a mismatch",
			&ApiPokemonResult{PokemonId: 25, Form: int64p(1)},
			25, 1, 7, true,
		},
		{"a miss is never a match", nil, 25, 1, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PeerRecordMatches(tt.record, tt.pokemonId, tt.form, tt.weathr); got != tt.want {
				t.Fatalf("PeerRecordMatches = %v, want %v", got, tt.want)
			}
		})
	}
}

// The specific failure this guards, spelled out because it is the one the
// suite could not previously see: weather NONE (0) is the zero value of the
// request field, so a check that only compares species and form treats a
// record held under a boosted weather as a valid answer to an unboosted
// question.
func TestPeerRecordMatchesRejectsStatsRolledUnderAnotherWeather(t *testing.T) {
	boosted := &ApiPokemonResult{
		PokemonId: 25,
		Form:      int64p(0),
		Weather:   int64p(3), // still holds the pre-flip, boosted roll
		AtkIv:     int64p(15),
		DefIv:     int64p(15),
		StaIv:     int64p(15),
	}

	if PeerRecordMatches(boosted, 25, 0, 0) {
		t.Fatal("a record rolled under a different boost state must not answer this question")
	}
}
