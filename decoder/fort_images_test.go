package decoder

import (
	"encoding/json"
	"testing"

	"golbat/pogo"
)

func TestFortImagesJson(t *testing.T) {
	if got := fortImagesJson(nil); got.Valid {
		t.Errorf("empty gallery = %q, want NULL", got.String)
	}

	images := []*pogo.TitanGameClientPhotoGalleryPoiImageProto{
		{ImageId: "b", PoiId: "p", SubmitterCodename: "ash", ImageUrl: "https://img/b", CreationTimestampMs: 200,
			HasPlayerVoted: true, NumVotesFromGame: 2, NumVotesNianticWide: 5},
		{ImageId: ""}, // nothing to key it by
		{ImageId: "a", PoiId: "p", ImageUrl: "https://img/a", CreationTimestampMs: 100},
	}
	const want = `[{"image_id":"a","image_url":"https://img/a","creation_timestamp_ms":100,"num_votes_from_game":0,"num_votes_niantic_wide":0},` +
		`{"image_id":"b","image_url":"https://img/b","creation_timestamp_ms":200,"num_votes_from_game":2,"num_votes_niantic_wide":5}]`
	got := fortImagesJson(images)
	if !got.Valid || got.String != want {
		t.Errorf("gallery = %s\nwant      %s", got.String, want)
	}

	// The order the server answers in must not change what is stored.
	images[0], images[2] = images[2], images[0]
	if again := fortImagesJson(images); again != got {
		t.Errorf("reordered gallery = %s", again.String)
	}
}

func TestFortImagesChanged(t *testing.T) {
	raw := func(s string) *json.RawMessage { r := json.RawMessage(s); return &r }
	cases := []struct {
		name     string
		old, new *json.RawMessage
		want     bool
	}{
		{"both unset", nil, nil, false},
		{"first gallery", nil, raw(`[{"image_id":"a"}]`), true},
		{"gallery emptied", raw(`[{"image_id":"a"}]`), nil, true},
		{"unchanged", raw(`[{"image_id":"a","num_votes_niantic_wide":1}]`), raw(`[{"image_id":"a","num_votes_niantic_wide":1}]`), false},
		{"vote moved", raw(`[{"image_id":"a","num_votes_niantic_wide":1}]`), raw(`[{"image_id":"a","num_votes_niantic_wide":2}]`), true},
	}
	for _, c := range cases {
		if got := fortImagesChanged(c.old, c.new); got != c.want {
			t.Errorf("%s: changed = %v, want %v", c.name, got, c.want)
		}
	}
}
