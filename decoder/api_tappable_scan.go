package decoder

import (
	"fmt"
	"slices"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
)

// ApiTappableDnfFilter is one clause of a tappable scan: fields AND'd; an
// omitted/null list is no constraint, an empty list matches nothing.
type ApiTappableDnfFilter struct {
	RewardType      []string `json:"reward_type" required:"false" doc:"reward_type values (LootItemProto field names, e.g. item, pokemon_candy). Unknown names are rejected with 400."`
	ItemId          []int16  `json:"item_id" required:"false" doc:"item_id values (item rewards)"`
	RewardPokemonId []int16  `json:"reward_pokemon_id" required:"false" doc:"reward_pokemon_id values (candy / XL candy / mega energy / temp evo branch species)"`
	PokemonId       []int16  `json:"pokemon_id" required:"false" doc:"pokemon_id values (encounter tappables)"`
}

// ApiTappableScan is the request body for POST /api/tappable/scan.
type ApiTappableScan struct {
	Min          ApiLatLon              `json:"min" doc:"SW (minimum lat/lon) corner of the bounding box."`
	Max          ApiLatLon              `json:"max" doc:"NE (maximum lat/lon) corner of the bounding box."`
	Limit        int                    `json:"limit" required:"false" doc:"Max results to return; 0 uses the server default. Clamped by tuning.max_tappable_results."`
	UpdatedAfter int64                  `json:"updated_after" required:"false" minimum:"0" doc:"Only return tappables whose updated timestamp is strictly newer than this unix time; 0 or omitted returns everything. Applied after the scan and limit, as in the fort scans."`
	DnfFilters   []ApiTappableDnfFilter `json:"filters" required:"false" doc:"OR'd filter clauses; omit, null or empty to match every tappable."`
}

type ApiTappableScanResult struct {
	Tappables    []*ApiTappableResult `json:"tappables" doc:"Matching tappables within the bounding box."`
	Examined     int                  `json:"examined" doc:"Tappables examined during the spatial scan."`
	Skipped      int                  `json:"skipped" doc:"Tappables skipped because they were not found in the lookup cache."`
	Total        int                  `json:"total" doc:"Total tappables in the spatial index at scan time."`
	LimitReached bool                 `json:"limit_reached" doc:"Whether the pre-filtered result list reached the effective result limit."`
}

// tappableDnfCompiled is a clause with reward_type names resolved; has*
// keeps omitted (false) apart from empty (true, matches nothing).
type tappableDnfCompiled struct {
	rewardType, itemId, rewardPokemonId, pokemonId             []int16
	hasRewardType, hasItemId, hasRewardPokemonId, hasPokemonId bool
}

func compileTappableDnf(f ApiTappableDnfFilter) (tappableDnfCompiled, error) {
	c := tappableDnfCompiled{
		itemId: f.ItemId, rewardPokemonId: f.RewardPokemonId, pokemonId: f.PokemonId,
		hasRewardType: f.RewardType != nil, hasItemId: f.ItemId != nil,
		hasRewardPokemonId: f.RewardPokemonId != nil, hasPokemonId: f.PokemonId != nil,
	}
	for _, name := range f.RewardType {
		i, ok := lootRewardTypeIndex(name)
		if !ok {
			return c, fmt.Errorf("unknown reward_type %q", name)
		}
		c.rewardType = append(c.rewardType, int16(i))
	}
	return c, nil
}

func isTappableDnfMatch(l *TappableLookup, f *tappableDnfCompiled) bool {
	return (!f.hasRewardType || slices.Contains(f.rewardType, int16(l.RewardType))) &&
		(!f.hasItemId || slices.Contains(f.itemId, l.ItemId)) &&
		(!f.hasRewardPokemonId || slices.Contains(f.rewardPokemonId, l.RewardPokemonId)) &&
		(!f.hasPokemonId || slices.Contains(f.pokemonId, l.PokemonId))
}

// tappableScanLimit: requested limit clamped by tuning.max_tappable_results (0 = server default).
func tappableScanLimit(limit int) int {
	maxTappables := config.Config.Tuning.MaxTappableResults
	if limit > 0 && limit < maxTappables {
		maxTappables = limit
	}
	return maxTappables
}

func TappableScanEndpoint(p ApiTappableScan) (*ApiTappableScanResult, error) {
	filters := make([]tappableDnfCompiled, 0, len(p.DnfFilters))
	for _, f := range p.DnfFilters {
		c, err := compileTappableDnf(f)
		if err != nil {
			return nil, err
		}
		filters = append(filters, c)
	}

	start := time.Now()
	minLocation, maxLocation := p.Min.Location(), p.Max.Location()
	maxTappables := tappableScanLimit(p.Limit)
	now := time.Now().Unix()
	examined, skipped := 0, 0
	tree := getTappableTreeSnapshot()

	var keys []uint64
	// the snapshot can briefly hold duplicate points for one id
	seen := make(map[uint64]struct{})
	tree.Search([2]float64{minLocation.Longitude, minLocation.Latitude}, [2]float64{maxLocation.Longitude, maxLocation.Latitude},
		func(_, _ [2]float64, id uint64) bool {
			examined++
			l, found := tappableLookupCache.Load(id)
			if !found {
				skipped++
				return true
			}
			if l.ExpireTimestamp != 0 && l.ExpireTimestamp <= now {
				return true
			}
			matched := len(filters) == 0
			for i := range filters {
				if isTappableDnfMatch(&l, &filters[i]) {
					matched = true
					break
				}
			}
			if !matched {
				return true
			}
			if _, dup := seen[id]; dup {
				return true
			}
			seen[id] = struct{}{}
			keys = append(keys, id)
			return len(keys) < maxTappables
		})

	results := make([]*ApiTappableResult, 0, len(keys))
	for _, id := range keys {
		t, unlock, _ := PeekTappableRecord(id, "API.TappableScan")
		if t == nil {
			continue
		}
		if updatedStrictlyAfter(t.Updated, p.UpdatedAfter) {
			r := BuildTappableResult(t)
			results = append(results, &r)
		}
		unlock()
	}

	log.Infof("TappableScan - scan time %s, %d scanned, %d skipped, %d matched, %d returned, tree size %d",
		time.Since(start), examined, skipped, len(keys), len(results), tree.Len())

	return &ApiTappableScanResult{
		Tappables:    results,
		Examined:     examined,
		Skipped:      skipped,
		Total:        tree.Len(),
		LimitReached: len(keys) >= maxTappables,
	}, nil
}
