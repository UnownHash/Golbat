package decoder

import (
	"math"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/config"
	pb "golbat/grpc"
)

func ptr[T any](v T) *T { return &v }

func TestIntRangeToPokemonMinMax(t *testing.T) {
	if got := intRangeToPokemonMinMax(nil); got != nil {
		t.Fatalf("nil range must stay nil (no constraint), got %+v", got)
	}
	got := intRangeToPokemonMinMax(&pb.IntRange{})
	if got.Min != 0 || got.Max != math.MaxInt16 {
		t.Errorf("unset bounds = %+v, want 0..32767", got)
	}
	got = intRangeToPokemonMinMax(&pb.IntRange{Min: ptr(int32(90)), Max: ptr(int32(100))})
	if got.Min != 90 || got.Max != 100 {
		t.Errorf("explicit bounds = %+v, want 90..100", got)
	}
	got = intRangeToPokemonMinMax(&pb.IntRange{Min: ptr(int32(-70000)), Max: ptr(int32(1 << 20))})
	if got.Min != math.MinInt16 || got.Max != math.MaxInt16 {
		t.Errorf("out-of-range bounds must clamp, got %+v", got)
	}
}

func TestPokemonScanRequestFromProto(t *testing.T) {
	req := &pb.PokemonScanRequest{
		Min:          &pb.LatLon{Lat: 1, Lon: 2},
		Max:          &pb.LatLon{Lat: 3, Lon: 4},
		Limit:        50,
		UpdatedAfter: ptr(int64(1700000000)),
		Filters: []*pb.PokemonDnfFilter{{
			Pokemon:  []*pb.DnfId{{PokemonId: 25}, {PokemonId: 26, Form: ptr(int32(2))}},
			Iv:       &pb.IntRange{Min: ptr(int32(100))},
			Gender:   []int32{1, 2},
			PvpGreat: &pb.IntRange{Max: ptr(int32(100))},
		}},
	}
	got := pokemonScanRequestFromProto(req)
	if got.Min != (ApiLatLon{Lat: 1, Lon: 2}) || got.Max != (ApiLatLon{Lat: 3, Lon: 4}) || got.Limit != 50 || got.UpdatedAfter != 1700000000 {
		t.Fatalf("bbox/limit/updated_after = %+v", got)
	}
	if len(got.DnfFilters) != 1 {
		t.Fatalf("filters = %d, want 1", len(got.DnfFilters))
	}
	f := got.DnfFilters[0]
	if len(f.Pokemon) != 2 || f.Pokemon[0].Pokemon != 25 || f.Pokemon[0].Form != nil ||
		f.Pokemon[1].Pokemon != 26 || f.Pokemon[1].Form == nil || *f.Pokemon[1].Form != 2 {
		t.Errorf("pokemon ids = %+v", f.Pokemon)
	}
	if f.Iv == nil || f.Iv.Min != 100 || f.Iv.Max != math.MaxInt16 {
		t.Errorf("iv = %+v, want 100..32767", f.Iv)
	}
	if f.AtkIv != nil || f.DefIv != nil || f.StaIv != nil || f.Level != nil || f.Cp != nil || f.Size != nil || f.Little != nil || f.Ultra != nil {
		t.Error("unset ranges must be nil")
	}
	if len(f.Gender) != 2 || f.Gender[0] != 1 || f.Gender[1] != 2 {
		t.Errorf("gender = %v", f.Gender)
	}
	if f.Great == nil || f.Great.Min != 0 || f.Great.Max != 100 {
		t.Errorf("pvp great = %+v, want 0..100", f.Great)
	}

	empty := pokemonScanRequestFromProto(&pb.PokemonScanRequest{Min: &pb.LatLon{}, Max: &pb.LatLon{}})
	if empty.DnfFilters != nil {
		t.Errorf("no filters must convert to a nil slice, got %#v", empty.DnfFilters)
	}
	if got := int32sTo[int8](nil); got != nil {
		t.Errorf("empty gender list must be nil, got %#v", got)
	}
}

func fullApiPokemonResult() ApiPokemonResult {
	return ApiPokemonResult{
		Id:                      "123456789",
		PokestopId:              ptr("fedcba9876543210fedcba9876543210.16"),
		SpawnId:                 ptr(int64(0x1234)),
		Lat:                     51.5,
		Lon:                     -0.12,
		Weight:                  ptr(float32(6.5)),
		Size:                    ptr(uint8(3)),
		Height:                  ptr(float32(0.4)),
		ExpireTimestamp:         ptr(int64(1700000600)),
		Updated:                 ptr(int64(1700000000)),
		PokemonId:               25,
		Move1:                   ptr(uint16(219)),
		Move2:                   ptr(uint16(21)),
		Gender:                  ptr(uint8(1)),
		Cp:                      ptr(uint16(1500)),
		AtkIv:                   ptr(uint8(15)),
		DefIv:                   ptr(uint8(14)),
		StaIv:                   ptr(uint8(13)),
		Iv:                      ptr(float32(93.3)),
		Form:                    ptr(uint16(2)),
		Level:                   ptr(uint8(30)),
		Weather:                 ptr(uint8(1)),
		Costume:                 ptr(uint8(0)),
		FirstSeenTimestamp:      1699999000,
		Changed:                 1699999500,
		CellId:                  ptr(int64(5221000000000000000)),
		ExpireTimestampVerified: true,
		DisplayPokemonId:        ptr(uint16(132)),
		DisplayPokemonForm:      ptr(uint16(0)),
		IsDitto:                 true,
		SeenType:                ptr("encounter"),
		Shiny:                   ptr(true),
		Username:                ptr("trainer"),
		Pvp: ApiPvpRankings{
			Great: []ApiPvpEntry{{Pokemon: 25, Form: 2, Cap: 50, Value: 1234.5, Level: 40, Cp: 1499, Percentage: 0.98, Rank: 7, Capped: true, Evolution: 0}},
			Ultra: []ApiPvpEntry{{Pokemon: 26, Rank: 1, Percentage: 1}},
		},
	}
}

func TestPokemonToProtoFullyPopulated(t *testing.T) {
	r := fullApiPokemonResult()
	got := pokemonToProto(&r, 123456789)

	if got.Id != 123456789 || got.GetPokestopId() != "fedcba9876543210fedcba9876543210.16" || got.GetSpawnId() != 0x1234 {
		t.Errorf("ids = %d %q %d", got.Id, got.GetPokestopId(), got.GetSpawnId())
	}
	if got.Lat != 51.5 || got.Lon != -0.12 {
		t.Errorf("lat/lon = %v %v", got.Lat, got.Lon)
	}
	if got.GetWeight() != 6.5 || got.GetSize() != 3 || got.GetHeight() != 0.4 || got.GetIv() != 93.3 {
		t.Errorf("weight/size/height/iv = %v %v %v %v", got.GetWeight(), got.GetSize(), got.GetHeight(), got.GetIv())
	}
	if got.GetExpireTimestamp() != 1700000600 || got.GetUpdated() != 1700000000 || got.FirstSeenTimestamp != 1699999000 || got.Changed != 1699999500 {
		t.Errorf("timestamps = %v %v %v %v", got.GetExpireTimestamp(), got.GetUpdated(), got.FirstSeenTimestamp, got.Changed)
	}
	if got.PokemonId != 25 || got.GetMove_1() != 219 || got.GetMove_2() != 21 || got.GetGender() != 1 || got.GetCp() != 1500 {
		t.Errorf("species/moves/gender/cp = %v %v %v %v %v", got.PokemonId, got.GetMove_1(), got.GetMove_2(), got.GetGender(), got.GetCp())
	}
	if got.GetAtkIv() != 15 || got.GetDefIv() != 14 || got.GetStaIv() != 13 || got.GetForm() != 2 || got.GetLevel() != 30 || got.GetWeather() != 1 {
		t.Errorf("ivs/form/level/weather = %v %v %v %v %v %v", got.GetAtkIv(), got.GetDefIv(), got.GetStaIv(), got.GetForm(), got.GetLevel(), got.GetWeather())
	}
	if got.Costume == nil || *got.Costume != 0 {
		t.Errorf("costume 0 must be present, not unset: %v", got.Costume)
	}
	if got.GetCellId() != 5221000000000000000 || !got.ExpireTimestampVerified || got.GetDisplayPokemonId() != 132 || got.DisplayPokemonForm == nil || !got.IsDitto {
		t.Errorf("cell/verified/display/ditto = %v %v %v %v %v", got.GetCellId(), got.ExpireTimestampVerified, got.GetDisplayPokemonId(), got.DisplayPokemonForm, got.IsDitto)
	}
	if got.GetSeenType() != "encounter" || !got.GetShiny() || got.GetUsername() != "trainer" {
		t.Errorf("seen/shiny/username = %q %v %q", got.GetSeenType(), got.GetShiny(), got.GetUsername())
	}
	if got.Capture_1 != nil || got.Capture_2 != nil || got.Capture_3 != nil || got.IsEvent != 0 {
		t.Error("capture rates and is_event are never populated by the API and must stay unset")
	}
	pvp := got.GetPvp()
	if pvp == nil || len(pvp.Little) != 0 || len(pvp.Great) != 1 || len(pvp.Ultra) != 1 {
		t.Fatalf("pvp = %+v", pvp)
	}
	g := pvp.Great[0]
	if g.Pokemon != 25 || g.Form != 2 || g.Cap != 50 || g.Value != 1234.5 || g.Level != 40 || g.Cp != 1499 || g.Percentage != 0.98 || g.Rank != 7 || !g.Capped || g.Evolution != 0 {
		t.Errorf("great entry = %+v", g)
	}
	if pvp.Ultra[0].Pokemon != 26 || pvp.Ultra[0].Rank != 1 {
		t.Errorf("ultra entry = %+v", pvp.Ultra[0])
	}
}

func TestPokemonToProtoNilOptionalsStayUnset(t *testing.T) {
	r := ApiPokemonResult{Id: "7", PokemonId: 1, Lat: 1, Lon: 2, FirstSeenTimestamp: 10, Changed: 11}
	got := pokemonToProto(&r, 7)
	if got.PokestopId != nil || got.SpawnId != nil || got.Weight != nil || got.Size != nil || got.Height != nil ||
		got.ExpireTimestamp != nil || got.Updated != nil || got.Move_1 != nil || got.Move_2 != nil || got.Gender != nil ||
		got.Cp != nil || got.AtkIv != nil || got.DefIv != nil || got.StaIv != nil || got.Iv != nil || got.Form != nil ||
		got.Level != nil || got.Weather != nil || got.Costume != nil || got.CellId != nil || got.DisplayPokemonId != nil ||
		got.DisplayPokemonForm != nil || got.SeenType != nil || got.Shiny != nil || got.Username != nil {
		t.Errorf("nil API pointers must map to unset proto optionals: %+v", got)
	}
	if got.GetPvp() == nil {
		t.Error("pvp must always be present (an empty rankings message), matching the JSON {} envelope")
	}
	assertNoPresentOptionals(t, got, "id", "lat", "lon", "pokemon_id", "first_seen_timestamp", "changed", "pvp")
}

// withScanLimits gives the decoder test binary the production result caps so
// limit_reached reflects the scan and not a zero default.
func withScanLimits(t *testing.T) {
	t.Helper()
	prevP, prevF := config.Config.Tuning.MaxPokemonResults, config.Config.Tuning.MaxFortResults
	config.Config.Tuning.MaxPokemonResults, config.Config.Tuning.MaxFortResults = 3000, 9000
	t.Cleanup(func() {
		config.Config.Tuning.MaxPokemonResults, config.Config.Tuning.MaxFortResults = prevP, prevF
	})
}

func TestGrpcScanPokemonReturnsLivePokemon(t *testing.T) {
	withScanLimits(t)
	const id = uint64(940001)
	const lat, lon = 12.25, 34.75
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: lat, Lon: lon, PokemonId: 25}}
	p.ExpireTimestamp = null.ValueFrom(uint32(time.Now().Unix() + 600))
	p.Cp = null.ValueFrom(uint16(777))

	pokemonRtreePreloadInsert(p)
	pokemonCache.Set(id, p, time.Minute)
	pokemonTreeSnapshot.Store(nil) // force the next scan to see the fresh tree
	t.Cleanup(func() {
		pokemonCache.Delete(id)
		pokemonLookupCache.Delete(id)
		pokemonTreeMutex.Lock()
		pokemonTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, id)
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})

	// One clause with no conditions is the catch-all; an empty filters list
	// matches nothing (JSON parity: the DNF map then has no entry to hit).
	resp := GrpcScanPokemon(&pb.PokemonScanRequest{
		Min:     &pb.LatLon{Lat: lat - 0.01, Lon: lon - 0.01},
		Max:     &pb.LatLon{Lat: lat + 0.01, Lon: lon + 0.01},
		Filters: []*pb.PokemonDnfFilter{{}},
	})
	if len(resp.Pokemon) != 1 {
		t.Fatalf("got %d pokemon, want 1 (examined %d, skipped %d)", len(resp.Pokemon), resp.Examined, resp.Skipped)
	}
	got := resp.Pokemon[0]
	if got.Id != id || got.PokemonId != 25 || got.Lat != lat || got.Lon != lon || got.GetCp() != 777 {
		t.Errorf("pokemon = %+v", got)
	}
	if resp.Examined < 1 || resp.Total < 1 || resp.LimitReached {
		t.Errorf("counts = examined %d skipped %d total %d limit_reached %v", resp.Examined, resp.Skipped, resp.Total, resp.LimitReached)
	}
	noFilters := GrpcScanPokemon(&pb.PokemonScanRequest{
		Min: &pb.LatLon{Lat: lat - 0.01, Lon: lon - 0.01},
		Max: &pb.LatLon{Lat: lat + 0.01, Lon: lon + 0.01},
	})
	if len(noFilters.Pokemon) != 0 {
		t.Errorf("an empty filters list must match nothing (JSON parity), got %d", len(noFilters.Pokemon))
	}

	// The same pokemon by id; an unknown id is omitted, not a placeholder.
	byId := GrpcGetPokemon([]uint64{id, 1})
	if len(byId) != 1 || byId[0].Id != id {
		t.Errorf("GetPokemon = %+v, want only id %d", byId, id)
	}
}

func TestGrpcScanPokemonEmptyBoxHasNoResults(t *testing.T) {
	withScanLimits(t)
	resp := GrpcScanPokemon(&pb.PokemonScanRequest{
		Min: &pb.LatLon{Lat: -89.9, Lon: -179.9},
		Max: &pb.LatLon{Lat: -89.8, Lon: -179.8},
	})
	if len(resp.Pokemon) != 0 || resp.LimitReached {
		t.Errorf("empty box: %d results, limit_reached %v", len(resp.Pokemon), resp.LimitReached)
	}
}
