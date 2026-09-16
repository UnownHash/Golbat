package decoder

import (
	"math"

	pb "golbat/grpc"
)

// gRPC adapter for the pokemon API: proto request → ApiPokemonScan3, and
// ApiPokemonResult → proto. The scan itself is the same code the HTTP v3
// endpoint runs; only the serialisation differs.

func latLonFromProto(l *pb.LatLon) ApiLatLon {
	return ApiLatLon{Lat: l.GetLat(), Lon: l.GetLon()}
}

func clampInt16(v int32) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}

// intRangeBounds resolves a proto IntRange: unset min is 0, unset max is
// MaxInt16 (no upper bound). ok is false for a nil range (no constraint).
func intRangeBounds(r *pb.IntRange) (minV, maxV int16, ok bool) {
	if r == nil {
		return 0, 0, false
	}
	minV, maxV = 0, math.MaxInt16
	if r.Min != nil {
		minV = clampInt16(r.GetMin())
	}
	if r.Max != nil {
		maxV = clampInt16(r.GetMax())
	}
	return minV, maxV, true
}

// intRangeTo resolves a proto IntRange into the filter's own range type via
// mk; nil (no constraint) stays nil. The pokemon and fort filters carry
// field-identical but distinct range types, hence the constructor.
func intRangeTo[T any](r *pb.IntRange, mk func(minV, maxV int16) T) *T {
	minV, maxV, ok := intRangeBounds(r)
	if !ok {
		return nil
	}
	out := mk(minV, maxV)
	return &out
}

func intRangeToPokemonMinMax(r *pb.IntRange) *ApiPokemonDnfMinMax {
	return intRangeTo(r, func(minV, maxV int16) ApiPokemonDnfMinMax { return ApiPokemonDnfMinMax{Min: minV, Max: maxV} })
}

// int32sTo narrows a repeated int32 to the filter's slice type. Empty input
// yields nil, which is the JSON "omitted" (no constraint) representation.
func int32sTo[T ~int8 | ~int16](in []int32) []T {
	if len(in) == 0 {
		return nil
	}
	out := make([]T, len(in))
	for i, v := range in {
		out[i] = T(v)
	}
	return out
}

// dnfIdsTo converts a repeated DnfId into the filter's own id/form entry type
// via mk (nil entries skipped; empty input yields nil, the JSON "omitted"
// representation). An unset form stays nil, meaning any form.
func dnfIdsTo[T any](ids []*pb.DnfId, mk func(pokemon int16, form *int16) T) []T {
	if len(ids) == 0 {
		return nil
	}
	out := make([]T, 0, len(ids))
	for _, id := range ids {
		if id == nil {
			continue
		}
		var form *int16
		if id.Form != nil {
			f := clampInt16(id.GetForm())
			form = &f
		}
		out = append(out, mk(clampInt16(id.GetPokemonId()), form))
	}
	return out
}

func dnfIdsToPokemon(ids []*pb.DnfId) []ApiPokemonDnfId {
	return dnfIdsTo(ids, func(pokemon int16, form *int16) ApiPokemonDnfId { return ApiPokemonDnfId{Pokemon: pokemon, Form: form} })
}

// mapValues converts each element of a value slice with f, preserving order;
// empty input yields nil so an absent list stays absent on the wire.
func mapValues[I, O any](in []I, f func(*I) *O) []*O {
	if len(in) == 0 {
		return nil
	}
	out := make([]*O, len(in))
	for i := range in {
		out[i] = f(&in[i])
	}
	return out
}

func pokemonDnfFilterFromProto(f *pb.PokemonDnfFilter) ApiPokemonDnfFilter3 {
	return ApiPokemonDnfFilter3{
		Pokemon: dnfIdsToPokemon(f.GetPokemon()),
		Iv:      intRangeToPokemonMinMax(f.GetIv()),
		AtkIv:   intRangeToPokemonMinMax(f.GetAtkIv()),
		DefIv:   intRangeToPokemonMinMax(f.GetDefIv()),
		StaIv:   intRangeToPokemonMinMax(f.GetStaIv()),
		Level:   intRangeToPokemonMinMax(f.GetLevel()),
		Cp:      intRangeToPokemonMinMax(f.GetCp()),
		Gender:  int32sTo[int8](f.GetGender()),
		Size:    intRangeToPokemonMinMax(f.GetSize()),
		Little:  intRangeToPokemonMinMax(f.GetPvpLittle()),
		Great:   intRangeToPokemonMinMax(f.GetPvpGreat()),
		Ultra:   intRangeToPokemonMinMax(f.GetPvpUltra()),
	}
}

func pokemonScanRequestFromProto(req *pb.PokemonScanRequest) ApiPokemonScan3 {
	out := ApiPokemonScan3{
		Min:          latLonFromProto(req.GetMin()),
		Max:          latLonFromProto(req.GetMax()),
		Limit:        int(req.GetLimit()),
		UpdatedAfter: req.GetUpdatedAfter(),
	}
	if filters := req.GetFilters(); len(filters) > 0 {
		out.DnfFilters = make([]ApiPokemonDnfFilter3, 0, len(filters))
		for _, f := range filters {
			if f != nil {
				out.DnfFilters = append(out.DnfFilters, pokemonDnfFilterFromProto(f))
			}
		}
	}
	return out
}

// optU32 widens a narrow optional API field to the proto's optional uint32.
func optU32[T ~uint8 | ~uint16](p *T) *uint32 {
	if p == nil {
		return nil
	}
	v := uint32(*p)
	return &v
}

func pvpEntryToProto(e *ApiPvpEntry) *pb.PvpEntry {
	return &pb.PvpEntry{
		Pokemon:    int32(e.Pokemon),
		Form:       int32(e.Form),
		Cap:        e.Cap,
		Value:      e.Value,
		Level:      e.Level,
		Cp:         int32(e.Cp),
		Percentage: e.Percentage,
		Rank:       int32(e.Rank),
		Capped:     e.Capped,
		Evolution:  int32(e.Evolution),
	}
}

func pvpEntriesToProto(entries []ApiPvpEntry) []*pb.PvpEntry {
	return mapValues(entries, pvpEntryToProto)
}

func pvpRankingsToProto(r ApiPvpRankings) *pb.PvpRankings {
	return &pb.PvpRankings{
		Little: pvpEntriesToProto(r.Little),
		Great:  pvpEntriesToProto(r.Great),
		Ultra:  pvpEntriesToProto(r.Ultra),
	}
}

// pokemonToProto mirrors an ApiPokemonResult into its proto message. The
// encounter id is passed by the caller: ApiPokemonResult.Id is a decimal
// string only because JSON cannot carry a uint64. Pointer fields alias the
// API struct's; both are discarded after serialisation.
func pokemonToProto(r *ApiPokemonResult, encounterId uint64) *pb.Pokemon {
	return &pb.Pokemon{
		Id:                      encounterId,
		PokestopId:              r.PokestopId,
		SpawnId:                 r.SpawnId,
		Lat:                     r.Lat,
		Lon:                     r.Lon,
		Weight:                  r.Weight,
		Size:                    optU32(r.Size),
		Height:                  r.Height,
		ExpireTimestamp:         r.ExpireTimestamp,
		Updated:                 r.Updated,
		PokemonId:               int32(r.PokemonId),
		Move_1:                  optU32(r.Move1),
		Move_2:                  optU32(r.Move2),
		Gender:                  optU32(r.Gender),
		Cp:                      optU32(r.Cp),
		AtkIv:                   optU32(r.AtkIv),
		DefIv:                   optU32(r.DefIv),
		StaIv:                   optU32(r.StaIv),
		Iv:                      r.Iv,
		Form:                    optU32(r.Form),
		Level:                   optU32(r.Level),
		Weather:                 optU32(r.Weather),
		Costume:                 optU32(r.Costume),
		FirstSeenTimestamp:      r.FirstSeenTimestamp,
		Changed:                 r.Changed,
		CellId:                  r.CellId,
		ExpireTimestampVerified: r.ExpireTimestampVerified,
		DisplayPokemonId:        optU32(r.DisplayPokemonId),
		DisplayPokemonForm:      optU32(r.DisplayPokemonForm),
		IsDitto:                 r.IsDitto,
		SeenType:                r.SeenType,
		Shiny:                   r.Shiny,
		Username:                r.Username,
		Capture_1:               r.Capture1,
		Capture_2:               r.Capture2,
		Capture_3:               r.Capture3,
		Pvp:                     pvpRankingsToProto(r.Pvp),
		IsEvent:                 int32(r.IsEvent),
	}
}

// GrpcScanPokemon is the gRPC counterpart of GetPokemonInArea3Clean: same
// spatial scan, same DNF matching, same per-record build, proto out.
func GrpcScanPokemon(req *pb.PokemonScanRequest) *pb.PokemonScanResponse {
	apiReq := pokemonScanRequestFromProto(req)
	keys, examined, skipped, total := internalGetPokemonInArea3(apiReq)

	results := make([]*pb.Pokemon, 0, len(keys))
	forEachLivePokemonResult(keys, "API.ScanPokemon.v3.grpc", apiReq.UpdatedAfter, func(id uint64, r *ApiPokemonResult) {
		results = append(results, pokemonToProto(r, id))
	})

	return &pb.PokemonScanResponse{
		Pokemon:      results,
		Examined:     int32(examined),
		Skipped:      int32(skipped),
		Total:        int32(total),
		LimitReached: pokemonScanLimitReached(apiReq, len(keys)),
	}
}

// GrpcGetPokemon answers a batched by-id lookup from the cache. Unknown ids
// are omitted, not returned as empty placeholders.
func GrpcGetPokemon(ids []uint64) []*pb.Pokemon {
	out := make([]*pb.Pokemon, 0, len(ids))
	for _, id := range ids {
		if r := GetOnePokemon(id); r != nil {
			out = append(out, pokemonToProto(r, id))
		}
	}
	return out
}
