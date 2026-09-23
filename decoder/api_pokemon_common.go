package decoder

import (
	"slices"
	"time"

	"golbat/config"
	"golbat/geo"

	log "github.com/sirupsen/logrus"
)

type ApiPokemonDnfId struct {
	Pokemon int16  `json:"id" doc:"Pokedex id to match; 0 matches any pokemon. Required within a pokemon entry — a form without an id can never match."`
	Form    *int16 `json:"form" required:"false" doc:"Form id to match; null matches any form of the given id."`
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
// {id, -1}, {-1, form} or {-1, -1}. Rather than probing all four keys per
// candidate, buildDnfFilterIndex folds each clause into every more specific
// bucket up front, so the first bucket found holds every applicable clause
// and the per-candidate cost stays a single first-hit lookup chain.
type dnfFilterIndex[F any] struct {
	keyed map[dnfFilterLookup][]F // every key except {-1, -1}, pre-merged
	any   []F                     // clauses without a pokemon list
	// formOnly holds the unmerged {-1, form} clauses. A {id, -1} bucket
	// cannot include them (they depend on the candidate's form), and
	// materialising every species × form bucket would grow quadratically
	// with the request, so they are returned alongside it instead.
	formOnly   map[int16][]F
	hasSpecies bool // keyed holds {id, *} buckets
}

// clauses returns every clause that can match a pokemon with the given id and
// form: the first merged bucket found, most to least specific, plus any
// form-only clauses that bucket could not include. A clause can appear in
// both; evaluating it twice is harmless.
func (ix *dnfFilterIndex[F]) clauses(pokemonId, form int16) (bucket, extra []F) {
	if ix.hasSpecies {
		if c, ok := ix.keyed[dnfFilterLookup{pokemon: pokemonId, form: form}]; ok {
			return c, nil
		}
		if c, ok := ix.keyed[dnfFilterLookup{pokemon: pokemonId, form: -1}]; ok {
			if ix.formOnly == nil {
				return c, nil
			}
			return c, ix.formOnly[form]
		}
	}
	if ix.formOnly != nil {
		if c, ok := ix.keyed[dnfFilterLookup{pokemon: -1, form: form}]; ok {
			return c, nil
		}
	}
	return ix.any, nil
}

// buildDnfFilterIndex keys each clause by its pokemon list (id 0 = any
// pokemon, nil form = any form; an empty list keys it at {-1, -1}) and merges
// every bucket with the buckets it is more specific than. Each merged bucket
// keeps request order and holds a clause once, even when the clause reaches
// it through several keys.
func buildDnfFilterIndex[F any](filters []F, pokemonOf func(*F) []ApiPokemonDnfId) *dnfFilterIndex[F] {
	anyKey := dnfFilterLookup{pokemon: -1, form: -1}

	// Clause indices per raw key; merging works on indices so a clause
	// reachable through several keys can be deduplicated.
	raw := make(map[dnfFilterLookup][]int)
	for i := range filters {
		ids := pokemonOf(&filters[i])
		if len(ids) == 0 {
			raw[anyKey] = append(raw[anyKey], i)
			continue
		}
		for _, id := range ids {
			key := dnfFilterLookup{pokemon: id.Pokemon, form: -1}
			if key.pokemon == 0 {
				key.pokemon = -1
			}
			if id.Form != nil {
				key.form = *id.Form
			}
			raw[key] = append(raw[key], i)
		}
	}

	ix := &dnfFilterIndex[F]{keyed: make(map[dnfFilterLookup][]F, len(raw))}
	collect := func(indices []int) []F {
		bucket := make([]F, len(indices))
		for j, i := range indices {
			bucket[j] = filters[i]
		}
		return bucket
	}

	seen := make([]bool, len(filters))
	var merged []int
	for key, own := range raw {
		merged = merged[:0]
		for _, pokemon := range [...]int16{key.pokemon, -1} {
			for _, form := range [...]int16{key.form, -1} {
				for _, i := range raw[dnfFilterLookup{pokemon: pokemon, form: form}] {
					if !seen[i] {
						seen[i] = true
						merged = append(merged, i)
					}
				}
				if form == -1 {
					break // key.form is -1: visit {pokemon, -1} once
				}
			}
			if pokemon == -1 {
				break // key.pokemon is -1: visit {-1, *} once
			}
		}
		for _, i := range merged {
			seen[i] = false
		}
		slices.Sort(merged)
		bucket := collect(merged)

		switch {
		case key == anyKey:
			ix.any = bucket
		case key.pokemon == -1:
			ix.keyed[key] = bucket
			if ix.formOnly == nil {
				ix.formOnly = make(map[int16][]F)
			}
			ix.formOnly[key.form] = collect(own)
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

	var returnKeys []uint64

	performScan := func() {
		pokemonMatched := 0
		// The shared snapshot can briefly hold duplicate points for one id
		// (eviction delete still queued while a save re-added the point).
		seen := make(map[uint64]struct{})
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
