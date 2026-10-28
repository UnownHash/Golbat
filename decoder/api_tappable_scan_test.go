package decoder

import (
	"strings"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/config"
)

func setMaxTappableResults(t *testing.T, n int) {
	t.Helper()
	orig := config.Config.Tuning.MaxTappableResults
	config.Config.Tuning.MaxTappableResults = n
	t.Cleanup(func() { config.Config.Tuning.MaxTappableResults = orig })
}

// seedTappable caches and indexes a tappable; removed on cleanup.
func seedTappable(t *testing.T, id uint64, lat, lon float64, mutate func(*TappableData)) {
	t.Helper()
	ta := &Tappable{TappableData: TappableData{
		Id: id, Lat: lat, Lon: lon, Updated: 1,
		ExpireTimestamp: null.IntFrom(time.Now().Unix() + 600),
	}}
	if mutate != nil {
		mutate(&ta.TappableData)
	}
	tappableCache.Set(id, ta, time.Minute)
	tappablePreloadInsert(ta)
	tappableTreeSnapshot.Store(nil)
	t.Cleanup(func() { tappableCache.Delete(id) })
}

func rareCandy(d *TappableData) {
	d.RewardType, d.ItemId, d.Count = null.StringFrom("item"), null.IntFrom(1301), null.IntFrom(1)
}

func pikachuCandy(d *TappableData) {
	d.RewardType, d.RewardPokemonId, d.Count = null.StringFrom("pokemon_candy"), null.IntFrom(25), null.IntFrom(6)
}

func box(lat, lon float64) (ApiLatLon, ApiLatLon) {
	return ApiLatLon{Lat: lat, Lon: lon}, ApiLatLon{Lat: lat + 0.01, Lon: lon + 0.01}
}

func scanIds(t *testing.T, p ApiTappableScan) (*ApiTappableScanResult, map[uint64]bool) {
	t.Helper()
	res, err := TappableScanEndpoint(p)
	if err != nil {
		t.Fatalf("TappableScanEndpoint: %v", err)
	}
	ids := map[uint64]bool{}
	for _, r := range res.Tappables {
		ids[r.Id] = true
	}
	return res, ids
}

func TestTappableDnfMatch(t *testing.T) {
	item := TappableLookup{RewardType: 0, ItemId: 1301, RewardPokemonId: -1, PokemonId: -1}
	candy := TappableLookup{RewardType: 3, ItemId: -1, RewardPokemonId: 25, PokemonId: -1}
	encounter := TappableLookup{RewardType: -1, ItemId: -1, RewardPokemonId: -1, PokemonId: 25}
	for name, tc := range map[string]struct {
		filter ApiTappableDnfFilter
		lookup TappableLookup
		want   bool
	}{
		"no fields":                           {ApiTappableDnfFilter{}, item, true},
		"reward_type item vs item":            {ApiTappableDnfFilter{RewardType: []string{"item"}}, item, true},
		"reward_type item vs candy":           {ApiTappableDnfFilter{RewardType: []string{"item"}}, candy, false},
		"item_id match":                       {ApiTappableDnfFilter{ItemId: []int16{1301}}, item, true},
		"item_id mismatch":                    {ApiTappableDnfFilter{ItemId: []int16{1}}, item, false},
		"empty item_id list":                  {ApiTappableDnfFilter{ItemId: []int16{}}, item, false},
		"reward_pokemon_id match":             {ApiTappableDnfFilter{RewardPokemonId: []int16{25}}, candy, true},
		"encounter clause vs reward tappable": {ApiTappableDnfFilter{PokemonId: []int16{25}}, candy, false},
		"reward clause vs encounter tappable": {ApiTappableDnfFilter{RewardType: []string{"pokemon_candy"}}, encounter, false},
		"AND with one failing":                {ApiTappableDnfFilter{RewardType: []string{"item"}, ItemId: []int16{1}}, item, false},
	} {
		t.Run(name, func(t *testing.T) {
			f, err := compileTappableDnf(tc.filter)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if got := isTappableDnfMatch(&tc.lookup, &f); got != tc.want {
				t.Errorf("match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTappableScanBox(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 930001, 30.001, 40.001, rareCandy)
	seedTappable(t, 930002, 30.002, 40.002, rareCandy)
	seedTappable(t, 930003, 31.0, 41.0, rareCandy)
	min, max := box(30, 40)
	res, ids := scanIds(t, ApiTappableScan{Min: min, Max: max})
	if len(ids) != 2 || !ids[930001] || !ids[930002] {
		t.Errorf("ids = %v, want 930001 and 930002", ids)
	}
	if res.Examined != 2 || res.Total < 3 {
		t.Errorf("examined %d total %d, want 2 and >= 3", res.Examined, res.Total)
	}
}

func TestTappableScanOrClauses(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 930011, 32.001, 42.001, rareCandy)
	seedTappable(t, 930012, 32.002, 42.002, pikachuCandy)
	seedTappable(t, 930013, 32.003, 42.003, func(d *TappableData) { d.RewardType = null.StringFrom("stardust") })
	min, max := box(32, 42)
	_, ids := scanIds(t, ApiTappableScan{Min: min, Max: max, DnfFilters: []ApiTappableDnfFilter{
		{RewardType: []string{"item"}, ItemId: []int16{1301}},
		{RewardPokemonId: []int16{25}},
	}})
	if len(ids) != 2 || !ids[930011] || !ids[930012] {
		t.Errorf("ids = %v, want 930011 and 930012", ids)
	}
}

func TestTappableScanExpiryGate(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 930021, 33.001, 43.001, func(d *TappableData) { d.ExpireTimestamp = null.IntFrom(time.Now().Unix() - 1) })
	seedTappable(t, 930022, 33.002, 43.002, func(d *TappableData) { d.ExpireTimestamp = null.Int{} })
	min, max := box(33, 43)
	_, ids := scanIds(t, ApiTappableScan{Min: min, Max: max})
	if len(ids) != 1 || !ids[930022] {
		t.Errorf("ids = %v, want only 930022", ids)
	}
}

func TestTappableScanLimit(t *testing.T) {
	setMaxTappableResults(t, 100)
	for i := uint64(0); i < 5; i++ {
		seedTappable(t, 930031+i, 34.001+float64(i)*0.001, 44.001, rareCandy)
	}
	min, max := box(34, 44)
	res, ids := scanIds(t, ApiTappableScan{Min: min, Max: max, Limit: 2})
	if len(ids) != 2 || !res.LimitReached {
		t.Errorf("limit 2: %d results, limit_reached %v; want 2, true", len(ids), res.LimitReached)
	}
	res, ids = scanIds(t, ApiTappableScan{Min: min, Max: max, Limit: -1})
	if len(ids) != 5 || res.LimitReached {
		t.Errorf("limit -1: %d results, limit_reached %v; want 5, false", len(ids), res.LimitReached)
	}
}

func TestTappableScanUpdatedAfter(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 930041, 35.001, 45.001, func(d *TappableData) { d.Updated = 100 })
	seedTappable(t, 930042, 35.002, 45.002, func(d *TappableData) { d.Updated = 200 })
	min, max := box(35, 45)
	res, ids := scanIds(t, ApiTappableScan{Min: min, Max: max, UpdatedAfter: 150})
	if len(ids) != 1 || !ids[930042] || res.Examined != 2 {
		t.Errorf("ids %v examined %d, want only 930042 and 2", ids, res.Examined)
	}
}

func TestTappableScanUnknownRewardType(t *testing.T) {
	for _, name := range []string{"Item", "candy"} {
		_, err := TappableScanEndpoint(ApiTappableScan{DnfFilters: []ApiTappableDnfFilter{{RewardType: []string{name}}}})
		if err == nil || !strings.Contains(err.Error(), `unknown reward_type "`+name+`"`) {
			t.Errorf("reward_type %q: err = %v, want unknown reward_type", name, err)
		}
	}
}

func TestTappableScanInvertedBox(t *testing.T) {
	setMaxTappableResults(t, 100)
	seedTappable(t, 930051, 36.001, 46.001, rareCandy)
	min, max := box(36, 46)
	res, err := TappableScanEndpoint(ApiTappableScan{Min: max, Max: min})
	if err != nil || len(res.Tappables) != 0 {
		t.Errorf("inverted box: %d results, err %v; want 0, nil", len(res.Tappables), err)
	}
}
