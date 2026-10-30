package decoder

import (
	"encoding/json"
	"math"
	"testing"

	gojson "github.com/goccy/go-json"
	"github.com/guregu/null/v6"

	pb "golbat/grpc"
)

// An omitted or null max is unbounded, as gRPC's intRangeBounds resolves it.
// Decoded through both codecs: huma reads request bodies with goccy/go-json.
func TestDnfMinMaxOmittedMaxIsUnbounded(t *testing.T) {
	codecs := map[string]func([]byte, any) error{"encoding/json": json.Unmarshal, "goccy/go-json": gojson.Unmarshal}
	cases := map[string]struct {
		body     string
		min, max int16
	}{
		"min only":   {`{"min":500}`, 500, math.MaxInt16},
		"both":       {`{"min":1,"max":5}`, 1, 5},
		"empty":      {`{}`, 0, math.MaxInt16},
		"null max":   {`{"min":3,"max":null}`, 3, math.MaxInt16},
		"max only":   {`{"max":7}`, 0, 7},
		"explicit 0": {`{"min":0,"max":0}`, 0, 0},
	}
	for codecName, unmarshal := range codecs {
		for name, tc := range cases {
			var f ApiFortDnfMinMax
			if err := unmarshal([]byte(tc.body), &f); err != nil || f.Min != tc.min || f.Max != tc.max {
				t.Errorf("%s fort %s: got %+v err %v, want {%d %d}", codecName, name, f, err, tc.min, tc.max)
			}
			var p ApiPokemonDnfMinMax
			if err := unmarshal([]byte(tc.body), &p); err != nil || p.Min != tc.min || p.Max != tc.max {
				t.Errorf("%s pokemon %s: got %+v err %v, want {%d %d}", codecName, name, p, err, tc.min, tc.max)
			}
		}
	}
}

// HTTP and gRPC resolve the same range identically.
func TestDnfMinMaxMatchesGrpc(t *testing.T) {
	min := int32(500)
	grpc := intRangeToFortMinMax(&pb.IntRange{Min: &min})
	var http ApiFortDnfMinMax
	if err := gojson.Unmarshal([]byte(`{"min":500}`), &http); err != nil || http != *grpc {
		t.Errorf("http %+v (err %v) != grpc %+v", http, err, *grpc)
	}
}

func TestTappableScanCountMinOnly(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 930111, 42.001, 52.001, func(d *TappableData) { d.RewardType, d.Count = null.StringFrom("stardust"), null.IntFrom(100) })
	seedTappable(t, 930112, 42.002, 52.002, func(d *TappableData) { d.RewardType, d.Count = null.StringFrom("stardust"), null.IntFrom(500) })
	var req ApiTappableScan
	body := `{"min":{"lat":42,"lon":52},"max":{"lat":42.01,"lon":52.01},"filters":[{"reward_type":["stardust"],"count":{"min":500}}]}`
	if err := gojson.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	_, ids := scanIds(t, req)
	if len(ids) != 1 || !ids[930112] {
		t.Errorf("ids = %v, want only 930112", ids)
	}
}
