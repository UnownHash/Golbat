package decoder

import (
	"strconv"

	pb "golbat/grpc"
)

// gRPC adapter for the tappable API: requests convert to ApiTappableScan and
// run through TappableScanEndpoint; results mirror ApiTappableResult.

// int32sClampInt16 narrows with saturation, so an out-of-range id never wraps
// into a valid or negative one. Empty input yields nil (no constraint).
func int32sClampInt16(in []int32) []int16 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int16, len(in))
	for i, v := range in {
		out[i] = clampInt16(v)
	}
	return out
}

func tappableDnfFilterFromProto(f *pb.TappableDnfFilter) ApiTappableDnfFilter {
	var rewardType []string
	if len(f.GetRewardType()) > 0 {
		rewardType = f.GetRewardType()
	}
	return ApiTappableDnfFilter{
		RewardType:      rewardType,
		ItemId:          int32sClampInt16(f.GetItemId()),
		RewardPokemonId: int32sClampInt16(f.GetRewardPokemonId()),
		PokemonId:       int32sClampInt16(f.GetPokemonId()),
		Count:           intRangeToFortMinMax(f.GetCount()),
	}
}

func tappableScanRequestFromProto(req *pb.TappableScanRequest) ApiTappableScan {
	var filters []ApiTappableDnfFilter
	for _, f := range req.GetFilters() {
		if f != nil {
			filters = append(filters, tappableDnfFilterFromProto(f))
		}
	}
	return ApiTappableScan{
		Min:          latLonFromProto(req.GetMin()),
		Max:          latLonFromProto(req.GetMax()),
		Limit:        int(req.GetLimit()),
		UpdatedAfter: req.GetUpdatedAfter(),
		DnfFilters:   filters,
	}
}

func tappableToProto(r *ApiTappableResult, id uint64) *pb.Tappable {
	return &pb.Tappable{
		Id:                      id,
		Lat:                     r.Lat,
		Lon:                     r.Lon,
		FortId:                  r.FortId,
		SpawnId:                 r.SpawnId,
		Type:                    r.Type,
		PokemonId:               r.Encounter,
		RewardType:              r.RewardType,
		ItemId:                  r.ItemId,
		RewardPokemonId:         r.RewardPokemonId,
		Count:                   r.Count,
		RewardsJson:             rawJsonToProto(r.Rewards),
		ExpireTimestamp:         r.ExpireTimestamp,
		ExpireTimestampVerified: r.ExpireTimestampVerified,
		Updated:                 r.Updated,
	}
}

// GrpcScanTappables runs TappableScanEndpoint; the error is an invalid filter.
func GrpcScanTappables(req *pb.TappableScanRequest) (*pb.TappableScanResponse, error) {
	res, err := TappableScanEndpoint(tappableScanRequestFromProto(req))
	if err != nil {
		return nil, err
	}
	out := make([]*pb.Tappable, 0, len(res.Tappables))
	for _, r := range res.Tappables {
		id, err := strconv.ParseUint(r.Id, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, tappableToProto(r, id))
	}
	return &pb.TappableScanResponse{
		Tappables:    out,
		Examined:     int32(res.Examined),
		Skipped:      int32(res.Skipped),
		Total:        int32(res.Total),
		LimitReached: res.LimitReached,
	}, nil
}

// GrpcGetTappables answers a batched by-id lookup from the cache. Unknown ids
// are omitted.
func GrpcGetTappables(ids []uint64) []*pb.Tappable {
	out := make([]*pb.Tappable, 0, len(ids))
	for _, id := range ids {
		t, unlock, _ := PeekTappableRecord(id, "API.GetTappable.grpc")
		if t == nil {
			continue
		}
		r := BuildTappableResult(t)
		unlock()
		out = append(out, tappableToProto(&r, id))
	}
	return out
}
