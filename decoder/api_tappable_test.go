package decoder

import (
	"testing"

	"github.com/guregu/null/v6"

	"golbat/jsonenc"
)

// goldenSnapshotTappable is a representative tappable with a mix of set and
// unset (null) fields across the nullable columns, used to pin the exact wire
// format.
func goldenSnapshotTappable() *Tappable {
	return &Tappable{
		TappableData: TappableData{
			Id:     123456789,
			Lat:    45.6789,
			Lon:    -120.9876,
			FortId: null.StringFrom("fort-abc"),
			// SpawnId intentionally left null
			Type:      "item",
			Encounter: null.IntFrom(150),
			ItemId:    null.IntFrom(1),
			// Count intentionally left null
			ExpireTimestamp:         null.IntFrom(1700001000),
			ExpireTimestampVerified: true,
			Updated:                 1699999999,
		},
	}
}

// TestBuildTappableResult_GoldenSnapshot pins the exact JSON wire format of an
// ApiTappableResult. Any accidental change to a json tag, field type,
// pointer/null handling, or field order will fail this test. Unset nullable
// fields serialize as null (pointers are nil, no omitempty).
//
// Marshals through jsonenc, not encoding/json directly, so this test tracks
// whichever codec the current build selects instead of always pinning
// stdlib's output — see jsonenc's package doc for what -tags go_json does
// and doesn't gate (it does not gate huma_api.go, which serves this struct
// through goccy/go-json unconditionally either way).
func TestBuildTappableResult_GoldenSnapshot(t *testing.T) {
	got, err := jsonenc.Marshal(BuildTappableResult(goldenSnapshotTappable()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"id":123456789,"lat":45.6789,"lon":-120.9876,"fort_id":"fort-abc","spawn_id":null,"type":"item","pokemon_id":150,"item_id":1,"count":null,"expire_timestamp":1700001000,"expire_timestamp_verified":true,"updated":1699999999}`

	if string(got) != want {
		t.Errorf("wire format changed.\n got: %s\nwant: %s", got, want)
	}
}
