package decoder

import (
	"math/rand"
	"testing"
	"time"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/config"
)

// seedDnfBenchPokemon places n random pokemon (400 species, 3 forms, uniform
// IV) in the tree and lookup cache inside a 1°×1° box at (50, 50), clear of
// the coordinates the scan tests use.
func seedDnfBenchPokemon(b *testing.B, n int) {
	b.Helper()
	r := rand.New(rand.NewSource(1))
	ids := make([]uint64, 0, n)
	points := make([][2]float64, 0, n)
	for i := 0; i < n; i++ {
		id := uint64(95_000_000 + i)
		p := &Pokemon{PokemonData: PokemonData{
			Id:        Uint64Str(id),
			Lat:       50 + r.Float64(),
			Lon:       50 + r.Float64(),
			PokemonId: int16(1 + r.Intn(400)),
			Form:      null.ValueFrom(uint16(r.Intn(3))),
			Gender:    null.ValueFrom(uint8(1 + r.Intn(2))),
			Iv:        null.ValueFrom(float32(r.Intn(101))),
		}}
		p.ExpireTimestamp = null.ValueFrom(uint32(time.Now().Unix() + 3600))
		pokemonRtreePreloadInsert(p)
		ids = append(ids, id)
		points = append(points, [2]float64{p.Lon, p.Lat})
	}
	pokemonTreeSnapshot.Store(nil)
	b.Cleanup(func() {
		pokemonTreeMutex.Lock()
		for i, id := range ids {
			pokemonTree.Delete(points[i], points[i], id)
			pokemonLookupCache.Delete(id)
		}
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})
}

// BenchmarkPokemonDnfScan scans 100k candidates with request shapes seen from
// real clients. "matched" is reported so variants can be checked for equal
// results as well as speed.
func BenchmarkPokemonDnfScan(b *testing.B) {
	prevLimit, prevLevel := config.Config.Tuning.MaxPokemonResults, log.GetLevel()
	config.Config.Tuning.MaxPokemonResults = 1 << 30
	log.SetLevel(log.WarnLevel)
	b.Cleanup(func() {
		config.Config.Tuning.MaxPokemonResults = prevLimit
		log.SetLevel(prevLevel)
	})
	seedDnfBenchPokemon(b, 100_000)

	iv := func(lo, hi int16) *ApiPokemonDnfMinMax { return &ApiPokemonDnfMinMax{Min: lo, Max: hi} }
	form0 := int16(0)
	// ReactMap: one clause per custom species/form, plus the global filter
	// listed under each of those keys and {id: -1} for everything else.
	reactMap := func(species int16) []ApiPokemonDnfFilter3 {
		var filters []ApiPokemonDnfFilter3
		var global []ApiPokemonDnfId
		for s := int16(1); s <= species; s++ {
			id := ApiPokemonDnfId{Pokemon: s, Form: &form0}
			filters = append(filters, ApiPokemonDnfFilter3{Pokemon: []ApiPokemonDnfId{id}, Iv: iv(0, 100)})
			global = append(global, id)
		}
		global = append(global, ApiPokemonDnfId{Pokemon: -1})
		return append(filters, ApiPokemonDnfFilter3{Pokemon: global, Iv: iv(90, 100)})
	}

	cases := []struct {
		name    string
		filters []ApiPokemonDnfFilter3
	}{
		{"global-only", []ApiPokemonDnfFilter3{{Iv: iv(90, 100)}}},
		{"reactmap-50-species", reactMap(50)},
		{"reactmap-400-species", reactMap(400)},
		// Diadem: species clauses alongside an independent generic clause.
		{"species-plus-generic", []ApiPokemonDnfFilter3{
			{Pokemon: []ApiPokemonDnfId{{Pokemon: 1}}, Iv: iv(100, 100), Gender: []int8{2}},
			{Pokemon: []ApiPokemonDnfId{{Pokemon: 4}, {Pokemon: 7}}, Iv: iv(0, 0)},
			{Iv: iv(100, 100)},
		}},
		// id 0 + form alongside species clauses: exercises the unmerged
		// form-only clauses returned with {id, -1} hits.
		{"species-plus-form-only", []ApiPokemonDnfFilter3{
			{Pokemon: []ApiPokemonDnfId{{Pokemon: 1}, {Pokemon: 4}, {Pokemon: 7}}, Iv: iv(100, 100)},
			{Pokemon: []ApiPokemonDnfId{{Pokemon: 0, Form: &form0}}, Iv: iv(0, 0)},
			{Iv: iv(100, 100)},
		}},
	}
	for _, tc := range cases {
		params := ApiPokemonScan3{
			Min:        ApiLatLon{Lat: 50, Lon: 50},
			Max:        ApiLatLon{Lat: 51, Lon: 51},
			DnfFilters: tc.filters,
		}
		b.Run(tc.name, func(b *testing.B) {
			var matched int
			for b.Loop() {
				keys, _, _, _ := internalGetPokemonInArea3(params)
				matched = len(keys)
			}
			b.ReportMetric(float64(matched), "matched")
		})
	}
}
