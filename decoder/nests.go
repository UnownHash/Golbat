package decoder

import (
	"github.com/paulmach/orb/encoding/wkt"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"strconv"
	"sync"
)

var nestCount = make(map[string]*nestPokemonCountDetail)
var nestCountLock = sync.Mutex{}

type nestPokemonCountDetail struct {
	count [maxPokemonNo]int
}

func updatePokemonNests(old *Pokemon, new *Pokemon) {
	if nestFeatureCollection == nil {
		return
	}

	if (old == nil || old.SeenType.ValueOrZero() != SeenType_Encounter) && new.SeenType.ValueOrZero() == SeenType_Encounter {
		nestAreas := geo.MatchGeofences(nestFeatureCollection, new.Lat, new.Lon)

		if len(nestAreas) > 0 {
			nestCountLock.Lock()
			defer nestCountLock.Unlock()
			for i := 0; i < len(nestAreas); i++ {
				area := nestAreas[i]

				countStats := nestCount[area.Name]

				if countStats == nil {
					countStats = &nestPokemonCountDetail{}
					nestCount[area.Name] = countStats
				}

				countStats.count[new.PokemonId]++
			}
		}
	}
}

func logNestCount() {
	nestCountLock.Lock()
	defer nestCountLock.Unlock()

	log.Infof("NESTS: Calculating pokemon percentage")

	for area, nestStats := range nestCount {
		total := 0
		maxPokemonId := 0
		maxPokemonCount := 0
		for pokemonId, pokemonSeenCount := range nestStats.count {
			if pokemonSeenCount > maxPokemonCount {
				maxPokemonCount = pokemonSeenCount
				maxPokemonId = pokemonId
			}
			total += pokemonSeenCount
		}

		if total > 0 {
			percentage := float64(maxPokemonCount) / float64(total) * 100
			log.Infof("NESTS: %s: saw pokemon %d %s %d times (%d %%)", area, maxPokemonId, pogo.HoloPokemonId(maxPokemonId), maxPokemonCount, int(percentage))
		}
	}
}

func ReloadNestsAndClearStats(dbDetails db.DbDetails) {
	LoadNests(dbDetails)
	nestCountLock.Lock()
	defer nestCountLock.Unlock()
	nestCount = make(map[string]*nestPokemonCountDetail)
}

func LoadNests(dbDetails db.DbDetails) {
	nests, err := db.LoadNests(dbDetails)
	if err != nil {
		panic(err)
	}

	newFeatureCollection := geojson.NewFeatureCollection()

	for _, nest := range nests {
		geom, err := wkt.Unmarshal(nest.Polygon)
		if err != nil {
			panic(err)
		}
		feat := geojson.NewFeature(geom)
		feat.Properties["name"] = strconv.Itoa(nest.Id)

		newFeatureCollection = newFeatureCollection.Append(feat)
	}

	nestFeatureCollection = newFeatureCollection
}
