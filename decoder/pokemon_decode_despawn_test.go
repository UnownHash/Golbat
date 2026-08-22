package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"

	"github.com/guregu/null/v6"
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

// Seeing a pokemon alive after the expiry its spawnpoint implies proves the
// despawn_sec wrong, whatever wrote it.
//
// Deliberately NOT the phantom-hour clamp's own numbers (despawn 3599,
// first_seen -1200s): that scenario clamps to exactly 1s in the past, which
// is inside despawnSkewMargin and TestApplyVerifiedDespawnClampsPhantomHour
// requires it to stay verified. contradicted is a pure function of the same
// three inputs as that test, so the same inputs cannot simultaneously prove
// "still verified" and "contradicted" - the two tests need different
// numbers. This case instead clamps to well outside the 5s margin: first_seen
// 4000s ago (already an anomaly by itself - no real encounter lives that
// long) with a despawn second implying up to 3000s from now, which the wrap
// clamp corrects back by a full hour.
//
// The expected expiry is derived the same way applyVerifiedDespawn computes
// it (local second-of-hour via time.Unix), not a hardcoded offset:
// nowSec-600 would silently assume secondOfHour(now)==0, true only in a
// whole-hour-offset timezone (review finding: this branch's Task 9 bug was
// exactly this UTC/local confusion - see CLAUDE.md's global constraints).
// despawnOffset+4000 always exceeds the wrap threshold (3605) regardless of
// timezone, so the clamp is guaranteed to fire here.
func TestApplyVerifiedDespawnReportsContradiction(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: nowSec - 4000}}

	contradicted := p.applyVerifiedDespawn(3000, now.UnixMilli())
	if !contradicted {
		t.Fatal("an expiry in the past for a live pokemon must be reported as contradicted")
	}

	local := time.Unix(nowSec, 0)
	secondOfHour := int64(local.Second() + local.Minute()*60)
	despawnOffset := int64(3000) - secondOfHour
	if despawnOffset < 0 {
		despawnOffset += 3600
	}
	wantExpiry := nowSec + despawnOffset - 3600 // the wrap clamp always fires for this scenario
	if got := p.ExpireTimestamp.ValueOrZero(); got != wantExpiry {
		t.Fatalf("expected clamped expiry %d, got %d", wantExpiry, got)
	}
	if p.ExpireTimestampVerified {
		t.Fatal("a contradicted despawn must not be left verified")
	}
}

func TestApplyVerifiedDespawnReportsNoContradictionNormally(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()

	p := &Pokemon{PokemonData: PokemonData{FirstSeenTimestamp: nowSec - 300}}

	if contradicted := p.applyVerifiedDespawn(600, now.UnixMilli()); contradicted {
		t.Fatal("a normal forward despawn is not a contradiction")
	}
}

// setExpireTimestampFromSpawnpoint has two code paths that both call
// applyVerifiedDespawn: the lock-free mirror fast path (spawnpoint synced in
// cache) and the locked getSpawnpointRecord fallback (mirror not yet
// synced). Both must act on a contradiction identically - extend the pokemon
// forward and queue the spawnpoint for despawn_correction.go's async clear.
//
// A Spawnpoint placed in cache via SetDespawnSec has a synced fast mirror
// (SetDespawnSec republishes it), driving the fast path. One constructed
// directly with DespawnSec set but never synced has despawnSecFast at its
// zero value (unsynced), driving the locked fallback - which still never
// touches a real DB, because getSpawnpointRecord's own cache check finds the
// same cached entry.
func TestSetExpireTimestampFromSpawnpointQueuesClearOnFastPathContradiction(t *testing.T) {
	oldQueue := despawnClearQueue
	t.Cleanup(func() { despawnClearQueue = oldQueue })
	despawnClearQueue = make(chan int64, 4)

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()
	const spawnId = int64(920401)

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId}}
	sp.SetDespawnSec(null.IntFrom(3000)) // also syncs the fast mirror
	spawnpointCache.Set(spawnId, sp, time.Minute)

	p := &Pokemon{PokemonData: PokemonData{
		SpawnId:            null.IntFrom(spawnId),
		FirstSeenTimestamp: nowSec - 4000,
	}}

	p.setExpireTimestampFromSpawnpoint(context.Background(), db.DbDetails{}, now.UnixMilli(), true)

	assertContradictionHandled(t, p, spawnId, nowSec)
}

func TestSetExpireTimestampFromSpawnpointQueuesClearOnLockedPathContradiction(t *testing.T) {
	oldQueue := despawnClearQueue
	t.Cleanup(func() { despawnClearQueue = oldQueue })
	despawnClearQueue = make(chan int64, 4)

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	nowSec := now.Unix()
	const spawnId = int64(920402)

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(3000)}}
	spawnpointCache.Set(spawnId, sp, time.Minute)

	p := &Pokemon{PokemonData: PokemonData{
		SpawnId:            null.IntFrom(spawnId),
		FirstSeenTimestamp: nowSec - 4000,
	}}

	p.setExpireTimestampFromSpawnpoint(context.Background(), db.DbDetails{}, now.UnixMilli(), true)

	assertContradictionHandled(t, p, spawnId, nowSec)
}

func assertContradictionHandled(t *testing.T, p *Pokemon, spawnId, nowSec int64) {
	t.Helper()
	if p.ExpireTimestampVerified {
		t.Fatal("a contradicted despawn must not be left verified")
	}
	if got := p.ExpireTimestamp.ValueOrZero(); got < nowSec {
		t.Fatalf("pokemon must be extended forward like an unknown spawnpoint, not left in the past: got %d (now=%d)", got, nowSec)
	}
	select {
	case gotId := <-despawnClearQueue:
		if gotId != spawnId {
			t.Fatalf("queued spawn id: got %d want %d", gotId, spawnId)
		}
	default:
		t.Fatal("expected the contradicted spawnpoint to be queued for a clear")
	}
}
