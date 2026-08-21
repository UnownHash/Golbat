package decoder

import (
	"context"
	"time"

	"golbat/db"
	pb "golbat/grpc"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
)

// applyPeerDespawnToSpawnpoint writes a peer's despawn second only when this
// instance has none. Local truth comes from watching a pokemon to its end
// state via TTH and, being a stable per-spawnpoint property, stays correct
// indefinitely - so a peer must never overwrite it. The reverse is desirable
// and unchanged: a later local TTH overwrites a peer value through
// spawnpointUpdateFromWild.
//
// Returns whether the value was taken.
func applyPeerDespawnToSpawnpoint(spawnpoint *Spawnpoint, despawnSecond int64) bool {
	if spawnpoint.DespawnSec.Valid {
		return false
	}
	spawnpoint.SetDespawnSec(null.IntFrom(despawnSecond))
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

	// --- 1. Spawnpoint, lock released before the pokemon is touched. ---
	persistedDespawn := false
	if res.GetExpireTimestampVerified() && res.ExpireTimestamp != nil && item.SpawnId != 0 {
		despawnSecond := res.GetExpireTimestamp() % 3600

		spawnpoint, unlock, err := getOrCreateSpawnpointRecord(ctx, dbDetails, item.SpawnId, "applyPeerResult")
		if err != nil {
			log.Warnf("[PEER] spawnpoint %d unavailable: %s", item.SpawnId, err)
		} else if spawnpoint != nil {
			if applyPeerDespawnToSpawnpoint(spawnpoint, despawnSecond) {
				spawnpointUpdate(ctx, dbDetails, spawnpoint)
				persistedDespawn = true
			}
			unlock()
		}
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
		if persistedDespawn {
			// Verified: derive through the normal path so the wrap clamp and
			// the verified flag are applied exactly as a TTH sighting would.
			pokemon.applyVerifiedDespawn(int(res.GetExpireTimestamp()%3600), now*1000)
			changed = true
		} else if !pokemon.ExpireTimestampVerified {
			// A hint. Only useful if it is in the future and better than what
			// we hold; setUnknownTimestamp will not overwrite it later.
			peerExpiry := res.GetExpireTimestamp()
			if peerExpiry > now && peerExpiry != pokemon.ExpireTimestamp.ValueOrZero() {
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
