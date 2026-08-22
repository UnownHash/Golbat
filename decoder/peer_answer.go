package decoder

// Answering side of the cross-golbat lookup: the checks and lookups a peer
// runs over its own local data before it replies. Everything here is
// deliberately lock-free and database-free, matching GetOnePokemon's
// cache-only contract, because it runs inline in the gRPC handler.

// PeerRecordMatches reports whether a record this instance holds actually
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
func PeerRecordMatches(record *ApiPokemonResult, pokemonId, form, weather int32) bool {
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
