package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/db"
	pb "golbat/grpc"

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
