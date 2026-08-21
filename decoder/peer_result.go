package decoder

import (
	pb "golbat/grpc"
)

// PokemonResultFromApi converts the API result struct into its proto mirror.
// The two are intentionally field-for-field: ApiPokemonResult is what
// GET /api/pokemon/id/{encounter_id} already returns, and keeping the shapes
// identical means a peer answer is just that endpoint over gRPC.
//
// The encounter id is supplied by the caller rather than parsed from r.Id:
// ApiPokemonResult renders it as a string only because JSON cannot carry a
// 64-bit integer, and the caller already holds the uint64.
//
// Pointer fields stay nil when absent. A peer that holds no IVs must be
// distinguishable from one reporting 0/0/0.
//
// Pvp is deliberately left unset: the proto's optional pvp mirrors the raw
// stored decoder.Pokemon.Pvp (a null.String), not ApiPokemonResult.Pvp, which
// is a computed ApiPvpRankings struct built by a live ohbem query. The client
// never adopts a peer's PVP — it recomputes locally — and PVP rankings depend
// on the answering instance's league config, so they are not transportable.
// An unset pvp is exactly what a PVP-disabled Golbat produces.
func PokemonResultFromApi(r *ApiPokemonResult, encounterId uint64) *pb.PokemonResult {
	if r == nil {
		return nil
	}

	out := &pb.PokemonResult{
		Id:                      encounterId,
		PokestopId:              r.PokestopId,
		SpawnId:                 r.SpawnId,
		Lat:                     r.Lat,
		Lon:                     r.Lon,
		Weight:                  r.Weight,
		Size:                    r.Size,
		Height:                  r.Height,
		ExpireTimestamp:         r.ExpireTimestamp,
		Updated:                 r.Updated,
		PokemonId:               int32(r.PokemonId),
		Move_1:                  r.Move1,
		Move_2:                  r.Move2,
		Gender:                  r.Gender,
		Cp:                      r.Cp,
		AtkIv:                   r.AtkIv,
		DefIv:                   r.DefIv,
		StaIv:                   r.StaIv,
		Iv:                      r.Iv,
		Form:                    r.Form,
		Level:                   r.Level,
		Weather:                 r.Weather,
		Costume:                 r.Costume,
		FirstSeenTimestamp:      r.FirstSeenTimestamp,
		Changed:                 r.Changed,
		CellId:                  r.CellId,
		ExpireTimestampVerified: r.ExpireTimestampVerified,
		DisplayPokemonId:        r.DisplayPokemonId,
		DisplayPokemonForm:      r.DisplayPokemonForm,
		IsDitto:                 r.IsDitto,
		SeenType:                r.SeenType,
		Shiny:                   r.Shiny,
		Username:                r.Username,
		Capture_1:               r.Capture1,
		Capture_2:               r.Capture2,
		Capture_3:               r.Capture3,
		IsEvent:                 int32(r.IsEvent),
	}

	return out
}
