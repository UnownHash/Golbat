package decoder

import (
	"time"

	pb "golbat/grpc"
)

// Answering side of the cross-golbat lookup: everything a peer does with its
// own local data to reply to one question. It is deliberately lock-free and
// database-free - GetOnePokemon is cache-only and the spawnpoint lookup reads
// the atomic despawn mirror - because it runs inline in the gRPC handler,
// which keeps package main to transport and auth alone.

// AnswerPeerLookup answers one item of a peer's GetPokemon request from local
// data, or returns nil for a miss. A miss is an absent entry in the response,
// never a placeholder: the asker matches answers to questions by id.
//
// It never consults this instance's own peers, so a lookup cannot loop
// between instances - loop prevention by construction, not by a depth
// counter.
//
// now is passed in rather than read here so the spawnpoint answer, which is a
// pure function of the spawnpoint and the clock, is testable.
func AnswerPeerLookup(item *pb.GetPokemonItem, now time.Time) *pb.PokemonResult {
	if record := GetOnePokemon(item.GetEncounterId()); record != nil {
		if !peerRecordMatches(record, item.GetPokemonId(), item.GetForm(), item.GetWeather()) {
			return nil
		}
		return PokemonResultFromApi(record, item.GetEncounterId())
	}

	// Never saw this pokemon - but it may sit on a spawnpoint whose despawn
	// second this instance does know, in which case the expiry half of the
	// question is still answerable. That is what spawn_id is in the request
	// for.
	if item.SpawnId == nil {
		return nil
	}
	return spawnpointOnlyAnswer(item, now)
}

// peerRecordMatches reports whether a record this instance holds actually
// describes the sighting a peer is asking about.
//
// Encounter ids are reused when the server mutates a spawn, so an id on its
// own does not identify a sighting: species, form and boost state must all
// agree before the record may be returned as an answer.
//
// Weather is part of that agreement, not a nicety. IVs and level are rolled
// per boost state, so a record held under a different weather describes a
// different roll; answering with it would hand the asker stats for the wrong
// boost state, which it would then adopt, recompute a CP from and persist.
//
// A field the local record does not know is not evidence of a mismatch, so an
// unset form or weather passes: the asker re-checks whatever the answer does
// carry.
func peerRecordMatches(record *ApiPokemonResult, pokemonId, form, weather int32) bool {
	if record == nil {
		return false
	}
	if int32(record.PokemonId) != pokemonId {
		return false
	}
	if record.Form != nil && int32(*record.Form) != form {
		return false
	}
	if record.Weather != nil && int32(*record.Weather) != weather {
		return false
	}
	return true
}

// spawnpointOnlyAnswer builds the minimal answer for a pokemon this instance
// has never seen but whose spawnpoint it knows: an expiry, and nothing else.
//
// Two overlapping instances routinely have different pokemon coverage but
// overlapping spawnpoint coverage - a spawnpoint's despawn second is learned
// once, from any TTH sighting there, and is then valid for every later
// pokemon on it - so this is a common case, not an edge one.
//
// The result is sparse on purpose. Only the fields the asker can act on are
// filled: id to match it back to its question, pokemon_id echoed so the
// answer names the same sighting that was asked about, spawn_id so it is
// clear which spawnpoint the expiry came from, and the verified expiry
// itself. Stats are absent because this instance holds none, and the asking
// side does not require stats to act on an expiry.
func spawnpointOnlyAnswer(item *pb.GetPokemonItem, now time.Time) *pb.PokemonResult {
	expiry, ok := peerSpawnpointExpiry(item.GetSpawnId(), now)
	if !ok {
		return nil
	}

	spawnId := item.GetSpawnId()
	return &pb.PokemonResult{
		Id:                      item.GetEncounterId(),
		PokemonId:               item.GetPokemonId(),
		SpawnId:                 &spawnId,
		ExpireTimestamp:         &expiry,
		ExpireTimestampVerified: true,
	}
}

// peerSpawnpointExpiry returns the next occurrence of a known spawnpoint's
// despawn second after now, derived exactly as applyVerifiedDespawn derives
// it on the asking side so that the second-of-hour the asker recovers from
// the timestamp is the one stored here.
//
// ok is false when the spawnpoint is unknown, or known but with no despawn
// second yet - both plain misses.
//
// Lock-free and database-free: the spawnpoint cache plus the atomic despawn
// mirror, no entity lock and no DB fallback.
func peerSpawnpointExpiry(spawnId int64, now time.Time) (int64, bool) {
	if spawnId == 0 {
		return 0, false
	}
	spawnpoint, ok := spawnpointCache.Get(spawnId)
	if !ok {
		return 0, false
	}
	despawnSecond, known, synced := spawnpoint.DespawnSecFast()
	if !synced || !known {
		return 0, false
	}

	despawnOffset := despawnSecond - (now.Second() + now.Minute()*60)
	if despawnOffset < 0 {
		despawnOffset += 3600
	}
	return now.Unix() + int64(despawnOffset), true
}
