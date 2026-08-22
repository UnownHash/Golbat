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

// backstopSpawnpointPosition sets lat/lon on a spawnpoint that has no DB row
// yet (newRecord true). getOrCreateSpawnpointRecord can hand back a brand
// new record when a peer answer is the first evidence of a spawnpoint; the
// write-behind INSERT that a subsequent spawnpointUpdate triggers would
// otherwise persist lat=lon=0 - this would be the only create-and-persist
// site in the tree that inserts a spawnpoint without a position. Existing
// records (a real DB row already loaded) are left alone: their position is
// already the fort/pokestop-derived one, not the peer's.
func backstopSpawnpointPosition(spawnpoint *Spawnpoint, lat, lon float64) {
	if !spawnpoint.newRecord {
		return
	}
	spawnpoint.SetLat(lat)
	spawnpoint.SetLon(lon)
}

// persistPeerDespawn locks the spawnpoint, applies the peer's despawn second
// (local truth wins - see applyPeerDespawnToSpawnpoint), and unlocks. The
// lock is released via defer specifically so a panic inside spawnpointUpdate
// cannot leak it.
func persistPeerDespawn(ctx context.Context, dbDetails db.DbDetails, spawnId int64, despawnSecond int64, lat, lon float64) (persisted bool) {
	spawnpoint, unlock, err := getOrCreateSpawnpointRecord(ctx, dbDetails, spawnId, "applyPeerResult")
	if err != nil {
		log.Warnf("[PEER] spawnpoint %d unavailable: %s", spawnId, err)
		return false
	}
	if spawnpoint == nil {
		return false
	}
	defer unlock()

	backstopSpawnpointPosition(spawnpoint, lat, lon)

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
func applyPeerStats(pokemon *Pokemon, atk, def, sta, level, peerCp int64) {
	_ = peerCp // never adopted; see doc comment

	pokemon.calculateIv(atk, def, sta)
	pokemon.SetLevel(null.IntFrom(level))
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
	if res.GetExpireTimestampVerified() && res.ExpireTimestamp != nil && item.SpawnId != 0 {
		persistPeerDespawn(ctx, dbDetails, item.SpawnId, despawnSecond, res.GetLat(), res.GetLon())
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
					// this live sighting - rule 2 applies here exactly as it
					// does in setExpireTimestampFromSpawnpoint (see
					// despawn_correction.go): despawnSecond is very often the
					// value persistPeerDespawn just wrote to the spawnpoint
					// above, since a fill of a previously-null despawn_sec is
					// the common case. Extend as for an unknown spawnpoint and
					// retire it asynchronously.
					pokemon.setUnknownTimestamp(now)
					queueDespawnClear(item.SpawnId)
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

	if !pokemon.AtkIv.Valid && hasStats {
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
