package decoder

import (
	"reflect"
	"strings"
	"testing"

	"github.com/guregu/null/v6"

	"golbat/config"
	pb "golbat/grpc"
	"golbat/pogo"
)

func TestWayfarerPoiUpdateFromProto(t *testing.T) {
	poi := &WayfarerPoi{WayfarerPoiData: WayfarerPoiData{Id: "abc.16"}}
	proto := &pogo.TitanGeodataServiceGameClientPoiProto{
		PoiId:       "abc.16",
		Title:       "Fountain",
		Description: "A fountain",
		Location:    &pogo.LocationE6Proto{LatitudeE6: 12345678, LongitudeE6: -98765432},
		ImageUrl:    "https://img/fountain",
		IsInGame:    true,
	}
	poi.updateFromGeodataPoiProto(proto)

	want := WayfarerPoiData{
		Id: "abc.16", Lat: 12.345678, Lon: -98.765432,
		Name: null.StringFrom("Fountain"), Description: null.StringFrom("A fountain"),
		Url: null.StringFrom("https://img/fountain"), InGame: true,
	}
	if poi.WayfarerPoiData != want {
		t.Errorf("poi = %+v\nwant  %+v", poi.WayfarerPoiData, want)
	}
	if !poi.IsDirty() {
		t.Error("a changed poi must be dirty")
	}

	// The same answer again changes nothing, so it is not written again.
	poi.ClearDirty()
	poi.updateFromGeodataPoiProto(proto)
	if poi.IsDirty() {
		t.Error("an unchanged poi must not be dirty")
	}

	// Empty strings are NULL; an over-long title is cut to the column width.
	poi.updateFromGeodataPoiProto(&pogo.TitanGeodataServiceGameClientPoiProto{
		PoiId:    "abc.16",
		Title:    strings.Repeat("\u00e9", 200),
		Location: &pogo.LocationE6Proto{LatitudeE6: 12345678, LongitudeE6: -98765432},
	})
	if poi.Description.Valid || poi.Url.Valid || poi.InGame {
		t.Errorf("cleared poi = %+v", poi.WayfarerPoiData)
	}
	if got := len([]rune(poi.Name.String)); got != 128 {
		t.Errorf("name has %d runes, want 128", got)
	}
}

func TestWayfarerPoiChangeWebhook(t *testing.T) {
	base := WayfarerPoiData{
		Id: "abc.16", Lat: 1.5, Lon: 2.5,
		Name: null.StringFrom("Fountain"), Url: null.StringFrom("https://a.example/img/1"),
	}

	hook := buildWayfarerPoiChangeWebhook(&WayfarerPoiData{}, &base, true)
	if hook == nil || hook.ChangeType != "new" || hook.Old != nil || hook.New.Id != "abc.16" || hook.EditTypes != nil {
		t.Fatalf("new poi hook = %+v", hook)
	}
	if hook.New.Description != nil || *hook.New.Name != "Fountain" || hook.New.Location != (Location{Latitude: 1.5, Longitude: 2.5}) {
		t.Errorf("new poi payload = %+v", hook.New)
	}

	// Seen again unchanged, or only on another image host: nothing to send.
	same := base
	same.Updated = 99
	same.Url = null.StringFrom("https://b.example/img/1")
	if hook := buildWayfarerPoiChangeWebhook(&base, &same, false); hook != nil {
		t.Errorf("unchanged poi hook = %+v", hook)
	}

	edited := base
	edited.Name = null.StringFrom("Old fountain")
	edited.Description = null.StringFrom("Dry")
	edited.Url = null.StringFrom("https://a.example/img/2")
	edited.Lat = 1.6
	edited.InGame = true
	hook = buildWayfarerPoiChangeWebhook(&base, &edited, false)
	want := []string{"name", "description", "image_url", "location", "in_game"}
	if hook == nil || hook.ChangeType != "edit" || !reflect.DeepEqual(hook.EditTypes, want) {
		t.Fatalf("edited poi hook = %+v, want edit types %v", hook, want)
	}
	if *hook.Old.Name != "Fountain" || *hook.New.Name != "Old fountain" || hook.Old.InGame || !hook.New.InGame {
		t.Errorf("edited poi old = %+v, new = %+v", hook.Old, hook.New)
	}
}

func TestWayfarerPoiScanQuery(t *testing.T) {
	prev := config.Config.Tuning.MaxFortResults
	config.Config.Tuning.MaxFortResults = 100
	t.Cleanup(func() { config.Config.Tuning.MaxFortResults = prev })

	for limit, want := range map[int]int{0: 100, 10: 10, 500: 100, -1: 100} {
		if got := wayfarerPoiScanLimit(limit); got != want {
			t.Errorf("limit %d = %d, want %d", limit, got, want)
		}
	}

	box := ApiWayfarerPoiScan{Min: ApiLatLon{Lat: 1, Lon: 2}, Max: ApiLatLon{Lat: 3, Lon: 4}}
	query, args := wayfarerPoiScanQuery(box, 10)
	if strings.Contains(query, "in_game =") || strings.Contains(query, "updated >") {
		t.Errorf("unfiltered query = %s", query)
	}
	if want := []any{1.0, 3.0, 2.0, 4.0, 11}; !reflect.DeepEqual(args, want) {
		t.Errorf("unfiltered args = %v, want %v", args, want)
	}

	inGame := false
	box.InGame = &inGame
	box.UpdatedAfter = 50
	query, args = wayfarerPoiScanQuery(box, 10)
	if !strings.Contains(query, "AND in_game = ? AND updated > ? ORDER BY id LIMIT ?") {
		t.Errorf("filtered query = %s", query)
	}
	if want := []any{1.0, 3.0, 2.0, 4.0, false, int64(50), 11}; !reflect.DeepEqual(args, want) {
		t.Errorf("filtered args = %v, want %v", args, want)
	}
}

func TestWayfarerPoiApiResult(t *testing.T) {
	r := buildWayfarerPoiResult(&WayfarerPoiData{
		Id: "abc.16", Lat: 1.5, Lon: 2.5, Name: null.StringFrom("Fountain"),
		InGame: true, FirstSeenTimestamp: 10, Updated: 20,
	})
	if r.Id != "abc.16" || *r.Name != "Fountain" || r.Description != nil || r.Url != nil || !r.InGame || r.FirstSeenTimestamp != 10 || r.Updated != 20 {
		t.Errorf("api result = %+v", r)
	}

	got := wayfarerPoiToProto(r)
	if got.GetId() != "abc.16" || got.GetLat() != 1.5 || got.GetLon() != 2.5 || got.GetName() != "Fountain" ||
		got.Description != nil || got.Url != nil || !got.GetInGame() || got.GetFirstSeenTimestamp() != 10 || got.GetUpdated() != 20 {
		t.Errorf("proto = %+v", got)
	}

	inGame := true
	req := wayfarerPoiScanRequestFromProto(&pb.WayfarerPoiScanRequest{
		Min: &pb.LatLon{Lat: 1, Lon: 2}, Max: &pb.LatLon{Lat: 3, Lon: 4}, Limit: 7, InGame: &inGame,
	})
	if req.Min != (ApiLatLon{Lat: 1, Lon: 2}) || req.Max != (ApiLatLon{Lat: 3, Lon: 4}) || req.Limit != 7 || req.UpdatedAfter != 0 || req.InGame == nil || !*req.InGame {
		t.Errorf("scan request = %+v", req)
	}
}
