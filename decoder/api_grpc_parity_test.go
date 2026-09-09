package decoder

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "golbat/grpc"
)

// Raw-JSON passthrough types map to a proto string field named <tag>_json.
var rawJsonFieldTypes = map[reflect.Type]bool{
	reflect.TypeOf((*json.RawMessage)(nil)):       true,
	reflect.TypeOf(ApiGymGuardingPokemonRaw(nil)): true,
	reflect.TypeOf(ApiGymDefendersRaw(nil)):       true,
}

// expectedProtoFieldNames derives, for every exported field of an API struct,
// the proto field name the spec requires: the JSON tag, plus "_json" for
// raw-JSON passthrough fields, with per-struct overrides for the two named
// exceptions.
func expectedProtoFieldNames(t *testing.T, typ reflect.Type, overrides map[string]string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			t.Fatalf("%s.%s has no json name; the proto mirror needs one", typ.Name(), f.Name)
		}
		if o, ok := overrides[tag]; ok {
			tag = o
		}
		if rawJsonFieldTypes[f.Type] {
			tag += "_json"
		}
		names[tag] = true
	}
	return names
}

// TestGrpcApiFieldParity locks the HTTP structs and the proto messages
// together: a field added to one without the other fails here, in both
// directions.
func TestGrpcApiFieldParity(t *testing.T) {
	cases := []struct {
		name      string
		goType    reflect.Type
		msg       proto.Message
		overrides map[string]string
	}{
		{"Pokemon", reflect.TypeOf(ApiPokemonResult{}), &pb.Pokemon{}, nil},
		{"PvpRankings", reflect.TypeOf(ApiPvpRankings{}), &pb.PvpRankings{}, nil},
		{"PvpEntry", reflect.TypeOf(ApiPvpEntry{}), &pb.PvpEntry{}, nil},
		{"Gym", reflect.TypeOf(ApiGymResult{}), &pb.Gym{}, nil},
		{"Pokestop", reflect.TypeOf(ApiPokestopResult{}), &pb.Pokestop{}, nil},
		{"Incident", reflect.TypeOf(ApiPokestopIncident{}), &pb.Incident{}, nil},
		{"Station", reflect.TypeOf(ApiStationResult{}), &pb.Station{}, nil},
		{"StationBattle", reflect.TypeOf(ApiStationBattleResult{}), &pb.StationBattle{}, nil},
		{"LatLon", reflect.TypeOf(ApiLatLon{}), &pb.LatLon{}, nil},
		{"PokemonScanRequest", reflect.TypeOf(ApiPokemonScan3{}), &pb.PokemonScanRequest{}, nil},
		{"PokemonDnfFilter", reflect.TypeOf(ApiPokemonDnfFilter3{}), &pb.PokemonDnfFilter{}, nil},
		// ApiPokemonDnfId uses json "id"; the shared DnfId message uses pokemon_id.
		{"DnfId(pokemon)", reflect.TypeOf(ApiPokemonDnfId{}), &pb.DnfId{}, map[string]string{"id": "pokemon_id"}},
		{"DnfId(fort)", reflect.TypeOf(ApiDnfId{}), &pb.DnfId{}, nil},
		{"IntRange(pokemon)", reflect.TypeOf(ApiPokemonDnfMinMax{}), &pb.IntRange{}, nil},
		{"IntRange(fort)", reflect.TypeOf(ApiFortDnfMinMax{}), &pb.IntRange{}, nil},
		{"FortScanRequest", reflect.TypeOf(ApiFortScan{}), &pb.FortScanRequest{}, nil},
		{"FortTypeScanGroup", reflect.TypeOf(ApiFortTypeScanGroup{}), &pb.FortTypeScanGroup{}, nil},
		{"FortTypeScanStats", reflect.TypeOf(ApiFortTypeScanStats{}), &pb.FortTypeScanStats{}, nil},
		{"FortScanResponse", reflect.TypeOf(ApiFortCombinedScanResult{}), &pb.FortScanResponse{}, nil},
		{"GymScanResponse", reflect.TypeOf(ApiGymScanResult{}), &pb.GymScanResponse{}, nil},
		{"PokestopScanResponse", reflect.TypeOf(ApiPokestopScanResult{}), &pb.PokestopScanResponse{}, nil},
		{"StationScanResponse", reflect.TypeOf(ApiStationScanResult{}), &pb.StationScanResponse{}, nil},
		{"FortCombinedScanRequest", reflect.TypeOf(ApiFortCombinedScan{}), &pb.FortCombinedScanRequest{}, nil},
		{"FortDnfFilter", reflect.TypeOf(ApiFortDnfFilter{}), &pb.FortDnfFilter{}, nil},
		{"ContestFocus", reflect.TypeOf(ApiFortDnfContestFocus{}), &pb.ContestFocus{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := expectedProtoFieldNames(t, tc.goType, tc.overrides)
			fields := tc.msg.ProtoReflect().Descriptor().Fields()
			got := map[string]bool{}
			for i := 0; i < fields.Len(); i++ {
				got[string(fields.Get(i).Name())] = true
			}
			for name := range want {
				if !got[name] {
					t.Errorf("proto %s lacks field %q that %s carries", tc.name, name, tc.goType.Name())
				}
			}
			for name := range got {
				if !want[name] {
					t.Errorf("proto %s has field %q with no counterpart on %s", tc.name, name, tc.goType.Name())
				}
			}
		})
	}
}
