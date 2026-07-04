package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
)

func TestRemainingDurationVerifiedFutureDespawn(t *testing.T) {
	now := int64(1_000_000)
	p := &Pokemon{PokemonData: PokemonData{
		ExpireTimestampVerified: true,
		ExpireTimestamp:         null.IntFrom(now + 600),
	}}
	got := p.remainingDuration(now)
	if want := 660 * time.Second; got != want {
		t.Errorf("remainingDuration = %v, want %v", got, want)
	}
}

func TestRemainingDurationVerifiedPastDespawnClampsToOneMinute(t *testing.T) {
	now := int64(1_000_000)
	p := &Pokemon{PokemonData: PokemonData{
		ExpireTimestampVerified: true,
		ExpireTimestamp:         null.IntFrom(now - 300),
	}}
	if got := p.remainingDuration(now); got != time.Minute {
		t.Errorf("remainingDuration past despawn = %v, want 1m (was previously a fresh hour)", got)
	}
}

func TestRemainingDurationUnverifiedIsJittered(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{ExpireTimestampVerified: false}}
	seen := map[time.Duration]bool{}
	for range 200 {
		d := p.remainingDuration(0)
		if d < pokemonUnverifiedTTL || d >= pokemonUnverifiedTTL+pokemonUnverifiedTTLJitter {
			t.Fatalf("jittered TTL %v outside [%v, %v)", d, pokemonUnverifiedTTL, pokemonUnverifiedTTL+pokemonUnverifiedTTLJitter)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Errorf("expected jitter to produce varied TTLs, got %d distinct values", len(seen))
	}
}

func TestRemainingDurationVerifiedBoundary(t *testing.T) {
	now := int64(1_000_000)
	// timeLeft = 60+expire-now; exactly 60 clamps to a minute, 61 is honored.
	atBoundary := &Pokemon{PokemonData: PokemonData{ExpireTimestampVerified: true, ExpireTimestamp: null.IntFrom(now)}}
	if got := atBoundary.remainingDuration(now); got != time.Minute {
		t.Errorf("timeLeft=60 → %v, want 1m", got)
	}
	justOver := &Pokemon{PokemonData: PokemonData{ExpireTimestampVerified: true, ExpireTimestamp: null.IntFrom(now + 1)}}
	if got := justOver.remainingDuration(now); got != 61*time.Second {
		t.Errorf("timeLeft=61 → %v, want 61s", got)
	}
}
