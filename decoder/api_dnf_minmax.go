package decoder

import (
	"encoding/json"
	"math"
)

// An omitted or null max is unbounded (MaxInt16), matching gRPC's
// intRangeBounds. Resolved at decode time so the scan hot paths compare plain
// int16s.
func unmarshalDnfMinMax(b []byte) (minV, maxV int16, err error) {
	var raw struct {
		Min int16  `json:"min"`
		Max *int16 `json:"max"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return 0, 0, err
	}
	maxV = math.MaxInt16
	if raw.Max != nil {
		maxV = *raw.Max
	}
	return raw.Min, maxV, nil
}

func (r *ApiFortDnfMinMax) UnmarshalJSON(b []byte) (err error) {
	r.Min, r.Max, err = unmarshalDnfMinMax(b)
	return err
}

func (r *ApiPokemonDnfMinMax) UnmarshalJSON(b []byte) (err error) {
	r.Min, r.Max, err = unmarshalDnfMinMax(b)
	return err
}
