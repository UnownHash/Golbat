package decoder

import (
	"context"
	"math"
	"sync"

	"golbat/config"

	"github.com/UnownHash/gohbem"
	"github.com/jellydator/ttlcache/v3"
	"github.com/puzpuzpuz/xsync/v3"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
	"gopkg.in/guregu/null.v4"
)

type PokemonLookupCacheItem struct {
	PokemonLookup    *PokemonLookup
	PokemonPvpLookup *PokemonPvpLookup
}

type PokemonLookup struct {
	PokemonId          int16
	Form               int16
	HasEncounterValues bool
	Atk                int8
	Def                int8
	Sta                int8
	Level              int8
	Cp                 int16
	Gender             int8
	Xxs                bool
	Xxl                bool
	Iv                 int8
	Size               int8
}

type PokemonPvpLookup struct {
	Little int16
	Great  int16
	Ultra  int16
}

var pokemonLookupCache *xsync.MapOf[uint64, PokemonLookupCacheItem]
var pokemonTreeMutex sync.RWMutex
var pokemonTree rtree.RTreeG[uint64]

func initPokemonRtree() {
	pokemonLookupCache = xsync.NewMapOf[uint64, PokemonLookupCacheItem]()

	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[uint64, Pokemon]) {
		r := v.Value()
		removePokemonFromTree(&r)
		// Rely on the pokemon pvp lookup caches to remove themselves rather than trying to synchronise
	})

}

func pokemonRtreeUpdatePokemonOnGet(pokemon *Pokemon) {
	pokemonId := pokemon.Id

	_, inMap := pokemonLookupCache.Load(pokemonId)

	if !inMap {
		addPokemonToTree(pokemon)
		// this pokemon won't be available for pvp searches
		updatePokemonLookup(pokemon, false, nil)
	}
}

func valueOrMinus1(n null.Int) int {
	if n.Valid {
		return int(n.Int64)
	}
	return -1
}

func updatePokemonLookup(pokemon *Pokemon, changePvp bool, pvpResults map[string][]gohbem.PokemonEntry) {
	pokemonId := pokemon.Id

	pokemonLookupCacheItem, _ := pokemonLookupCache.Load(pokemonId)

	pokemonLookupCacheItem.PokemonLookup = &PokemonLookup{
		PokemonId:          pokemon.PokemonId,
		HasEncounterValues: pokemon.Move1.Valid,
		Atk:                int8(valueOrMinus1(pokemon.AtkIv)),
		Def:                int8(valueOrMinus1(pokemon.DefIv)),
		Sta:                int8(valueOrMinus1(pokemon.StaIv)),
		Level:              int8(valueOrMinus1(pokemon.Level)),
		Gender:             int8(valueOrMinus1(pokemon.Gender)),
		Cp:                 int16(valueOrMinus1(pokemon.Cp)),
		Iv: func() int8 {
			if pokemon.Iv.Valid {
				return int8(math.Floor(pokemon.Iv.Float64))
			}
			return -1
		}(),
		Size: int8(valueOrMinus1(pokemon.Size)),
	}
	if !pokemon.IsDitto {
		pokemonLookupCacheItem.PokemonLookup.Form = int16(pokemon.Form.ValueOrZero())
	}

	if changePvp {
		pokemonLookupCacheItem.PokemonPvpLookup = calculatePokemonPvpLookup(pokemon, pvpResults)
	}

	pokemonLookupCache.Store(pokemonId, pokemonLookupCacheItem)
}

func calculatePokemonPvpLookup(pokemon *Pokemon, pvpResults map[string][]gohbem.PokemonEntry) *PokemonPvpLookup {
	if pvpResults == nil {
		return nil
	}

	pvpStore := make(map[string]int16)
	for key, value := range pvpResults {
		var best int16 = 4096 // worst possible rank
		// This code actually calculates best in a level cap, which is no longer strictly necessary
		// But will leave in this form to allow easy change to per-cap again later

		for _, levelCap := range config.Config.Pvp.LevelCaps {
			for _, entry := range value {
				// we don't exclude mega evolutions yet
				if (int(entry.Cap) == levelCap || (entry.Capped && int(entry.Cap) <= levelCap)) &&
					entry.Rank < best {
					best = entry.Rank
				}
			}
		}
		if best != 4096 {
			pvpStore[key] = best
		}
	}

	bestValue := func(leagueKey string) int16 {
		if value, ok := pvpStore[leagueKey]; ok {
			return value
		}
		return 4096
	}

	return &PokemonPvpLookup{
		Little: bestValue("little"),
		Great:  bestValue("great"),
		Ultra:  bestValue("ultra"),
	}
}

func addPokemonToTree(pokemon *Pokemon) {
	pokemonId := pokemon.Id

	pokemonTreeMutex.Lock()
	pokemonTree.Insert([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	pokemonTreeMutex.Unlock()
}

func removePokemonFromTree(pokemon *Pokemon) {
	pokemonId := pokemon.Id
	pokemonTreeMutex.Lock()
	beforeLen := pokemonTree.Len()
	pokemonTree.Delete([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	afterLen := pokemonTree.Len()
	pokemonTreeMutex.Unlock()
	pokemonLookupCache.Delete(pokemonId)

	if beforeLen != afterLen+1 {
		log.Infof("PokemonRtree - UNEXPECTED removing %d, lat %f lon %f size %d->%d Map Len %d", pokemonId, pokemon.Lat, pokemon.Lon, beforeLen, afterLen, pokemonLookupCache.Size())
	}
}
