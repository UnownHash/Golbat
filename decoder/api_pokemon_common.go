package decoder

import (
	"slices"
	"time"

	"golbat/config"
	"golbat/geo"

	log "github.com/sirupsen/logrus"
)

type ApiPokemonDnfId struct {
	Pokemon int16  `json:"id" doc:"Pokedex id to match; 0 (or a negative value) matches any pokemon. Required within a pokemon entry — a form without an id can never match."`
	Form    *int16 `json:"form" required:"false" doc:"Form id to match; null (or a negative value) matches any form of the given id."`
}

// ApiPokemonDnfMinMax is an inclusive integer range used by the filter clauses.
// It is int16 internally (wide enough for CP and PVP ranks); the smaller fields
// like IV simply use the low end of that range.
type ApiPokemonDnfMinMax struct {
	Min int16 `json:"min" required:"false" doc:"Minimum value (inclusive). An omitted bound defaults to 0."`
	Max int16 `json:"max" required:"false" doc:"Maximum value (inclusive). An omitted bound defaults to 0, so a range with only min can never match — send both bounds."`
}

func contains(s []int8, e int8) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

type dnfFilterLookup struct {
	pokemon int16
	form    int16
}

// dnfFilterIndex groups a scan's clauses by the (pokemon, form) keys they
// apply to, so each candidate evaluates only clauses that can match it.
//
// Clauses are OR'd, so a pokemon must see every clause keyed at {id, form},
// {id, -1}, {-1, form} and {-1, -1}. Rather than probing all four keys per
// candidate, buildDnfFilterIndex folds the less specific keys into each
// species bucket up front, so the first bucket found holds every applicable
// clause and the per-candidate cost stays a single first-hit lookup chain.
type dnfFilterIndex[F any] struct {
	// keyed holds every key except {-1, -1}. A species bucket ({id, form} or
	// {id, -1}) is merged with every less specific key it covers. A form-only
	// bucket ({-1, form}) is left as listed: merging it into every {id, -1}
	// bucket would need a bucket per species × form pair, quadratic in the
	// request, so clauses returns it separately instead.
	keyed       map[dnfFilterLookup][]F
	any         []F // clauses without a pokemon list
	hasSpecies  bool
	hasFormOnly bool
}

// clauses returns every clause that can match a pokemon with the given id and
// form as up to two slices: the first bucket found, most to least specific,
// plus whatever that bucket could not have merged. A clause listed under two
// keys can appear in both slices; evaluating it twice is harmless.
func (ix *dnfFilterIndex[F]) clauses(pokemonId, form int16) (bucket, extra []F) {
	if ix.hasSpecies {
		if c, ok := ix.keyed[dnfFilterLookup{pokemon: pokemonId, form: form}]; ok {
			return c, nil
		}
		if c, ok := ix.keyed[dnfFilterLookup{pokemon: pokemonId, form: -1}]; ok {
			if ix.hasFormOnly {
				return c, ix.keyed[dnfFilterLookup{pokemon: -1, form: form}]
			}
			return c, nil
		}
	}
	if ix.hasFormOnly {
		if c, ok := ix.keyed[dnfFilterLookup{pokemon: -1, form: form}]; ok {
			return c, ix.any
		}
	}
	return ix.any, nil
}

// buildDnfFilterIndex keys each clause by its pokemon list (a non-positive
// id = any pokemon, a nil or negative form = any form; an empty list keys it
// at {-1, -1}) and merges
// each species bucket with the less specific keys it covers. Every bucket
// keeps request order and holds a clause once, even when the clause is listed
// under several of the keys a bucket merges, or under the same key twice.
func buildDnfFilterIndex[F any](filters []F, pokemonOf func(*F) []ApiPokemonDnfId) *dnfFilterIndex[F] {
	anyKey := dnfFilterLookup{pokemon: -1, form: -1}

	// Clause positions per key as listed in the request. Merging works on
	// positions so a clause reachable through several keys can be deduplicated.
	listed := make(map[dnfFilterLookup][]int)
	for i := range filters {
		ids := pokemonOf(&filters[i])
		if len(ids) == 0 {
			listed[anyKey] = append(listed[anyKey], i)
			continue
		}
		for _, id := range ids {
			// -1 is the wildcard on both axes. Any other non-positive id or
			// negative form would make a key no candidate can hit, which
			// would still switch on the species or form probes for the
			// whole scan.
			key := dnfFilterLookup{pokemon: id.Pokemon, form: -1}
			if key.pokemon <= 0 {
				key.pokemon = -1
			}
			if id.Form != nil && *id.Form >= 0 {
				key.form = *id.Form
			}
			listed[key] = append(listed[key], i)
		}
	}

	ix := &dnfFilterIndex[F]{keyed: make(map[dnfFilterLookup][]F, len(listed))}
	var merged []int
	for key, positions := range listed {
		merged = append(merged[:0], positions...)
		if key.pokemon != -1 {
			merged = append(merged, listed[anyKey]...)
			if key.form != -1 {
				merged = append(merged, listed[dnfFilterLookup{pokemon: key.pokemon, form: -1}]...)
				merged = append(merged, listed[dnfFilterLookup{pokemon: -1, form: key.form}]...)
			}
		}
		slices.Sort(merged)
		merged = slices.Compact(merged) // a clause may list the same key twice
		bucket := make([]F, len(merged))
		for j, i := range merged {
			bucket[j] = filters[i]
		}
		switch {
		case key == anyKey:
			ix.any = bucket
		case key.pokemon == -1:
			ix.keyed[key] = bucket
			ix.hasFormOnly = true
		default:
			ix.keyed[key] = bucket
			ix.hasSpecies = true
		}
	}
	return ix
}

type PokemonScanRetrieveParameters interface {
	GetMin() geo.Location
	GetMax() geo.Location
	GetLimit() int
}

func pokemonScanLimit(retrieveParameters PokemonScanRetrieveParameters) int {
	maxPokemon := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.GetLimit() > 0 && retrieveParameters.GetLimit() < maxPokemon {
		maxPokemon = retrieveParameters.GetLimit()
	}
	return maxPokemon
}

func pokemonScanLimitReached(retrieveParameters PokemonScanRetrieveParameters, resultCount int) bool {
	return resultCount >= pokemonScanLimit(retrieveParameters)
}

func internalGetPokemonInArea[F any](
	retrieveParameters PokemonScanRetrieveParameters,
	dnfFilters *dnfFilterIndex[F],
	isPokemonDnfMatch func(pokemonLookup *PokemonLookup, pvpLookup *PokemonPvpLookup, filter *F) bool,
) ([]uint64, int, int, int) {
	start := time.Now()

	minLocation := retrieveParameters.GetMin()
	maxLocation := retrieveParameters.GetMax()

	maxPokemon := pokemonScanLimit(retrieveParameters)

	pokemonExamined := 0
	pokemonSkipped := 0

	pokemonTree2 := getPokemonTreeSnapshot()

	lockedTime := time.Since(start)
	totalPokemon := pokemonTree2.Len()

	// A scan that hits the limit collects maxPokemon results; sizing the
	// result slice and the dedup set for that up front avoids regrowing
	// both while candidates stream in. The cap keeps a huge limit from
	// reserving memory a small result never uses.
	resultHint := min(maxPokemon, 4096)
	returnKeys := make([]uint64, 0, resultHint)

	performScan := func() {
		pokemonMatched := 0
		// The shared snapshot can briefly hold duplicate points for one id
		// (eviction delete still queued while a save re-added the point).
		seen := make(map[uint64]struct{}, resultHint)
		// Hoisted outside the closure: its address is passed to the
		// (indirect) matcher call, which would otherwise heap-escape a
		// fresh copy per candidate. One escape per scan, reused for all
		// candidates, keeps the hot line resident.
		var pokemonLookupItem PokemonLookupCacheItem
		pokemonTree2.Search([2]float64{minLocation.Longitude, minLocation.Latitude}, [2]float64{maxLocation.Longitude, maxLocation.Latitude},
			func(min, max [2]float64, pokemonId uint64) bool {
				pokemonExamined++

				var found bool
				pokemonLookupItem, found = pokemonLookupCache.Load(pokemonId)
				if !found {
					pokemonSkipped++
					// Did not find cached result, something amiss?
					return true
				}

				pokemonLookup := &pokemonLookupItem.PokemonLookup
				var pvpLookup *PokemonPvpLookup
				if pokemonLookupItem.HasPvp {
					pvpLookup = &pokemonLookupItem.PokemonPvpLookup
				}

				matched := false

				filters, extra := dnfFilters.clauses(pokemonLookup.PokemonId, pokemonLookup.Form)
				for x := 0; x < len(filters); x++ {
					if isPokemonDnfMatch(pokemonLookup, pvpLookup, &filters[x]) {
						matched = true
						break
					}
				}
				for x := 0; !matched && x < len(extra); x++ {
					matched = isPokemonDnfMatch(pokemonLookup, pvpLookup, &extra[x])
				}

				if matched {
					if _, dup := seen[pokemonId]; dup {
						return true
					}
					seen[pokemonId] = struct{}{}
					returnKeys = append(returnKeys, pokemonId)
					pokemonMatched++
					if pokemonMatched > maxPokemon {
						log.Infof("GetPokemonInArea - result would exceed maximum size (%d), stopping scan", maxPokemon)
						return false
					}
				}

				return true // always continue
			})

	}

	performScan()
	log.Infof("GetPokemonInArea - scan time %s (locked time %s), %d scanned, %d skipped, %d returned", time.Since(start), lockedTime, pokemonExamined, pokemonSkipped, len(returnKeys))

	return returnKeys, pokemonExamined, pokemonSkipped, totalPokemon
}
