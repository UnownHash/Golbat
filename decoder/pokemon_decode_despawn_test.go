package decoder

import (
	"testing"
	"time"
)

// A sighting timestamped just past the true despawn second wraps +3600 and
// grants a phantom hour. No encounter lives longer than 3600s, so the wrapped
// expiry is always provably impossible against FirstSeenTimestamp.
func TestApplyVerifiedDespawnClampsPhantomHour(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	// Despawn second-of-hour 3599 (59:59) — one second before "now" (00:00).
	// First seen 20 minutes ago, so the naive wrap implies a 4799s lifetime.
	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: nowSec - 1200}}
	p.applyVerifiedDespawn(3599, now.UnixMilli())

	if got := p.ExpireTimestamp.ValueOrZero(); got != nowSec-1 {
		t.Fatalf("expected clamped expiry %d (1s ago), got %d (+%ds)", nowSec-1, got, got-nowSec)
	}
	if !p.ExpireTimestampVerified {
		t.Fatal("a clamped despawn is still verified")
	}
}

// FirstSeenTimestamp is set only in savePokemonRecord, so it is 0 during a new
// record's first decode. The guard must not fire there.
func TestApplyVerifiedDespawnSkipsClampForNewRecord(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: 0}}
	p.applyVerifiedDespawn(3599, now.UnixMilli())

	if got := p.ExpireTimestamp.ValueOrZero(); got != nowSec+3599 {
		t.Fatalf("new record must keep the unclamped expiry %d, got %d", nowSec+3599, got)
	}
}

// An ordinary forward despawn must be untouched.
func TestApplyVerifiedDespawnLeavesNormalExpiryAlone(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	// Despawn at 12:10:00 -> second-of-hour 600, 600s away.
	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: nowSec - 300}}
	p.applyVerifiedDespawn(600, now.UnixMilli())

	if got := p.ExpireTimestamp.ValueOrZero(); got != nowSec+600 {
		t.Fatalf("expected %d, got %d", nowSec+600, got)
	}
}
