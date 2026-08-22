package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"
	"golbat/decoder/writebehind"
	pb "golbat/grpc"
	"golbat/stats_collector"

	"github.com/guregu/null/v6"
)

// A verified peer answer may fill a NULL despawn_sec.
func TestPeerDespawnFillsEmptySpawnpoint(t *testing.T) {
	s := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 10}}

	applied := applyPeerDespawnToSpawnpoint(s, 1234)

	if !applied {
		t.Fatal("a NULL despawn_sec must accept a peer value")
	}
	if got := s.DespawnSec.ValueOrZero(); got != 1234 {
		t.Fatalf("despawn_sec: got %d want 1234", got)
	}
}

// Local TTH watched the pokemon to its end state. A peer must never overwrite it.
func TestPeerDespawnNeverOverwritesLocalValue(t *testing.T) {
	s := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 11, DespawnSec: null.IntFrom(600)}}

	applied := applyPeerDespawnToSpawnpoint(s, 1234)

	if applied {
		t.Fatal("a known despawn_sec must reject a peer value")
	}
	if got := s.DespawnSec.ValueOrZero(); got != 600 {
		t.Fatalf("local despawn_sec must survive, got %d", got)
	}
}

// Stats are adopted; cp and pvp are not - recomputeCpIfNeeded early-returns on
// Cp.Valid, so adopting a peer CP would suppress the local computation.
func TestApplyPeerStatsIgnoresPeerCp(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{}}
	peerCp := int64(9999)

	applyPeerStats(p, 15, 14, 13, 20, peerCp)

	if p.Cp.Valid {
		t.Fatalf("peer cp must not be adopted, got %d", p.Cp.ValueOrZero())
	}
	if p.AtkIv.ValueOrZero() != 15 || p.DefIv.ValueOrZero() != 14 || p.StaIv.ValueOrZero() != 13 {
		t.Fatalf("ivs not applied: %d/%d/%d",
			p.AtkIv.ValueOrZero(), p.DefIv.ValueOrZero(), p.StaIv.ValueOrZero())
	}
	if p.Level.ValueOrZero() != 20 {
		t.Fatalf("level: got %d want 20", p.Level.ValueOrZero())
	}
}

// --- applyPeerResult: the orchestration function itself. ---
//
// lureTestSetup (pokemon_lure_test.go) puts the package in PokemonMemoryOnly
// mode, which is what lets these exercise the real get/save plumbing without
// a database: savePokemonRecordAsAtTime's writeDB branch is gated on
// `!config.Config.PokemonMemoryOnly`. Spawnpoint has no equivalent memory-only
// escape hatch (confirmed: no existing test anywhere in decoder/ calls
// spawnpointUpdate), so spawnpoint-touching cases here are restricted to the
// path where the local value already exists and spawnpointUpdate is never
// reached - which is exactly rule 2, the one this task must prove.

// A peer that supplies IVs/level fills a pokemon that has none; its cp is
// dropped, not adopted.
func TestApplyPeerResultAdoptsStatsButNotCp(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(920201)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId), PokemonId: 25}}
	pokemonCache.Set(encId, p, time.Minute)

	atk, def, sta, level, cp, size := int64(15), int64(14), int64(13), int64(20), int64(9999), int64(3)
	res := &pb.PokemonResult{Id: encId, AtkIv: &atk, DefIv: &def, StaIv: &sta, Level: &level, Cp: &cp, Size: &size}
	item := peerLookupItem{EncounterId: encId, PokemonId: 25}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if p.AtkIv.ValueOrZero() != 15 || p.DefIv.ValueOrZero() != 14 || p.StaIv.ValueOrZero() != 13 {
		t.Fatalf("ivs not adopted: %d/%d/%d", p.AtkIv.ValueOrZero(), p.DefIv.ValueOrZero(), p.StaIv.ValueOrZero())
	}
	if p.Level.ValueOrZero() != 20 {
		t.Fatalf("level not adopted: %d", p.Level.ValueOrZero())
	}
	if p.Size.ValueOrZero() != 3 {
		t.Fatalf("size not adopted: %d", p.Size.ValueOrZero())
	}
	if p.Cp.Valid {
		t.Fatalf("peer cp must not be adopted, got %d", p.Cp.ValueOrZero())
	}
}

// A pokemon that already has stats must keep them - a peer answer is not
// allowed to overwrite a local IV read.
func TestApplyPeerResultDoesNotOverwriteExistingStats(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(920202)
	p := &Pokemon{PokemonData: PokemonData{
		Id: Uint64Str(encId), PokemonId: 6,
		AtkIv: null.IntFrom(1), DefIv: null.IntFrom(2), StaIv: null.IntFrom(3),
	}}
	pokemonCache.Set(encId, p, time.Minute)

	atk, def, sta, level := int64(15), int64(14), int64(13), int64(20)
	res := &pb.PokemonResult{Id: encId, AtkIv: &atk, DefIv: &def, StaIv: &sta, Level: &level}
	item := peerLookupItem{EncounterId: encId, PokemonId: 6}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if p.AtkIv.ValueOrZero() != 1 || p.DefIv.ValueOrZero() != 2 || p.StaIv.ValueOrZero() != 3 {
		t.Fatalf("existing ivs must survive, got %d/%d/%d", p.AtkIv.ValueOrZero(), p.DefIv.ValueOrZero(), p.StaIv.ValueOrZero())
	}
}

// Encounter ids are reused when the server mutates a spawn: if the cached
// pokemon's species no longer matches what was asked about, the record moved
// on between asking and answering and the answer must be dropped.
func TestApplyPeerResultSkipsWhenPokemonIdMoved(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(920203)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId), PokemonId: 1}}
	pokemonCache.Set(encId, p, time.Minute)

	atk, def, sta, level := int64(15), int64(14), int64(13), int64(20)
	res := &pb.PokemonResult{Id: encId, AtkIv: &atk, DefIv: &def, StaIv: &sta, Level: &level}
	item := peerLookupItem{EncounterId: encId, PokemonId: 99} // stale question

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if p.AtkIv.Valid {
		t.Fatalf("stats must not be applied to a record that moved on, got %d", p.AtkIv.ValueOrZero())
	}
}

// A pokemon with no verified expiry of its own may take a peer's future
// expiry as an unverified hint.
func TestApplyPeerResultHintUpdatesUnverifiedExpiry(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(920204)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId), PokemonId: 1}}
	pokemonCache.Set(encId, p, time.Minute)

	peerExpiry := time.Now().Unix() + 500
	res := &pb.PokemonResult{Id: encId, ExpireTimestamp: &peerExpiry}
	// SpawnId 0 skips the verified/spawnpoint block entirely, so any expiry
	// change here can only come from the unverified hint branch.
	item := peerLookupItem{EncounterId: encId, PokemonId: 1, SpawnId: 0}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if p.ExpireTimestampVerified {
		t.Fatal("a hint must not set verified")
	}
	if got := p.ExpireTimestamp.ValueOrZero(); got != peerExpiry {
		t.Fatalf("expire_timestamp hint: got %d want %d", got, peerExpiry)
	}
}

// A question whose pokemon is no longer cached (evicted, never existed) must
// return cleanly rather than panic.
func TestApplyPeerResultMissingPokemonReturnsCleanly(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(920205) // deliberately never cached

	atk, def, sta, level := int64(15), int64(14), int64(13), int64(20)
	res := &pb.PokemonResult{Id: encId, AtkIv: &atk, DefIv: &def, StaIv: &sta, Level: &level}
	item := peerLookupItem{EncounterId: encId, PokemonId: 1}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item) // must not panic
}

// End-to-end rule 2: a spawnpoint that already knows its despawn second must
// reject a peer's verified answer. A buggy implementation that called
// spawnpointUpdate unconditionally would panic here - spawnpointQueue and
// GeneralDb are both nil in this test binary - so reaching the assertions at
// all is itself evidence the write was skipped. Also proves both entity
// locks are released: re-acquiring each below would deadlock if either
// unlock had been skipped.
func TestApplyPeerResultSpawnpointLocalDespawnWins(t *testing.T) {
	lureTestSetup(t)
	const spawnId = int64(920210)
	const encId = uint64(920206)

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(600)}}
	spawnpointCache.Set(spawnId, sp, time.Minute)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId), PokemonId: 1}}
	pokemonCache.Set(encId, p, time.Minute)

	// In the past, so it also can't land on the pokemon as an hint - keeps
	// the assertions focused on the spawnpoint rule alone.
	pastExpiry := time.Now().Unix() - 100
	res := &pb.PokemonResult{Id: encId, ExpireTimestamp: &pastExpiry, ExpireTimestampVerified: true}
	item := peerLookupItem{EncounterId: encId, PokemonId: 1, SpawnId: spawnId}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if got := sp.DespawnSec.ValueOrZero(); got != 600 {
		t.Fatalf("local despawn_sec must survive a peer answer, got %d", got)
	}
	if p.ExpireTimestamp.Valid {
		t.Fatalf("a past peer expiry must not even land as a hint, got %d", p.ExpireTimestamp.ValueOrZero())
	}

	sp.Lock("post-check")
	sp.Unlock()
	p.Lock("post-check")
	p.Unlock()
}

// --- Important 1 (review): despawn_sec must be a LOCAL second-of-hour, not
// a UTC one (`ts % 3600`). The two only agree when the zone offset is a
// whole number of hours - proven here with a +5:30 fixed zone, chosen
// because it is a real timezone offset (India, etc.) and diverges from UTC
// by exactly the 1800s that separates the two despawn half-hour buckets, so
// a bug here would silently flip which bucket a spawnpoint lands in.
//
// This tests localSecondOfHour directly, with the offset passed as an
// explicit *time.Location, rather than driving it through applyPeerResult by
// swapping the process-wide time.Local: that global is read, unsynchronized,
// by the background stats worker (decoder/stats.go:908 et al, started from
// decoder's package init), so mutating it from a test would be a real -race
// hazard, not merely a style preference. localSecondOfHour is exactly the
// function the fix lives in and the one applyPeerResult/persistPeerDespawn
// call with time.Local in production, so this pins the fix without that
// hazard.
func TestLocalSecondOfHourDiffersFromUtcModAtHalfHourOffset(t *testing.T) {
	loc := time.FixedZone("UTC+5:30", 5*3600+30*60)
	ts := time.Date(2026, 3, 15, 10, 38, 0, 0, time.UTC).Unix() // arbitrary instant

	got := localSecondOfHour(ts, loc)

	wantLocal := time.Unix(ts, 0).In(loc)
	want := int64(wantLocal.Second() + wantLocal.Minute()*60)
	utcModValue := ts % 3600

	if want == utcModValue {
		t.Fatalf("test setup did not create a UTC/local divergence (both %d) - pick a different offset/instant", want)
	}
	if got != want {
		t.Fatalf("localSecondOfHour: got %d want %d (local second-of-hour)", got, want)
	}
	if got == utcModValue {
		t.Fatalf("localSecondOfHour returned the UTC value %d instead of the local one %d", utcModValue, want)
	}
}

// --- Minor 7 (review): a spawnpoint this peer answer is the first evidence
// of must not be inserted with lat=lon=0.

func TestBackstopSpawnpointPositionSetsLatLonForNewRecord(t *testing.T) {
	s := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 920220}, newRecord: true}

	backstopSpawnpointPosition(s, 51.5, -0.12)

	if s.Lat != 51.5 || s.Lon != -0.12 {
		t.Fatalf("new spawnpoint must adopt the peer's lat/lon, got %v/%v", s.Lat, s.Lon)
	}
}

func TestBackstopSpawnpointPositionLeavesExistingRecordAlone(t *testing.T) {
	s := &Spawnpoint{SpawnpointData: SpawnpointData{Id: 920221, Lat: 10, Lon: 20}} // newRecord: false

	backstopSpawnpointPosition(s, 51.5, -0.12)

	if s.Lat != 10 || s.Lon != 20 {
		t.Fatalf("an existing spawnpoint's position must not change, got %v/%v", s.Lat, s.Lon)
	}
}

// stubSpawnpointQueue installs a real (but disconnected) write-behind queue
// so spawnpointUpdate takes the Enqueue branch instead of the nil-DB
// fallback (mirrors the pattern in station_battle_test.go). Enqueue only
// stages an in-memory map (writebehind/typed_queue.go) - nothing flushes
// without a call to ProcessLoop, which nothing here makes, so this never
// touches a database.
func stubSpawnpointQueue(t *testing.T) *writebehind.TypedQueue[int64, SpawnpointData] {
	t.Helper()
	old := spawnpointQueue
	q := writebehind.NewTypedQueue(writebehind.TypedQueueConfig[int64, SpawnpointData]{
		Name:      "spawnpoint-test",
		Stats:     stats_collector.NewNoopStatsCollector(),
		FlushFunc: func(context.Context, db.DbDetails, []SpawnpointData) error { return nil },
		KeyFunc:   func(d SpawnpointData) int64 { return d.Id },
	})
	spawnpointQueue = q
	t.Cleanup(func() { spawnpointQueue = old })
	return q
}

// --- Important 2 (review): the positive half of rule 2 - a peer filling a
// genuinely NULL despawn_sec - reaching spawnpointUpdate and
// applyVerifiedDespawn. Also Important 3: the pokemon save and its webhook
// actually fire, not just the in-memory mutation.
func TestApplyPeerResultFillsNullDespawnAndVerifiesPokemon(t *testing.T) {
	sink := lureTestSetup(t)
	stubQueue := stubSpawnpointQueue(t)

	const spawnId = int64(920211)
	const encId = uint64(920207)

	// An existing (not new) spawnpoint record whose despawn_sec has never
	// been confirmed - e.g. discovered from fort/pokestop presence only.
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId}}
	spawnpointCache.Set(spawnId, sp, time.Minute)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId), PokemonId: 1}}
	pokemonCache.Set(encId, p, time.Minute)

	peerExpiry := time.Now().Add(10 * time.Minute).Unix()
	res := &pb.PokemonResult{Id: encId, ExpireTimestamp: &peerExpiry, ExpireTimestampVerified: true}
	item := peerLookupItem{EncounterId: encId, PokemonId: 1, SpawnId: spawnId}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	wantSecondOfHour := localSecondOfHour(peerExpiry, time.Local)
	if !sp.DespawnSec.Valid || sp.DespawnSec.ValueOrZero() != wantSecondOfHour {
		t.Fatalf("despawn_sec: got %v want %d", sp.DespawnSec, wantSecondOfHour)
	}
	if stubQueue.Size() != 1 {
		t.Fatalf("expected the spawnpoint write to reach the write-behind queue, size=%d", stubQueue.Size())
	}

	if !p.ExpireTimestampVerified {
		t.Fatal("pokemon expiry must be verified once the spawnpoint accepted the peer's despawn")
	}
	if p.IsDirty() {
		t.Fatal("pokemon must have been saved (dirty flag left set - savePokemonRecordAsAtTime clears it)")
	}

	msgs := sink.drain()
	if len(msgs) == 0 {
		t.Fatal("expected a webhook once verification flipped from false to true")
	}
}

// --- Minor 3 (review): a genuinely verified peer answer must still stamp
// this pokemon verified even when the spawnpoint-level write is rejected
// (rule 2 governs the shared despawn_sec, not this specific sighting).
func TestApplyPeerResultTrustsVerifiedAnswerEvenWhenSpawnpointWriteRejected(t *testing.T) {
	lureTestSetup(t)
	// Deliberately no stubSpawnpointQueue: the spawnpoint step must reject
	// the write (DespawnSec already known) and never reach spawnpointUpdate,
	// so the nil queue and nil GeneralDb in this test binary are never
	// exercised - reaching the assertions without panicking is itself proof.
	const spawnId = int64(920212)
	const encId = uint64(920208)

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(600)}}
	spawnpointCache.Set(spawnId, sp, time.Minute)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId), PokemonId: 1}}
	pokemonCache.Set(encId, p, time.Minute)

	peerExpiry := time.Now().Add(500 * time.Second).Unix()
	res := &pb.PokemonResult{Id: encId, ExpireTimestamp: &peerExpiry, ExpireTimestampVerified: true}
	item := peerLookupItem{EncounterId: encId, PokemonId: 1, SpawnId: spawnId}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if got := sp.DespawnSec.ValueOrZero(); got != 600 {
		t.Fatalf("spawnpoint despawn_sec must still be rejected, got %d", got)
	}
	if !p.ExpireTimestampVerified {
		t.Fatal("a genuinely verified peer answer must stamp this pokemon verified even when the spawnpoint-level write was rejected")
	}
	wantSecondOfHour := localSecondOfHour(peerExpiry, time.Local)
	gotSecondOfHour := localSecondOfHour(p.ExpireTimestamp.ValueOrZero(), time.Local)
	if gotSecondOfHour != wantSecondOfHour {
		t.Fatalf("stamped expiry second-of-hour: got %d want %d", gotSecondOfHour, wantSecondOfHour)
	}
}

// --- Task 10: rule 2 applied at this call site too. A peer's declared
// despawn second can itself be contradicted by the very sighting it is being
// applied to - most commonly the value persistPeerDespawn just wrote moments
// earlier, since it only writes when the local despawn_sec was null. When
// that happens the pokemon must be extended forward (not left with a
// near-past unverified expiry) and the spawnpoint queued for despawn_correction.go's
// async clear, exactly as setExpireTimestampFromSpawnpoint does.
//
// Deliberately no stubSpawnpointQueue: the spawnpoint already has an existing
// DespawnSec, so persistPeerDespawn's write is rejected and spawnpointUpdate
// is never reached (rule 2) - same reasoning as
// TestApplyPeerResultTrustsVerifiedAnswerEvenWhenSpawnpointWriteRejected.
func TestApplyPeerResultQueuesClearWhenPeerDespawnContradicted(t *testing.T) {
	lureTestSetup(t)
	const spawnId = int64(920213)
	const encId = uint64(920210)

	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId, DespawnSec: null.IntFrom(999)}}
	spawnpointCache.Set(spawnId, sp, time.Minute)

	oldQueue := despawnClearQueue
	t.Cleanup(func() { despawnClearQueue = oldQueue })
	despawnClearQueue = make(chan int64, 4)

	now := time.Now()
	// First seen long enough ago that reconstructing the peer's declared
	// despawn second from "now" wrap-clamps to well in the past - a live
	// pokemon whose declared phase has already elapsed.
	p := &Pokemon{PokemonData: PokemonData{
		Id:                 Uint64Str(encId),
		PokemonId:          1,
		FirstSeenTimestamp: now.Unix() - 4000,
	}}
	pokemonCache.Set(encId, p, time.Minute)

	peerExpiry := now.Add(3000 * time.Second).Unix() // 50 minutes out - passes the "in the future" guard
	res := &pb.PokemonResult{Id: encId, ExpireTimestamp: &peerExpiry, ExpireTimestampVerified: true}
	item := peerLookupItem{EncounterId: encId, PokemonId: 1, SpawnId: spawnId}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if p.ExpireTimestampVerified {
		t.Fatal("a contradicted peer answer must not be left verified")
	}
	if got := p.ExpireTimestamp.ValueOrZero(); got < now.Unix() {
		t.Fatalf("pokemon must be extended forward like an unknown spawnpoint, not left in the past: got %d, now=%d", got, now.Unix())
	}
	select {
	case gotId := <-despawnClearQueue:
		if gotId != spawnId {
			t.Fatalf("queued spawn id: got %d want %d", gotId, spawnId)
		}
	default:
		t.Fatal("expected the contradicted spawnpoint to be queued for a clear")
	}
	if p.IsDirty() {
		t.Fatal("pokemon must have been saved (dirty flag left set)")
	}
}

// --- Minor 4 (review): an unverified hint has no upper bound today, so an
// implausible far-future peer expiry would reach disappear_time on the wire.
func TestApplyPeerResultHintRejectsExpiryTooFarInFuture(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(920209)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encId), PokemonId: 1}}
	pokemonCache.Set(encId, p, time.Minute)

	peerExpiry := time.Now().Unix() + 1_000_000_000                   // no pokemon lives this long
	res := &pb.PokemonResult{Id: encId, ExpireTimestamp: &peerExpiry} // unverified
	item := peerLookupItem{EncounterId: encId, PokemonId: 1, SpawnId: 0}

	applyPeerResult(context.Background(), db.DbDetails{}, res, item)

	if p.ExpireTimestamp.Valid {
		t.Fatalf("an implausible far-future hint must be rejected, got %d", p.ExpireTimestamp.ValueOrZero())
	}
}

// --- Minor 1 (review): a panic inside spawnpointUpdate must not leak the
// spawnpoint lock - persistPeerDespawn releases it via defer specifically so
// this survives.
func TestPersistPeerDespawnReleasesLockOnPanic(t *testing.T) {
	lureTestSetup(t)
	// Deliberately no stubSpawnpointQueue: a NULL local despawn_sec takes
	// the write path, and spawnpointUpdate's nil-queue fallback against a
	// nil GeneralDb panics (proven above in TestApplyPeerResultSpawnpointLocalDespawnWins's
	// mutation testing) - exactly the scenario the defer must survive.
	const spawnId = int64(920222)
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnId}}
	spawnpointCache.Set(spawnId, sp, time.Minute)

	func() {
		defer func() { _ = recover() }()
		persistPeerDespawn(context.Background(), db.DbDetails{}, spawnId, 1234, 1, 2)
	}()

	// If the lock had leaked, this would deadlock the test instead of
	// failing it cleanly.
	sp.Lock("post-panic-check")
	sp.Unlock()
}
