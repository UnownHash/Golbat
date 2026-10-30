package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"

	"github.com/guregu/null/v6"
)

// A sighting timestamped just past the true despawn second wraps +3600 and
// grants a phantom hour, as it does for Pokemon.applyVerifiedDespawn.
// Tappable.applyVerifiedDespawn shares resolveDespawnExpiry with it, so this
// is the direct analogue of TestApplyVerifiedDespawnClampsPhantomHour
// (derive_despawn_test.go) for the tappable path.
func TestTappableApplyVerifiedDespawnClampsPhantomHour(t *testing.T) {
	fake := withDespawnCountingCollector(t)

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	// Despawn second-of-hour 3599 (59:59) — one second before "now" (00:00).
	// firstSeen 20 minutes ago, so the naive wrap implies a 4799s lifetime.
	ta := &Tappable{firstSeen: nowSec - 1200}
	ta.applyVerifiedDespawn(3599, now.UnixMilli())

	if got := ta.ExpireTimestamp.ValueOrZero(); got != nowSec-1 {
		t.Fatalf("expected clamped expiry %d (1s ago), got %d (+%ds)", nowSec-1, got, got-nowSec)
	}
	if !ta.ExpireTimestampVerified {
		t.Fatal("a clamped despawn is still verified")
	}
	if fake.wrapClamped != 1 {
		t.Fatalf("IncDespawnWrapClamped count = %d, want 1", fake.wrapClamped)
	}
}

// A tappable with no in-memory firstSeen (the common case: this process
// never created the record fresh — see Tappable.firstSeen's doc comment)
// must keep the unclamped, phantom-hour expiry rather than guess. That
// degradation is accepted; this guards against a zero firstSeen ever being
// treated as a real lower bound on the tappable's lifetime.
func TestTappableApplyVerifiedDespawnSkipsClampWithNoFirstSeen(t *testing.T) {
	fake := withDespawnCountingCollector(t)

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	ta := &Tappable{} // firstSeen zero value: no lower bound available
	ta.applyVerifiedDespawn(3599, now.UnixMilli())

	if got := ta.ExpireTimestamp.ValueOrZero(); got != nowSec+3599 {
		t.Fatalf("tappable with no firstSeen must keep the unclamped expiry %d, got %d", nowSec+3599, got)
	}
	if fake.wrapClamped != 0 {
		t.Fatalf("IncDespawnWrapClamped count = %d, want 0", fake.wrapClamped)
	}
}

// An ordinary forward despawn — the ubiquitous case — must be untouched by
// the clamp regardless of firstSeen being known.
func TestTappableApplyVerifiedDespawnLeavesNormalExpiryAlone(t *testing.T) {
	fake := withDespawnCountingCollector(t)

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	// Despawn at 12:10:00 -> second-of-hour 600, 600s away.
	ta := &Tappable{firstSeen: nowSec - 300}
	ta.applyVerifiedDespawn(600, now.UnixMilli())

	if got := ta.ExpireTimestamp.ValueOrZero(); got != nowSec+600 {
		t.Fatalf("expected %d, got %d", nowSec+600, got)
	}
	if !ta.ExpireTimestampVerified {
		t.Fatal("a normal despawn must still be marked verified")
	}
	if fake.wrapClamped != 0 {
		t.Fatalf("IncDespawnWrapClamped count = %d, want 0", fake.wrapClamped)
	}
}

// Re-confirming a corpse past its own already-verified expiry must not keep
// incrementing the shared counter once the clamp has already corrected the
// record once. This is the scenario of
// TestUpdatePokemonBatchStoredDespawnDoesNotReCountTheWrapClamp for the
// tappable path, which shares golbat_despawn_wrap_clamped_total with Pokemon:
// a normal forward decode establishes a real expiry, then a second decode 60s
// past it recomputes via the wrapped branch and clamps back to that SAME
// value — correcting nothing, so it must not count again.
func TestTappableApplyVerifiedDespawnDoesNotReCountAnUnchangedClamp(t *testing.T) {
	fake := withDespawnCountingCollector(t)

	const despawnSecond = 1500
	first := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC) // second-of-hour 1053

	// firstSeen fixed at this tappable's genesis, as in production (set once
	// in getOrCreateTappableRecord and never touched again).
	ta := &Tappable{firstSeen: first.Unix()}

	// First decode: despawnOffset = 1500-1053 = 447, no wrap, no clamp.
	ta.applyVerifiedDespawn(despawnSecond, first.UnixMilli())
	if fake.wrapClamped != 0 {
		t.Fatalf("first decode clamped %d times, want 0", fake.wrapClamped)
	}
	storedExpiry := ta.ExpireTimestamp.ValueOrZero()

	// Second decode, 60s past the stored expiry: wraps, then clamps back to
	// exactly storedExpiry.
	second := time.Unix(storedExpiry+60, 0)
	ta.applyVerifiedDespawn(despawnSecond, second.UnixMilli())

	if got := ta.ExpireTimestamp.ValueOrZero(); got != storedExpiry {
		t.Fatalf("re-decode past the stored expiry moved it: got %d want %d (the clamp must still correct the +3600 wrap)", got, storedExpiry)
	}
	if fake.wrapClamped != 0 {
		t.Fatalf("re-confirming an unchanged expiry incremented the counter %d time(s), want 0", fake.wrapClamped)
	}
}

// TestTappableSetExpireTimestampWiresPhantomHourThroughToClamp drives the
// clamp through the real production entry point — setExpireTimestamp, called
// from updateFromProcessTappableProto on every incoming
// ProcessTappableProto — rather than calling applyVerifiedDespawn directly,
// so it fails if setExpireTimestamp stops calling applyVerifiedDespawn, which
// the lower-level tests above cannot detect.
func TestTappableSetExpireTimestampWiresPhantomHourThroughToClamp(t *testing.T) {
	fake := withDespawnCountingCollector(t)

	const spawnID = int64(0x47b973e0595)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	seedCachedSpawnpoint(t, spawnID, null.IntFrom(3599))

	ta := &Tappable{firstSeen: nowSec - 1200}
	ta.SetSpawnId(null.IntFrom(spawnID))

	ta.setExpireTimestamp(context.Background(), db.DbDetails{}, now.UnixMilli())

	if got := ta.ExpireTimestamp.ValueOrZero(); got != nowSec-1 {
		t.Fatalf("expected clamped expiry %d (1s ago), got %d (+%ds)", nowSec-1, got, got-nowSec)
	}
	if !ta.ExpireTimestampVerified {
		t.Fatal("a clamped despawn is still verified")
	}
	if fake.wrapClamped != 1 {
		t.Fatalf("IncDespawnWrapClamped count = %d, want 1 — setExpireTimestamp may not be reaching "+
			"applyVerifiedDespawn/resolveDespawnExpiry", fake.wrapClamped)
	}
}
