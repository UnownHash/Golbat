package decoder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/config"
	"golbat/derive/encounter"
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
	r.SizeFor = map[string]ApiPersonalSize{"player-id": {Height: 0.71, Weight: 6.52}}
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
	if !sizesMatch(got.SizeFor, r.SizeFor) {
		t.Errorf("size_for = %v, want %v", got.SizeFor, r.SizeFor)
	}
}

// sizesMatch reports whether a proto size_for carries exactly the HTTP one.
func sizesMatch(got map[string]*pb.PersonalSize, want map[string]ApiPersonalSize) bool {
	if len(got) != len(want) {
		return false
	}
	for player, w := range want {
		if g, ok := got[player]; !ok || g.GetHeight() != w.Height || g.GetWeight() != w.Weight {
			return false
		}
	}
	return true
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

// mustGrpcScanPokemon runs a scan that has no reason to fail.
func mustGrpcScanPokemon(t *testing.T, req *pb.PokemonScanRequest) *pb.PokemonScanResponse {
	t.Helper()
	resp, err := GrpcScanPokemon(context.Background(), req)
	if err != nil {
		t.Fatalf("GrpcScanPokemon: %v", err)
	}
	return resp
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
	resp := mustGrpcScanPokemon(t, &pb.PokemonScanRequest{
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
	noFilters := mustGrpcScanPokemon(t, &pb.PokemonScanRequest{
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
	resp := mustGrpcScanPokemon(t, &pb.PokemonScanRequest{
		Min: &pb.LatLon{Lat: -89.9, Lon: -179.9},
		Max: &pb.LatLon{Lat: -89.8, Lon: -179.8},
	})
	if len(resp.Pokemon) != 0 || resp.LimitReached {
		t.Errorf("empty box: %d results, limit_reached %v", len(resp.Pokemon), resp.LimitReached)
	}
}

// TestGrpcPersonalShinyScan mirrors TestPersonalShinyScan through the gRPC
// adapter, and checks each answer against the HTTP entry point. Rates and
// rolls as there: encounter 1 in Phoenix, shiny 0.05 and background 0.15.
func TestGrpcPersonalShinyScan(t *testing.T) {
	withScanLimits(t)
	const lat, lon = 33.45, -112.07
	oldConfig := config.Config.ShinyRates
	config.Config.ShinyRates = config.ShinyRates{URL: "http://rates.invalid"}
	shinyRatesSnapshot.Store(nil)
	t.Cleanup(func() {
		config.Config.ShinyRates = oldConfig
		shinyRatesSnapshot.Store(nil)
	})
	p := &Pokemon{PokemonData: PokemonData{Id: 1, PokemonId: 25, Lat: lat, Lon: lon, SeenType: SeenTypeFrom(SeenTypeCodeWild), ExpireTimestamp: null.ValueFrom(uint32(time.Now().Unix() + 600))}}
	pokemonRtreePreloadInsert(p)
	pokemonCache.Set(1, p, time.Minute)
	pokemonTreeSnapshot.Store(nil)
	t.Cleanup(func() {
		pokemonCache.Delete(1)
		pokemonLookupCache.Delete(1)
		pokemonTreeMutex.Lock()
		pokemonTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, 1)
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})
	const hex, abc, trainer = "0123456789abcdef", "Player-ID:ABC/123", " Trainer-é "
	box := func(filters []*pb.PokemonDnfFilter, annotate ...string) *pb.PokemonScanRequest {
		return &pb.PokemonScanRequest{
			Min:         &pb.LatLon{Lat: lat - 0.01, Lon: lon - 0.01},
			Max:         &pb.LatLon{Lat: lat + 0.01, Lon: lon + 0.01},
			Filters:     filters,
			AnnotateFor: annotate,
		}
	}
	plain := box([]*pb.PokemonDnfFilter{{}})
	personal := box([]*pb.PokemonDnfFilter{{ShinyFor: []string{trainer}}})

	// Without rates, personal scans fail rather than guess; others run.
	for _, tc := range []struct {
		url  string
		want error
	}{{"", ErrShinyRatesNotConfigured}, {"http://rates.invalid", ErrShinyRatesNotLoaded}} {
		config.Config.ShinyRates.URL = tc.url
		if _, err := GrpcScanPokemon(context.Background(), personal); !errors.Is(err, tc.want) {
			t.Errorf("url %q: err = %v, want %v", tc.url, err, tc.want)
		}
		if _, err := GrpcScanPokemon(context.Background(), box(nil, hex)); !errors.Is(err, tc.want) {
			t.Errorf("url %q annotate_for: err = %v, want %v", tc.url, err, tc.want)
		}
		if resp := mustGrpcScanPokemon(t, plain); len(resp.Pokemon) != 1 || resp.Pokemon[0].ShinyFor != nil {
			t.Errorf("url %q: plain scan = %+v", tc.url, resp.Pokemon)
		}
	}
	config.Config.ShinyRates.URL = "http://rates.invalid"
	background := &shinyPrediction{Probability: 0.2, Supported: true}
	shinyRatesSnapshot.Store(newShinyRates([]shinyRate{{
		Pokemon: 25, Prediction: shinyPrediction{Probability: 0.1, Supported: true}, Background: background,
		Overrides:           map[string]shinyPrediction{"America/Phoenix": {Probability: 0.05, Supported: true}},
		BackgroundOverrides: map[string]shinyPrediction{"America/Phoenix": {Probability: 0.15, Supported: true}},
	}}, nil, nil))

	// scan runs req over gRPC and HTTP and requires the same pokemon with
	// the same annotations from both.
	scan := func(name string, req *pb.PokemonScanRequest) []*pb.Pokemon {
		t.Helper()
		resp, err := GrpcScanPokemon(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		viaHTTP, err := ScanPokemonWithShiny(context.Background(), pokemonScanRequestFromProto(req))
		if err != nil {
			t.Fatalf("%s: HTTP: %v", name, err)
		}
		if len(viaHTTP.Pokemon) != len(resp.Pokemon) {
			t.Fatalf("%s: gRPC %d results, HTTP %d", name, len(resp.Pokemon), len(viaHTTP.Pokemon))
		}
		for i, r := range viaHTTP.Pokemon {
			if g := resp.Pokemon[i]; !slices.Equal(g.ShinyFor, r.ShinyFor) || !slices.Equal(g.BackgroundFor, r.BackgroundFor) || !sizesMatch(g.SizeFor, r.SizeFor) {
				t.Errorf("%s: gRPC %v/%v/%v, HTTP %v/%v/%v", name, g.ShinyFor, g.BackgroundFor, g.SizeFor, r.ShinyFor, r.BackgroundFor, r.SizeFor)
			}
		}
		return resp.Pokemon
	}
	form0 := ptr(int32(0))
	for _, tc := range []struct {
		name    string
		filters []*pb.PokemonDnfFilter
		want    int
	}{
		{"not shiny", []*pb.PokemonDnfFilter{{ShinyFor: []string{abc}}}, 0},
		{"shiny for any listed player", []*pb.PokemonDnfFilter{{ShinyFor: []string{abc, trainer}}}, 1},
		{"ANDed with ordinary conditions", []*pb.PokemonDnfFilter{{ShinyFor: []string{trainer}, Cp: &pb.IntRange{Min: ptr(int32(100)), Max: ptr(int32(200))}}}, 0},
		{"background", []*pb.PokemonDnfFilter{{BackgroundFor: []string{hex}}}, 1},
		{"no background", []*pb.PokemonDnfFilter{{BackgroundFor: []string{abc}}}, 0},
		{"shiny_for AND background_for", []*pb.PokemonDnfFilter{{ShinyFor: []string{abc}, BackgroundFor: []string{hex}}}, 0},
		{"shiny_for AND background_for, both hit", []*pb.PokemonDnfFilter{{ShinyFor: []string{trainer}, BackgroundFor: []string{hex}}}, 1},
		{"clauses OR", []*pb.PokemonDnfFilter{{ShinyFor: []string{abc}}, {}}, 1},
		{"species group shadows everything else",
			[]*pb.PokemonDnfFilter{{ShinyFor: []string{trainer}}, {Pokemon: []*pb.DnfId{{PokemonId: 25}}, Iv: &pb.IntRange{Min: ptr(int32(100)), Max: ptr(int32(100))}}}, 0},
		{"exact form shadows the species",
			[]*pb.PokemonDnfFilter{{Pokemon: []*pb.DnfId{{PokemonId: 25, Form: form0}}, ShinyFor: []string{abc}}, {Pokemon: []*pb.DnfId{{PokemonId: 25}}, ShinyFor: []string{trainer}}}, 0},
	} {
		if got := scan(tc.name, box(tc.filters)); len(got) != tc.want {
			t.Errorf("%s: got %d results want %d", tc.name, len(got), tc.want)
		}
	}

	// Annotation marks results without filtering them; duplicates count once.
	got := scan("annotate_for", box([]*pb.PokemonDnfFilter{{}}, hex, abc, trainer, trainer))
	if len(got) != 1 || !slices.Equal(got[0].ShinyFor, []string{trainer}) || !slices.Equal(got[0].BackgroundFor, []string{hex}) {
		t.Fatalf("annotations = %+v", got)
	}
	if got := mustGrpcScanPokemon(t, plain); got.Pokemon[0].ShinyFor != nil || got.Pokemon[0].BackgroundFor != nil {
		t.Errorf("annotations without annotate_for: %+v", got.Pokemon[0])
	}

	// Malformed player IDs are client errors, wherever they appear.
	many := make([]string, maxPersonalPlayers+1)
	for i := range many {
		many[i] = fmt.Sprint("player-", i)
	}
	for name, req := range map[string]*pb.PokemonScanRequest{
		"empty ID":           box([]*pb.PokemonDnfFilter{{ShinyFor: []string{""}}}),
		"oversized ID":       box([]*pb.PokemonDnfFilter{{BackgroundFor: []string{strings.Repeat("x", maxPersonalPlayerIDBytes+1)}}}),
		"oversized annotate": box([]*pb.PokemonDnfFilter{{}}, strings.Repeat("x", maxPersonalPlayerIDBytes+1)),
		"too many players":   box([]*pb.PokemonDnfFilter{{ShinyFor: many[:25]}}, many[25:]...),
	} {
		if _, err := GrpcScanPokemon(context.Background(), req); !errors.Is(err, ErrPersonalRequest) {
			t.Errorf("%s: err = %v, want ErrPersonalRequest", name, err)
		}
	}
	if _, err := GrpcScanPokemon(context.Background(), box([]*pb.PokemonDnfFilter{{ShinyFor: []string{strings.Repeat("x", maxPersonalPlayerIDBytes)}}})); err != nil {
		t.Errorf("ID at the byte cap: %v", err)
	}

	// A cancelled request fails instead of returning a partial answer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GrpcScanPokemon(ctx, personal); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v", err)
	}

	// Nearby sightings are never eligible: not matched, not annotated.
	p.SeenType = SeenTypeFrom(SeenTypeCodeNearbyStop)
	updatePokemonLookup(p, false, nil)
	if got := scan("nearby shiny_for", personal); len(got) != 0 {
		t.Errorf("nearby matched shiny_for: %+v", got)
	}
	if got := scan("nearby annotate_for", box([]*pb.PokemonDnfFilter{{}}, trainer, hex)); len(got) != 1 || got[0].ShinyFor != nil || got[0].BackgroundFor != nil {
		t.Errorf("nearby annotations = %+v", got)
	}
}

// TestGrpcPersonalDittoScan checks a known Ditto over gRPC against HTTP.
// Encounter 6 is a Ditto shiny for Player-ID:ABC/123 only (see
// TestPersonalDittoShiny), while its Pidgey disguise would be shiny and have
// a background for everyone and the feed lists 132 as never shiny.
func TestGrpcPersonalDittoScan(t *testing.T) {
	withScanLimits(t)
	const id = uint64(6)
	const lat, lon = 12.75, 34.25
	oldConfig := config.Config.ShinyRates
	config.Config.ShinyRates = config.ShinyRates{URL: "http://rates.invalid"}
	always := shinyPrediction{Probability: 1, Supported: true}
	shinyRatesSnapshot.Store(newShinyRates([]shinyRate{{Pokemon: 16, Prediction: always, Background: &always}}, []int{dittoPokemonId}, nil))
	t.Cleanup(func() {
		config.Config.ShinyRates = oldConfig
		shinyRatesSnapshot.Store(nil)
	})
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), PokemonId: dittoPokemonId, IsDitto: true, DisplayPokemonId: null.ValueFrom(uint16(16)),
		Lat: lat, Lon: lon, SeenType: SeenTypeFrom(SeenTypeCodeWild), ExpireTimestamp: null.ValueFrom(uint32(time.Now().Unix() + 600))}}
	pokemonRtreePreloadInsert(p)
	pokemonCache.Set(id, p, time.Minute)
	pokemonTreeSnapshot.Store(nil)
	t.Cleanup(func() {
		pokemonCache.Delete(id)
		pokemonLookupCache.Delete(id)
		pokemonTreeMutex.Lock()
		pokemonTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, id)
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})
	const hex, abc, trainer = "0123456789abcdef", "Player-ID:ABC/123", " Trainer-é "
	box := func(filters []*pb.PokemonDnfFilter, annotate ...string) *pb.PokemonScanRequest {
		return &pb.PokemonScanRequest{
			Min:         &pb.LatLon{Lat: lat - 0.01, Lon: lon - 0.01},
			Max:         &pb.LatLon{Lat: lat + 0.01, Lon: lon + 0.01},
			Filters:     filters,
			AnnotateFor: annotate,
		}
	}
	for _, tc := range []struct {
		name              string
		req               *pb.PokemonScanRequest
		want              int
		shiny, background []string
	}{
		{"shiny_for", box([]*pb.PokemonDnfFilter{{ShinyFor: []string{abc}}}), 1, nil, nil},
		{"not shiny_for", box([]*pb.PokemonDnfFilter{{ShinyFor: []string{hex, trainer}}}), 0, nil, nil},
		{"background_for", box([]*pb.PokemonDnfFilter{{BackgroundFor: []string{hex, abc, trainer}}}), 0, nil, nil},
		{"annotate_for", box([]*pb.PokemonDnfFilter{{}}, hex, abc, trainer), 1, []string{abc}, nil},
	} {
		resp := mustGrpcScanPokemon(t, tc.req)
		viaHTTP, err := ScanPokemonWithShiny(context.Background(), pokemonScanRequestFromProto(tc.req))
		if err != nil {
			t.Fatalf("%s: HTTP: %v", tc.name, err)
		}
		if len(resp.Pokemon) != tc.want || len(viaHTTP.Pokemon) != tc.want {
			t.Fatalf("%s: gRPC %d results, HTTP %d, want %d", tc.name, len(resp.Pokemon), len(viaHTTP.Pokemon), tc.want)
		}
		for i, r := range viaHTTP.Pokemon {
			g := resp.Pokemon[i]
			if !slices.Equal(g.ShinyFor, tc.shiny) || !slices.Equal(r.ShinyFor, tc.shiny) ||
				!slices.Equal(g.BackgroundFor, tc.background) || !slices.Equal(r.BackgroundFor, tc.background) ||
				g.SizeFor != nil || r.SizeFor != nil {
				t.Errorf("%s: gRPC %v/%v/%v, HTTP %v/%v/%v", tc.name, g.ShinyFor, g.BackgroundFor, g.SizeFor, r.ShinyFor, r.BackgroundFor, r.SizeFor)
			}
		}
	}
}

// TestGrpcPersonalSizeScan mirrors TestPersonalSizeAnnotation through a full
// gRPC scan: size_for carries each annotate_for player's height and weight,
// identical to the HTTP scan, and is empty when the IVs don't match the
// encounter derivation.
func TestGrpcPersonalSizeScan(t *testing.T) {
	withScanLimits(t)
	oldMasterFile := getMasterFileData()
	oldConfig := config.Config.ShinyRates
	config.Config.ShinyRates = config.ShinyRates{URL: "http://rates.invalid"}
	shinyRatesSnapshot.Store(newShinyRates(nil, nil, nil))
	masterFileMu.Lock()
	masterFileData = MasterFileData{Initialized: true, Pokemon: map[int]MasterFilePokemon{
		1: {Size: MasterFileSize{Height: 0.7, Weight: 6.9, Bounds: sizeBounds{0.343, 0.35, 0.525, 0.875, 1.05, 1.225}}},
	}}
	masterFileMu.Unlock()
	t.Cleanup(func() {
		config.Config.ShinyRates = oldConfig
		shinyRatesSnapshot.Store(nil)
		masterFileMu.Lock()
		masterFileData = oldMasterFile
		masterFileMu.Unlock()
	})

	const encounterID = 8429844873227918432
	const lat, lon = 33.45, -112.07
	draw := encounter.For(encounterID, false, 0)
	p := &Pokemon{PokemonData: PokemonData{Id: encounterID, PokemonId: 1, Lat: lat, Lon: lon,
		SeenType: SeenTypeFrom(SeenTypeCodeWild), ExpireTimestamp: null.ValueFrom(uint32(time.Now().Unix() + 600)),
		AtkIv: null.ValueFrom(uint8(draw.Attack)), DefIv: null.ValueFrom(uint8(draw.Defense)), StaIv: null.ValueFrom(uint8(draw.Stamina)),
		Level: null.ValueFrom(uint8(draw.Level)), Weather: null.ValueFrom(uint8(0))}}
	pokemonRtreePreloadInsert(p)
	pokemonCache.Set(encounterID, p, time.Minute)
	pokemonTreeSnapshot.Store(nil)
	t.Cleanup(func() {
		pokemonCache.Delete(encounterID)
		pokemonLookupCache.Delete(encounterID)
		pokemonTreeMutex.Lock()
		pokemonTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, encounterID)
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})

	const player = "0123456789abcdef"
	req := &pb.PokemonScanRequest{
		Min:         &pb.LatLon{Lat: lat - 0.01, Lon: lon - 0.01},
		Max:         &pb.LatLon{Lat: lat + 0.01, Lon: lon + 0.01},
		Filters:     []*pb.PokemonDnfFilter{{}},
		AnnotateFor: []string{player},
	}
	resp := mustGrpcScanPokemon(t, req)
	if len(resp.Pokemon) != 1 {
		t.Fatalf("results = %+v", resp.Pokemon)
	}
	size, ok := resp.Pokemon[0].SizeFor[player]
	if !ok {
		t.Fatalf("no size: %+v", resp.Pokemon[0].SizeFor)
	}
	// Unknown variant: 1/512 shiny draw, no background, then the height.
	input := make([]byte, 8+len(player))
	copy(input[8:], player)
	rng := (&shinyPlayer{input: input}).generator(encounterID)
	rng.Next(24)
	height, weight := encounterSize{class: draw.Size, noise: draw.WeightNoise, MasterFileSize: masterFileData.Pokemon[1].Size}.personal(rng)
	if size.GetHeight() != float32(height) || size.GetWeight() != float32(weight) {
		t.Fatalf("size %+v, want %v/%v", size, height, weight)
	}
	viaHTTP, err := ScanPokemonWithShiny(context.Background(), pokemonScanRequestFromProto(req))
	if err != nil {
		t.Fatalf("HTTP: %v", err)
	}
	if len(viaHTTP.Pokemon) != 1 || !sizesMatch(resp.Pokemon[0].SizeFor, viaHTTP.Pokemon[0].SizeFor) {
		t.Fatalf("gRPC %v, HTTP %+v", resp.Pokemon[0].SizeFor, viaHTTP.Pokemon)
	}

	// Without annotate_for, or with IVs the derivation doesn't produce, no size.
	req.AnnotateFor = nil
	if got := mustGrpcScanPokemon(t, req); len(got.Pokemon) != 1 || got.Pokemon[0].SizeFor != nil {
		t.Errorf("size_for without annotate_for: %+v", got.Pokemon)
	}
	if draw.Attack != draw.Defense {
		p.AtkIv, p.DefIv = p.DefIv, p.AtkIv
		req.AnnotateFor = []string{player}
		if got := mustGrpcScanPokemon(t, req); len(got.Pokemon) != 1 || got.Pokemon[0].SizeFor != nil {
			t.Errorf("mismatched derivation still sized: %+v", got.Pokemon)
		}
	}
}
