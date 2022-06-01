package decoder

import (
	"github.com/google/go-cmp/cmp"
	"github.com/jellydator/ttlcache/v3"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"golbat/pogo"
	"math"
	"strconv"
	"time"
)

type RawFortData struct {
	Cell uint64
	Data *pogo.PokemonFortProto
}

type RawWildPokemonData struct {
	Cell      uint64
	Data      *pogo.WildPokemonProto
	Timestamp uint64
}

type RawNearbyPokemonData struct {
	Cell uint64
	Data *pogo.NearbyPokemonProto
}

var pokestopCache *ttlcache.Cache[string, Pokestop]
var gymCache *ttlcache.Cache[string, Gym]
var spawnpointCache *ttlcache.Cache[int64, Spawnpoint]
var pokemonCache *ttlcache.Cache[string, Pokemon]
var incidentCache *ttlcache.Cache[string, Incident]

func init() {
	pokestopCache = ttlcache.New[string, Pokestop](
		ttlcache.WithTTL[string, Pokestop](60 * time.Minute),
	)
	go pokestopCache.Start()

	gymCache = ttlcache.New[string, Gym](
		ttlcache.WithTTL[string, Gym](60 * time.Minute),
	)
	go gymCache.Start()

	spawnpointCache = ttlcache.New[int64, Spawnpoint](
		ttlcache.WithTTL[int64, Spawnpoint](60 * time.Minute),
	)
	go spawnpointCache.Start()

	pokemonCache = ttlcache.New[string, Pokemon](
		ttlcache.WithTTL[string, Pokemon](60 * time.Minute),
	)
	go pokemonCache.Start()

	incidentCache = ttlcache.New[string, Incident](
		ttlcache.WithTTL[string, Incident](60 * time.Minute),
	)
	go incidentCache.Start()

}

var ignoreNearFloats = cmp.Comparer(func(x, y float64) bool {
	delta := math.Abs(x - y)
	return delta < 0.000001
})

func UpdateFortBatch(db *sqlx.DB, p []RawFortData) {
	// Logic is:
	// 1. Filter out pokestops that are unchanged (last modified time)
	// 2. Fetch current stops from database
	// 3. Generate batch of inserts as needed (with on duplicate saveGymRecord)

	//var stopsToModify []string

	for _, fort := range p {
		fortId := fort.Data.FortId
		if fort.Data.FortType == pogo.FortType_CHECKPOINT {

			pokestop, err := getPokestopRecord(db, fortId) // should check error
			if err != nil {
				log.Errorf("getPokestopRecord: %s", err)
				continue
			}

			if pokestop == nil {
				pokestop = &Pokestop{}
			}
			pokestop.updatePokestopFromFort(fort.Data, fort.Cell)
			savePokestopRecord(db, pokestop)

			incidents := fort.Data.PokestopDisplays
			if incidents == nil && fort.Data.PokestopDisplay != nil {
				incidents = []*pogo.PokestopIncidentDisplayProto{fort.Data.PokestopDisplay}
			}

			if incidents != nil {
				for _, incidentProto := range incidents {
					incident, err := getIncidentRecord(db, incidentProto.IncidentId)
					if err != nil {
						log.Errorf("getIncident: %s", err)
						continue
					}
					if incident == nil {
						incident = &Incident{
							PokestopId: fortId,
						}
					}
					incident.updateFromPokestopIncidentDisplay(incidentProto)
					saveIncidentRecord(db, incident)
				}
			}
		}

		if fort.Data.FortType == pogo.FortType_GYM {
			gym, err := getGymRecord(db, fortId)
			if err != nil {
				log.Errorf("getGymRecord: %s", err)
				continue
			}

			if gym == nil {
				gym = &Gym{}
			}

			gym.updateGymFromFort(fort.Data, fort.Cell)
			saveGymRecord(db, gym)
		}
	}
}

func UpdatePokemonBatch(db *sqlx.DB, wildPokemonList []RawWildPokemonData, nearbyPokemonList []RawNearbyPokemonData) {
	for _, wild := range wildPokemonList {
		pokemon, err := getPokemonRecord(db, strconv.FormatUint(wild.Data.EncounterId, 10))
		if err != nil {
			log.Printf("getPokemonRecord: %s", err)
			continue
		}

		if pokemon == nil {
			pokemon = &Pokemon{}
		}

		pokemon.updateFromWild(db, wild.Data, int64(wild.Cell), int64(wild.Timestamp), "Account")
		savePokemonRecord(db, pokemon)
	}

	for _, nearby := range nearbyPokemonList {
		pokemon, err := getPokemonRecord(db, strconv.FormatUint(nearby.Data.EncounterId, 10))
		if err != nil {
			log.Printf("getPokemonRecord: %s", err)
			continue
		}

		if pokemon == nil {
			pokemon = &Pokemon{}
		}

		pokemon.updateFromNearby(db, nearby.Data, int64(nearby.Cell), "Account")
		savePokemonRecord(db, pokemon)

	}
}
