package decoder

import (
	"testing"

	"github.com/guregu/null/v6"

	pb "golbat/grpc"
)

func TestGrpcScanTappables(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 18446744073709551000, 38.001, 48.001, pikachuCandy)
	seedTappable(t, 930071, 38.002, 48.002, rareCandy)
	req := &pb.TappableScanRequest{
		Min: &pb.LatLon{Lat: 38, Lon: 48}, Max: &pb.LatLon{Lat: 38.01, Lon: 48.01},
		Filters: []*pb.TappableDnfFilter{{RewardType: []string{"pokemon_candy"}, Count: &pb.IntRange{Min: ptrInt32(5)}}},
	}
	res, err := GrpcScanTappables(req)
	if err != nil {
		t.Fatalf("GrpcScanTappables: %v", err)
	}
	if len(res.GetTappables()) != 1 {
		t.Fatalf("tappables = %d, want 1", len(res.GetTappables()))
	}
	got := res.GetTappables()[0]
	if got.GetId() != 18446744073709551000 || got.GetRewardType() != "pokemon_candy" || got.GetRewardPokemonId() != 25 || got.GetCount() != 6 {
		t.Errorf("tappable = %v", got)
	}
	if res.GetExamined() != 2 || res.GetLimitReached() {
		t.Errorf("examined %d limit_reached %v, want 2 false", res.GetExamined(), res.GetLimitReached())
	}
}

func TestGrpcScanTappablesEmptyFiltersMatchAll(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 930081, 39.001, 49.001, rareCandy)
	seedTappable(t, 930082, 39.002, 49.002, func(d *TappableData) { d.Encounter = null.IntFrom(25) })
	res, err := GrpcScanTappables(&pb.TappableScanRequest{Min: &pb.LatLon{Lat: 39, Lon: 49}, Max: &pb.LatLon{Lat: 39.01, Lon: 49.01}})
	if err != nil || len(res.GetTappables()) != 2 {
		t.Errorf("tappables = %d, err %v; want 2, nil", len(res.GetTappables()), err)
	}
}

func TestGrpcScanTappablesUnknownRewardType(t *testing.T) {
	_, err := GrpcScanTappables(&pb.TappableScanRequest{Filters: []*pb.TappableDnfFilter{{RewardType: []string{"Item"}}}})
	if err == nil {
		t.Error("unknown reward_type: want error")
	}
}

func TestGrpcGetTappables(t *testing.T) {
	seedTappable(t, 930091, 40.001, 50.001, rareCandy)
	got := GrpcGetTappables([]uint64{930091, 999999999})
	if len(got) != 1 || got[0].GetId() != 930091 || got[0].GetItemId() != 1301 {
		t.Errorf("GrpcGetTappables = %v, want only 930091", got)
	}
}

func ptrInt32(v int32) *int32 { return &v }
