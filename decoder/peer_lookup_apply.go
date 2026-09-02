package decoder

import (
	"context"
	"time"

	"golbat/db"
	pb "golbat/grpc"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
)

// localSecondOfHour converts a unix timestamp to a second-of-hour in loc,
// matching every other producer/consumer of despawn_sec (spawnpoint.go:331,
// pokemon_decode.go:382, tappable_decode.go:89) - never a UTC one
// (`ts % 3600`), which only agrees with local time when the zone offset is a
// whole number of hours. Production always calls this with time.Local; it
// takes an explicit *time.Location, rather than relying on time.Unix's
// implicit use of the time.Local global, so tests can inject a specific
// offset without mutating that global - which is read, unsynchronized, by
// the background stats worker (decoder/stats.go) and would otherwise be a
// data race under -race.
func localSecondOfHour(ts int64, loc *time.Location) int64 {
	d := time.Unix(ts, 0).In(loc)
	return int64(d.Second() + d.Minute()*60)
}

// applyPeerDespawnToSpawnpoint writes a peer's despawn second only when this
// instance has none. Local truth comes from watching a pokemon to its end
// state via TTH and, being a stable per-spawnpoint property, stays correct
// indefinitely - so a peer must never overwrite it. The reverse is desirable
// and unchanged: a later local TTH overwrites a peer value through
// spawnpointUpdateFromWild.
//
// despawnSecond must already be a LOCAL second-of-hour (see
// localSecondOfHour) - never a UTC one.
//
// Returns whether the value was taken.
func applyPeerDespawnToSpawnpoint(spawnpoint *Spawnpoint, despawnSecond int64) bool {
	if spawnpoint.DespawnSec.Valid {
		return false
	}
	spawnpoint.SetDespawnSec(null.IntFrom(despawnSecond))
	return true
}

// persistPeerDespawn locks the spawnpoint, applies the peer's despawn second
// (local truth wins - see applyPeerDespawnToSpawnpoint), and unlocks. The
// lock is released via defer specifically so a panic inside spawnpointUpdate
// cannot leak it.
//
// It deliberately does NOT create a spawnpoint this instance has no record
// of. The asker always already holds the spawnpoint: it only asks about an
// expiry when the pokemon carries a spawn id, which comes from a wild
// sighting, and spawnpointUpdateFromWild resolves the spawnpoint before the
// pokemon within the same GMO. So the question is always "fill a despawn_sec
// that is NULL", never "invent a spawnpoint".
//
// Creating one would also be actively unsafe. A peer answering out of its own
// spawnpoint table holds no pokemon record, so it sends lat=lon=0 - they are
// non-optional doubles on the wire and an omitted value is indistinguishable
// from a real zero - and a newly created record is INSERTed by the following
// spawnpointUpdate, which would put a spawnpoint at Null Island. Skipping the
// write instead leaves the despawn second to be learned from a local TTH, or
// from a later lookup once the spawnpoint exists.
func persistPeerDespawn(ctx context.Context, dbDetails db.DbDetails, spawnId int64, despawnSecond int64) (persisted bool) {
	spawnpoint, unlock, err := getSpawnpointRecord(ctx, dbDetails, spawnId, "applyPeerResult")
	if err != nil {
		log.Warnf("[PEER] spawnpoint %d unavailable: %s", spawnId, err)
		return false
	}
	if spawnpoint == nil {
		return false
	}
	defer unlock()

	if !applyPeerDespawnToSpawnpoint(spawnpoint, despawnSecond) {
		return false
	}
	spawnpointUpdate(ctx, dbDetails, spawnpoint)
	return true
}

// applyPeerStats adopts a peer's IVs and level. It deliberately ignores the
// peer's cp: recomputeCpIfNeeded early-returns on Cp.Valid, so adopting one
// would suppress the local computation and pin a value derived from the
// peer's masterfile version rather than ours. peerCp is accepted only to
// make the discard explicit at the call site.
//
// Gaining IVs upgrades seen_type, exactly as repopulateIv's restore branch
// does - and mirroring clearIv, which performs the same transition in reverse
// on the way out. A record with IVs and seen_type "wild" is a state no other
// path produces, and it is not cosmetic: updatePokemonStats (decoder/stats.go)
// drives monsIv, the verified/unverified encounter counters and the TTH
// histogram entirely off seen_type becoming "encounter", comparing the saved
// record against its pre-mutation snapshot. Without this the operator's
// dashboard never sees a peer-sourced IV, and webhook consumers receive
// pokemon_iv payloads labelled seen_type "wild".
//
// updatePokemonStats, not updateEncounterStats: the aggregation worker routes
// to the latter only for events flagged encounter, which
// savePokemonRecordAsAtTime does not emit on this path.
func applyPeerStats(pokemon *Pokemon, atk, def, sta, level, peerCp int64) {
	_ = peerCp // never adopted; see doc comment

	pokemon.calculateIv(atk, def, sta)
	pokemon.SetLevel(null.IntFrom(level))

	switch pokemon.SeenType.ValueOrZero() {
	case SeenType_LureWild:
		pokemon.SetSeenType(null.StringFrom(SeenType_LureEncounter))
	case SeenType_Wild:
		pokemon.SetSeenType(null.StringFrom(SeenType_Encounter))
	}
}

// applyPeerResult applies one peer answer. Locks are taken ONE AT A TIME:
// CLAUDE.md forbids holding two entity locks, and this does not need
// atomicity across the two entities.
func applyPeerResult(ctx context.Context, dbDetails db.DbDetails, res *pb.PokemonResult, item peerLookupItem) {
	hasStats := res.AtkIv != nil && res.DefIv != nil && res.StaIv != nil && res.Level != nil
	if res.ExpireTimestamp == nil && !hasStats {
		// Nothing actionable in this answer - do not even take the pokemon
		// lock for it.
		return
	}

	// despawnSecond is a LOCAL second-of-hour (see localSecondOfHour) -
	// computed once, independent of SpawnId, because it is also what stamps
	// the pokemon's own verified expiry below.
	var despawnSecond int64
	if res.ExpireTimestamp != nil {
		despawnSecond = localSecondOfHour(res.GetExpireTimestamp(), time.Local)
	}

	// --- 1. Spawnpoint, lock released before the pokemon is touched. ---
	// persisted is remembered for step 2: applyVerifiedDespawn below tests
	// despawnSecond (the peer's claim), not necessarily what is actually
	// stored - persistPeerDespawn only accepts the write when the local
	// value was NULL (rule 2, local truth wins). Only when persisted is true
	// did the tested value actually reach the spawnpoint, so only then is a
	// contradiction proof about the stored value rather than a stale peer
	// claim that was already correctly rejected.
	var persisted bool
	if res.GetExpireTimestampVerified() && res.ExpireTimestamp != nil && item.SpawnId != 0 {
		persisted = persistPeerDespawn(ctx, dbDetails, item.SpawnId, despawnSecond)
	}

	// --- 2. Pokemon. ---
	pokemon, unlock, err := getPokemonRecordForUpdate(ctx, dbDetails, item.EncounterId, "applyPeerResult")
	if err != nil || pokemon == nil {
		if unlock != nil {
			unlock()
		}
		return
	}
	defer unlock()

	// The record may have moved on between asking and answering.
	if int32(pokemon.PokemonId) != item.PokemonId {
		return
	}

	changed := false
	now := time.Now().Unix()

	if res.ExpireTimestamp != nil {
		if res.GetExpireTimestampVerified() {
			// Trust the peer's own verification for this specific sighting
			// regardless of whether the spawnpoint-level write above
			// happened: rule 2 (local truth wins) governs only the shared,
			// stable despawn_sec value, not whether this pokemon's expiry
			// can be trusted - e.g. the local spawnpoint may have gained its
			// despawn_sec from a real TTH between ask and answer, rejecting
			// the write, while the peer's answer about this pokemon is
			// still correct.
			// An already-past verified answer is stale: applyVerifiedDespawn
			// would derive a false next-hour expiry, and the wrap clamp is
			// not guaranteed to catch it (it requires FirstSeenTimestamp
			// already more than an hour old). Drop it rather than risk a
			// fabricated verified expiry.
			if peerExpiry := res.GetExpireTimestamp(); peerExpiry > now {
				if pokemon.applyVerifiedDespawn(int(despawnSecond), now*1000) {
					// The peer's declared despawn second is contradicted by
					// this live sighting. Extend as for an unknown spawnpoint
					// unconditionally - that part is right regardless of
					// what got persisted. But only retire the spawnpoint's
					// despawn_sec when persisted is true: only then is
					// despawnSecond (the value just tested) actually the
					// value in the spawnpoint. When the write was rejected
					// (local truth already had a different value - rule 2),
					// the untested local value must not be cleared on the
					// strength of a contradiction about the peer's claim;
					// setExpireTimestampFromSpawnpoint already tests the
					// real stored value on the next sighting regardless.
					pokemon.setUnknownTimestamp(now)
					if persisted && item.SpawnId != 0 {
						queueDespawnClear(item.SpawnId)
					}
				}
				changed = true
			}
		} else if !pokemon.ExpireTimestampVerified {
			// A hint. Only useful if it is a plausible near-future value
			// (never longer than a pokemon can live) and different from
			// what we hold; setUnknownTimestamp will not overwrite it later.
			peerExpiry := res.GetExpireTimestamp()
			if peerExpiry > now && peerExpiry < now+3600 && peerExpiry != pokemon.ExpireTimestamp.ValueOrZero() {
				pokemon.SetExpireTimestamp(null.IntFrom(peerExpiry))
				pokemon.SetExpireTimestampVerified(false)
				changed = true
			}
		}
	}

	// IVs and level are rolled per boost state, so stats held under a
	// different weather than the one asked about describe a different roll.
	// The answering side already refuses those (peerRecordMatches), but this
	// is the branch that makes them permanent - adopted stats gate every
	// later path on AtkIv.Valid, get a CP computed from them, and fire a
	// pokemon_iv webhook - so the boost state is re-checked here rather than
	// trusted across the wire. An answer that leaves weather unset carries no
	// claim about boost state and so is not evidence of a mismatch.
	statsForRequestedWeather := res.Weather == nil || int32(res.GetWeather()) == item.Weather

	if !pokemon.AtkIv.Valid && hasStats && statsForRequestedWeather {
		applyPeerStats(pokemon, res.GetAtkIv(), res.GetDefIv(), res.GetStaIv(), res.GetLevel(), res.GetCp())
		if res.Size != nil {
			pokemon.SetSize(null.IntFrom(res.GetSize()))
		}
		pokemon.recomputeCpIfNeeded(ctx, dbDetails, nil)
		changed = true
	}

	if changed {
		savePokemonRecordAsAtTime(ctx, dbDetails, pokemon, false, true, true, now)
	}
}
