package decoder

import (
	log "github.com/sirupsen/logrus"
	"sync"
)

var nestCount = make(map[string]*nestPokemonCountDetail)
var nestCountLock = sync.Mutex{}

type nestPokemonCountDetail struct {
	count [maxPokemonNo]int
}

func updatePokemonNests(old *Pokemon, new *Pokemon) {
	if (old == nil || old.SeenType.ValueOrZero() != SeenType_Encounter) && new.SeenType.ValueOrZero() == SeenType_Encounter {
		nestAreas := matchGeofences(nestFeatureCollection, new.Lat, new.Lon)

		if len(nestAreas) > 0 {
			nestCountLock.Lock()
			defer nestCountLock.Unlock()
			for i := 0; i < len(nestAreas); i++ {
				area := nestAreas[i]

				countStats := nestCount[area.name]

				if countStats == nil {
					countStats = &nestPokemonCountDetail{}
					nestCount[area.name] = countStats
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
			log.Infof("NESTS: %s: saw pokemon %d %d times (%d %%)", area, maxPokemonId, maxPokemonCount, int(percentage))
		}
	}
}
