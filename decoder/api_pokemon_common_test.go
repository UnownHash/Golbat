package decoder

import (
	"testing"

	"golbat/config"
)

func TestPokemonScanLimitReached(t *testing.T) {
	previousLimit := config.Config.Tuning.MaxPokemonResults
	config.Config.Tuning.MaxPokemonResults = 100
	t.Cleanup(func() {
		config.Config.Tuning.MaxPokemonResults = previousLimit
	})

	tests := []struct {
		name         string
		requestLimit int
		resultCount  int
		want         bool
	}{
		{name: "below requested limit", requestLimit: 10, resultCount: 9, want: false},
		{name: "at requested limit", requestLimit: 10, resultCount: 10, want: true},
		{name: "above requested limit", requestLimit: 10, resultCount: 11, want: true},
		{name: "below server default", requestLimit: 0, resultCount: 99, want: false},
		{name: "at server default", requestLimit: 0, resultCount: 100, want: true},
		{name: "requested limit capped by server", requestLimit: 200, resultCount: 100, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ApiPokemonScan3{Limit: tt.requestLimit}
			if got := pokemonScanLimitReached(req, tt.resultCount); got != tt.want {
				t.Errorf("pokemonScanLimitReached() = %v, want %v", got, tt.want)
			}
		})
	}
}
