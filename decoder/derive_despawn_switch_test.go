package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/config"
	"golbat/db"
)

func withDeriveDespawn(t *testing.T, enabled bool) {
	t.Helper()
	prev := config.Config.DeriveDespawn
	config.Config.DeriveDespawn = enabled
	t.Cleanup(func() { config.Config.DeriveDespawn = prev })
}

// With derive_despawn off, a pokemon at a spawnpoint with no observed despawn
// second gets the 20-minute guess, unverified, and is cached as a guess.
func TestDeriveDespawnOffGivesAPokemonTheGuess(t *testing.T) {
	withDeriveDespawn(t, false)
	const spawnID = int64(0x2b7c1a9d0f1)
	seedCachedSpawnpoint(t, spawnID, null.Int{})
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)

	p := &Pokemon{}
	p.SetSpawnId(null.IntFrom(spawnID))
	p.setExpireTimestampFromSpawnpoint(context.Background(), db.DbDetails{}, now.UnixMilli(), true)

	if want := now.Unix() + 20*60; int64(p.ExpireTimestamp.ValueOrZero()) != want {
		t.Errorf("expiry = %d, want the 20-minute guess %d", p.ExpireTimestamp.ValueOrZero(), want)
	}
	if p.ExpireTimestampVerified {
		t.Error("a guessed expiry must not be verified")
	}
	if p.hasSpawnpointExpiry() {
		t.Error("with derive_despawn off an unverified expiry is a guess, not a spawnpoint expiry")
	}
}

// An observed despawn second is used whether or not derive_despawn is on.
func TestDeriveDespawnOffStillUsesAnObservedDespawn(t *testing.T) {
	withDeriveDespawn(t, false)
	const spawnID = int64(0x2b7c1a9d0f3)
	seedCachedSpawnpoint(t, spawnID, null.IntFrom(1500))
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	p := &Pokemon{}
	p.SetSpawnId(null.IntFrom(spawnID))
	p.setExpireTimestampFromSpawnpoint(context.Background(), db.DbDetails{}, now.UnixMilli(), true)

	if want := now.Unix() + 1500; int64(p.ExpireTimestamp.ValueOrZero()) != want || !p.ExpireTimestampVerified {
		t.Errorf("expiry = %d verified=%v, want %d verified", p.ExpireTimestamp.ValueOrZero(), p.ExpireTimestampVerified, want)
	}
	if !p.hasSpawnpointExpiry() {
		t.Error("a verified expiry is a spawnpoint expiry")
	}
}

// With derive_despawn off, a tappable at a spawnpoint with no observed
// despawn second gets the 20-minute guess, unverified.
func TestDeriveDespawnOffGivesATappableTheGuess(t *testing.T) {
	withDeriveDespawn(t, false)
	const spawnID = int64(0x2b7c1a9d0f5)
	seedCachedSpawnpoint(t, spawnID, null.Int{})
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)

	ta := &Tappable{}
	ta.SpawnId = null.IntFrom(spawnID)
	ta.setExpireTimestamp(context.Background(), db.DbDetails{}, now.UnixMilli())

	if want := now.Unix() + 20*60; ta.ExpireTimestamp.Int64 != want {
		t.Errorf("tappable expiry = %d, want the 20-minute guess %d", ta.ExpireTimestamp.Int64, want)
	}
	if ta.ExpireTimestampVerified {
		t.Error("a guessed tappable expiry must not be verified")
	}
}
