package decoder

import (
	"testing"
	"time"

	"golbat/derive/despawn"
	"golbat/stats_collector"
)

// despawnCountingCollector embeds the noop collector so every other method
// keeps its normal no-op behavior, and counts the wrap-clamp counter, which
// the noop collector discards.
type despawnCountingCollector struct {
	stats_collector.StatsCollector
	wrapClamped int
}

func (c *despawnCountingCollector) IncDespawnWrapClamped() { c.wrapClamped++ }

func withDespawnCountingCollector(t *testing.T) *despawnCountingCollector {
	t.Helper()
	fake := &despawnCountingCollector{StatsCollector: stats_collector.NewNoopStatsCollector()}
	setStatsCollectorForTest(t, fake)
	return fake
}

// The derived expiry must be the next occurrence of this spawnpoint's phase,
// and unverified: it is exact for half-hour spawnpoints and half an hour early
// for the rest, which a later sighting corrects.
func TestApplyDerivedDespawnUsesNextOccurrence(t *testing.T) {
	const spawnID = int64(0x47b973e0595)
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)

	p := &Pokemon{}
	p.applyDerivedDespawn(spawnID, now.UnixMilli(), true)

	want := despawn.NextOccurrence(spawnID, now).Unix()
	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != want {
		t.Fatalf("expiry: got %d want %d", got, want)
	}
	if p.ExpireTimestampVerified {
		t.Fatal("a derived despawn must not be verified")
	}
}

// The result is the next occurrence counting one up to derivedDespawnTolerance
// in the past as current, so it lies after now minus that tolerance and within
// a half hour of it.
func TestApplyDerivedDespawnIsAheadAndWithinHalfHour(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 17, 33, 0, time.UTC)
	nowSec := now.Unix()

	// Odd ids only: every real spawn id is odd, and the derivation is defined
	// only for those.
	for id := int64(1); id < 3000; id += 2 {
		p := &Pokemon{}
		p.applyDerivedDespawn(id, now.UnixMilli(), true)
		got := int64(p.ExpireTimestamp.ValueOrZero())
		from := nowSec - derivedDespawnTolerance
		if got <= from {
			t.Fatalf("id %d: expiry %d is not after %d", id, got, from)
		}
		if got-from > 1800 {
			t.Fatalf("id %d: expiry %d is more than a half hour after %d", id, got, from)
		}
	}
}

// A sighting timestamped just past the true despawn second wraps +3600 and
// grants a phantom hour. No encounter lives longer than 3600s, so the wrapped
// expiry is always provably impossible against FirstSeenTimestamp.
func TestApplyVerifiedDespawnClampsPhantomHour(t *testing.T) {
	fake := withDespawnCountingCollector(t)

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	// Despawn second-of-hour 3599 (59:59) — one second before "now" (00:00).
	// First seen 20 minutes ago, so the naive wrap implies a 4799s lifetime.
	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: uint32(nowSec - 1200)}}
	p.applyVerifiedDespawn(3599, now.UnixMilli())

	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != nowSec-1 {
		t.Fatalf("expected clamped expiry %d (1s ago), got %d (+%ds)", nowSec-1, got, got-nowSec)
	}
	if !p.ExpireTimestampVerified {
		t.Fatal("a clamped despawn is still verified")
	}
	if fake.wrapClamped != 1 {
		t.Fatalf("IncDespawnWrapClamped count = %d, want 1", fake.wrapClamped)
	}
}

// FirstSeenTimestamp is set only in savePokemonRecordAsAtTime, so it is 0
// during a new record's first decode. The guard must not fire there.
func TestApplyVerifiedDespawnSkipsClampForNewRecord(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: 0}}
	p.applyVerifiedDespawn(3599, now.UnixMilli())

	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != nowSec+3599 {
		t.Fatalf("new record must keep the unclamped expiry %d, got %d", nowSec+3599, got)
	}
}

// An ordinary forward despawn must be untouched.
func TestApplyVerifiedDespawnLeavesNormalExpiryAlone(t *testing.T) {
	fake := withDespawnCountingCollector(t)

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	// Despawn at 12:10:00 -> second-of-hour 600, 600s away.
	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: uint32(nowSec - 300)}}
	p.applyVerifiedDespawn(600, now.UnixMilli())

	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != nowSec+600 {
		t.Fatalf("expected %d, got %d", nowSec+600, got)
	}
	if fake.wrapClamped != 0 {
		t.Fatalf("IncDespawnWrapClamped count = %d, want 0", fake.wrapClamped)
	}
}

// The phantom-hour clamp corrects an expiry computed a full hour too far. It
// must not fire when the inputs simply come from clocks that disagree.
//
// expiry derives from the caller's timestamp (the scanner's, on a GMO path)
// while firstSeen was stamped by whoever created the record — the scanner's
// clock from a GMO, this host's time.Now() from an encounter or tappable. If
// those differ by more than an hour's worth, the difference looks exactly
// like a phantom hour, and clamping would move a Pokemon with most of its
// life left to an expiry an hour in the past: published as despawned, dropped
// to the corpse TTL, while still catchable.
func TestResolveDespawnExpiryDoesNotClampFarIntoThePast(t *testing.T) {
	const nowSec = int64(1787486400)

	// A record whose firstSeen was stamped by a clock well behind the one
	// that produced nowSec. despawnSecond puts the true expiry comfortably in
	// the future, so nothing here is phantom.
	despawnSecond := despawnSecondOfHour(nowSec + 1800)
	firstSeen := nowSec - 7200

	expiry, clamped := resolveDespawnExpiry(despawnSecond, nowSec, firstSeen)

	if clamped {
		t.Errorf("clamped a live Pokemon's expiry to %d (%ds relative to now) — "+
			"a clock disagreement is not a phantom hour", expiry, expiry-nowSec)
	}
	if expiry <= nowSec {
		t.Errorf("expiry = %d is not in the future relative to now %d", expiry, nowSec)
	}
}
