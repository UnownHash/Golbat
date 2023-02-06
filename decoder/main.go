package decoder

import (
	"context"
	"github.com/Pupitar/ohbemgo"
	"github.com/google/go-cmp/cmp"
	"github.com/jellydator/ttlcache/v3"
	stripedmutex "github.com/nmvalera/striped-mutex"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/db"
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

type RawMapPokemonData struct {
	Cell uint64
	Data *pogo.MapPokemonProto
}

type RawClientWeatherData struct {
	Cell int64
	Data *pogo.ClientWeatherProto
}

var pokestopCache *ttlcache.Cache[string, Pokestop]
var gymCache *ttlcache.Cache[string, Gym]
var weatherCache *ttlcache.Cache[int64, Weather]
var spawnpointCache *ttlcache.Cache[int64, Spawnpoint]
var pokemonCache *ttlcache.Cache[string, Pokemon]
var incidentCache *ttlcache.Cache[string, Incident]
var playerCache *ttlcache.Cache[string, Player]
var diskEncounterCache *ttlcache.Cache[string, *pogo.DiskEncounterOutProto]
var getMapFortsCache *ttlcache.Cache[string, *pogo.GetMapFortsOutProto_FortProto]

var gymStripedMutex = stripedmutex.New(32)
var pokestopStripedMutex = stripedmutex.New(32)
var pokemonStripedMutex = stripedmutex.New(128)
var weatherStripedMutex = stripedmutex.New(8)

var ohbem *ohbemgo.Ohbem

func init() {
	initDataCache()
	initLiveStats()
	initNests()
}

func initDataCache() {
	pokestopCache = ttlcache.New[string, Pokestop](
		ttlcache.WithTTL[string, Pokestop](60 * time.Minute),
	)
	go pokestopCache.Start()

	gymCache = ttlcache.New[string, Gym](
		ttlcache.WithTTL[string, Gym](60 * time.Minute),
	)
	go gymCache.Start()

	weatherCache = ttlcache.New[int64, Weather](
		ttlcache.WithTTL[int64, Weather](60 * time.Minute),
	)
	go weatherCache.Start()

	spawnpointCache = ttlcache.New[int64, Spawnpoint](
		ttlcache.WithTTL[int64, Spawnpoint](60 * time.Minute),
	)
	go spawnpointCache.Start()

	pokemonCache = ttlcache.New[string, Pokemon](
		ttlcache.WithTTL[string, Pokemon](60*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, Pokemon](), // Pokemon will last 60 mins from when we first see them not last see them
	)
	go pokemonCache.Start()

	incidentCache = ttlcache.New[string, Incident](
		ttlcache.WithTTL[string, Incident](60 * time.Minute),
	)
	go incidentCache.Start()

	playerCache = ttlcache.New[string, Player](
		ttlcache.WithTTL[string, Player](60 * time.Minute),
	)
	go playerCache.Start()

	diskEncounterCache = ttlcache.New[string, *pogo.DiskEncounterOutProto](
		ttlcache.WithTTL[string, *pogo.DiskEncounterOutProto](10*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, *pogo.DiskEncounterOutProto](),
	)
	go diskEncounterCache.Start()

	getMapFortsCache = ttlcache.New[string, *pogo.GetMapFortsOutProto_FortProto](
		ttlcache.WithTTL[string, *pogo.GetMapFortsOutProto_FortProto](5*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, *pogo.GetMapFortsOutProto_FortProto](),
	)
	go getMapFortsCache.Start()
}

func InitialiseOhbem() {
	if config.Config.Pvp.Enabled {
		log.Info("Initialising Ohbem for PVP")
		if len(config.Config.Pvp.Leagues) == 0 {
			log.Errorf("PVP leagues not configured")
			return
		}
		if len(config.Config.Pvp.LevelCaps) == 0 {
			log.Errorf("PVP level caps not configured")
			return
		}
		leagues := make(map[string]ohbemgo.League)

		for _, league := range config.Config.Pvp.Leagues {
			leagues[league.Name] = ohbemgo.League{
				Cap:            league.Cap,
				LittleCupRules: league.LittleCupRules,
			}
		}

		o := &ohbemgo.Ohbem{Leagues: leagues, LevelCaps: config.Config.Pvp.LevelCaps,
			IncludeHundosUnderCap: config.Config.Pvp.IncludeHundosUnderCap}

		if err := o.FetchPokemonData(); err != nil {
			log.Errorf("ohbem.FetchPokemonData: %s", err)
			return
		}

		_ = o.WatchPokemonData()

		ohbem = o
	}
}

func ClearPokestopCache() {
	pokestopCache.DeleteAll()
}

func ClearGymCache() {
	gymCache.DeleteAll()
}

var ignoreNearFloats = cmp.Comparer(func(x, y float64) bool {
	delta := math.Abs(x - y)
	return delta < 0.000001
})

func UpdateFortBatch(ctx context.Context, db db.DbDetails, p []RawFortData) {
	// Logic is:
	// 1. Filter out pokestops that are unchanged (last modified time)
	// 2. Fetch current stops from database
	// 3. Generate batch of inserts as needed (with on duplicate saveGymRecord)

	//var stopsToModify []string

	for _, fort := range p {
		fortId := fort.Data.FortId
		if fort.Data.FortType == pogo.FortType_CHECKPOINT {

			pokestopMutex, _ := pokestopStripedMutex.GetLock(fortId)

			pokestopMutex.Lock()
			pokestop, err := getPokestopRecord(ctx, db, fortId) // should check error
			if err != nil {
				log.Errorf("getPokestopRecord: %s", err)
				pokestopMutex.Unlock()
				continue
			}

			if pokestop == nil {
				pokestop = &Pokestop{}
			}
			pokestop.updatePokestopFromFort(fort.Data, fort.Cell)
			savePokestopRecord(ctx, db, pokestop)
			pokestopMutex.Unlock()

			incidents := fort.Data.PokestopDisplays
			if incidents == nil && fort.Data.PokestopDisplay != nil {
				incidents = []*pogo.PokestopIncidentDisplayProto{fort.Data.PokestopDisplay}
			}

			if incidents != nil {
				for _, incidentProto := range incidents {
					incident, err := getIncidentRecord(ctx, db, incidentProto.IncidentId)
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
					saveIncidentRecord(ctx, db, incident)
				}
			}
		}

		if fort.Data.FortType == pogo.FortType_GYM {

			gymMutex, _ := gymStripedMutex.GetLock(fortId)

			gymMutex.Lock()
			gym, err := getGymRecord(db, fortId)
			if err != nil {
				log.Errorf("getGymRecord: %s", err)
				gymMutex.Unlock()
				continue
			}

			if gym == nil {
				gym = &Gym{}
			}

			gym.updateGymFromFort(fort.Data, fort.Cell)
			saveGymRecord(db, gym)
			gymMutex.Unlock()
		}
	}
}

func UpdatePokemonBatch(ctx context.Context, db db.DbDetails, wildPokemonList []RawWildPokemonData, nearbyPokemonList []RawNearbyPokemonData, mapPokemonList []RawMapPokemonData, username string) {
	for _, wild := range wildPokemonList {
		encounterId := strconv.FormatUint(wild.Data.EncounterId, 10)
		pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
		pokemonMutex.Lock()

		pokemon, err := getPokemonRecord(ctx, db, encounterId)
		if err != nil {
			log.Printf("getPokemonRecord: %s", err)
		} else {
			if pokemon == nil {
				pokemon = &Pokemon{}
			}

			pokemon.updateFromWild(ctx, db, wild.Data, int64(wild.Cell), int64(wild.Timestamp), username)
			savePokemonRecord(ctx, db, pokemon)
		}

		pokemonMutex.Unlock()
	}

	for _, nearby := range nearbyPokemonList {
		encounterId := strconv.FormatUint(nearby.Data.EncounterId, 10)
		pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
		pokemonMutex.Lock()

		pokemon, err := getPokemonRecord(ctx, db, encounterId)
		if err != nil {
			log.Printf("getPokemonRecord: %s", err)
		} else {
			if pokemon == nil {
				pokemon = &Pokemon{}
			}

			pokemon.updateFromNearby(ctx, db, nearby.Data, int64(nearby.Cell), username)
			savePokemonRecord(ctx, db, pokemon)
		}
		pokemonMutex.Unlock()
	}

	for _, mapPokemon := range mapPokemonList {
		encounterId := strconv.FormatUint(mapPokemon.Data.EncounterId, 10)
		pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
		pokemonMutex.Lock()

		pokemon, err := getPokemonRecord(ctx, db, encounterId)
		if err != nil {
			log.Printf("getPokemonRecord: %s", err)
		} else {
			if pokemon == nil {
				pokemon = &Pokemon{}
			}

			pokemon.updateFromMap(ctx, db, mapPokemon.Data, int64(mapPokemon.Cell), username)

			storedDiskEncounter := diskEncounterCache.Get(encounterId)
			if storedDiskEncounter != nil {
				diskEncounter := storedDiskEncounter.Value()
				diskEncounterCache.Delete(encounterId)
				pokemon.updatePokemonFromDiskEncounterProto(ctx, db, diskEncounter)
				log.Infof("Processed stored disk encounter")
			}
			savePokemonRecord(ctx, db, pokemon)
		}
		pokemonMutex.Unlock()
	}
}

func UpdateClientWeatherBatch(db db.DbDetails, p []RawClientWeatherData) {
	for _, weatherProto := range p {
		weatherId := strconv.FormatInt(weatherProto.Data.S2CellId, 10)
		weatherMutex, _ := weatherStripedMutex.GetLock(weatherId)
		weatherMutex.Lock()
		weather, err := getWeatherRecord(db, weatherProto.Cell)
		if err != nil {
			log.Printf("getWeatherRecord: %s", err)
		} else {
			if weather == nil {
				weather = &Weather{}
			}
			weather.updateWeatherFromClientWeatherProto(weatherProto.Data)
			saveWeatherRecord(db, weather)
		}
		weatherMutex.Unlock()
	}
}
