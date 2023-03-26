package decoder

import (
	"context"
	"github.com/Pupitar/ohbemgo"
	"github.com/jellydator/ttlcache/v3"
	stripedmutex "github.com/nmvalera/striped-mutex"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/db"
	"golbat/pogo"
	"gopkg.in/guregu/null.v4"
	"math"
	"strconv"
	"sync"
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
var s2CellCache *ttlcache.Cache[uint64, S2Cell]
var spawnpointCache *ttlcache.Cache[int64, Spawnpoint]
var pokemonCache *ttlcache.Cache[string, Pokemon]
var incidentCache *ttlcache.Cache[string, Incident]
var playerCache *ttlcache.Cache[string, Player]
var diskEncounterCache *ttlcache.Cache[string, *pogo.DiskEncounterOutProto]
var getMapFortsCache *ttlcache.Cache[string, *pogo.GetMapFortsOutProto_FortProto]

var gymStripedMutex = stripedmutex.New(128)
var pokestopStripedMutex = stripedmutex.New(128)
var pokemonStripedMutex = stripedmutex.New(1024)
var weatherStripedMutex = stripedmutex.New(128)
var s2cellStripedMutex = stripedmutex.New(1024)

var s2CellLookup = sync.Map{}

var ohbem *ohbemgo.Ohbem

func init() {
	initDataCache()
	initLiveStats()
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

	s2CellCache = ttlcache.New[uint64, S2Cell](
		ttlcache.WithTTL[uint64, S2Cell](60 * time.Minute),
	)
	go s2CellCache.Start()

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

const floatTolerance = 0.000001

func floatAlmostEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}

func nullFloatAlmostEqual(a, b null.Float, tolerance float64) bool {
	if a.Valid {
		return b.Valid && math.Abs(a.Float64-b.Float64) < tolerance
	} else {
		return !b.Valid
	}
}

const floatTolerance = 0.000001

func floatAlmostEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) < tolerance
}

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
			pokestopMutex.Unlock()
		}

		if fort.Data.FortType == pogo.FortType_GYM {
			gymMutex, _ := gymStripedMutex.GetLock(fortId)

			gymMutex.Lock()
			gym, err := getGymRecord(ctx, db, fortId)
			if err != nil {
				log.Errorf("getGymRecord: %s", err)
				gymMutex.Unlock()
				continue
			}

			if gym == nil {
				gym = &Gym{}
			}

			gym.updateGymFromFort(fort.Data, fort.Cell)
			saveGymRecord(ctx, db, gym)
			gymMutex.Unlock()
		}
	}
}

func UpdatePokemonBatch(ctx context.Context, db db.DbDetails, wildPokemonList []RawWildPokemonData, nearbyPokemonList []RawNearbyPokemonData, mapPokemonList []RawMapPokemonData, username string) {
	for _, wild := range wildPokemonList {
		encounterId := strconv.FormatUint(wild.Data.EncounterId, 10)
		pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
		pokemonMutex.Lock()

		spawnpointUpdateFromWild(ctx, db, wild.Data, int64(wild.Timestamp))

		if config.Config.Tuning.ProcessWilds {
			pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId)
			if err != nil {
				log.Errorf("getOrCreatePokemonRecord: %s", err)
			} else {
				updateTime := int64(wild.Timestamp / 1000)
				if pokemon.isNewRecord() || pokemon.wildSignificantUpdate(wild.Data, updateTime) {
					go func(wildPokemon *pogo.WildPokemonProto, cellId int64, timestampMs int64) {
						time.Sleep(15 * time.Second)
						pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
						pokemonMutex.Lock()
						defer pokemonMutex.Unlock()

						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()

						if pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId); err != nil {
							log.Errorf("getOrCreatePokemonRecord: %s", err)
						} else {
							// Update if there is still a change required & this update is the most recent
							if pokemon.wildSignificantUpdate(wildPokemon, updateTime) && pokemon.Updated.ValueOrZero() < updateTime {
								log.Debugf("DELAYED UPDATE: Updating pokemon %s from wild", encounterId)

								pokemon.updateFromWild(ctx, db, wildPokemon, cellId, timestampMs, username)
								savePokemonRecordAsAtTime(ctx, db, pokemon, updateTime)
							}
						}
					}(wild.Data, int64(wild.Cell), int64(wild.Timestamp))
				}
			}
		}
		pokemonMutex.Unlock()
	}

	if config.Config.Tuning.ProcessNearby {
		for _, nearby := range nearbyPokemonList {
			encounterId := strconv.FormatUint(nearby.Data.EncounterId, 10)
			pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
			pokemonMutex.Lock()

			pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId)
			if err != nil {
				log.Printf("getOrCreatePokemonRecord: %s", err)
			} else {
				pokemon.updateFromNearby(ctx, db, nearby.Data, int64(nearby.Cell), username)
				savePokemonRecord(ctx, db, pokemon)
			}

			pokemonMutex.Unlock()
		}
	}

	for _, mapPokemon := range mapPokemonList {
		encounterId := strconv.FormatUint(mapPokemon.Data.EncounterId, 10)
		pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
		pokemonMutex.Lock()

		pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId)
		if err != nil {
			log.Printf("getOrCreatePokemonRecord: %s", err)
		} else {
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

func UpdateClientWeatherBatch(ctx context.Context, db db.DbDetails, p []RawClientWeatherData) {
	for _, weatherProto := range p {
		weatherId := strconv.FormatInt(weatherProto.Data.S2CellId, 10)
		weatherMutex, _ := weatherStripedMutex.GetLock(weatherId)
		weatherMutex.Lock()
		weather, err := getWeatherRecord(ctx, db, weatherProto.Cell)
		if err != nil {
			log.Printf("getWeatherRecord: %s", err)
		} else {
			if weather == nil {
				weather = &Weather{}
			}
			weather.updateWeatherFromClientWeatherProto(weatherProto.Data)
			saveWeatherRecord(ctx, db, weather)
		}
		weatherMutex.Unlock()
	}
}

func UpdateClientMapS2CellBatch(ctx context.Context, db db.DbDetails, r []uint64) {
	for _, mapS2CellId := range r {
		s2Cell := &S2Cell{}
		s2Cell.updateS2CellFromClientMapProto(mapS2CellId)
		saveS2CellRecord(ctx, db, s2Cell)
	}
}

func ClearRemovedForts(ctx context.Context, dbDetails db.DbDetails, mapCells []uint64) {
	now := time.Now().Unix()
	// check gyms in cell
	for _, cellId := range mapCells {
		// lookup for last check
		if shouldSkipCellCheck(cellId, now) {
			continue
		}

		// time to check again
		s2cellMutex, _ := s2cellStripedMutex.GetLock(strconv.FormatUint(cellId, 10))
		s2cellMutex.Lock()

		if shouldSkipCellCheck(cellId, now) {
			// if another GMO processed that cell already, then skip
			s2cellMutex.Unlock()
			continue
		}

		var gymsDone = false
		gymIds, errGyms := db.FindOldGyms(ctx, dbDetails, int64(cellId))
		if errGyms != nil {
			log.Errorf("Unable to clear old gyms: %s", errGyms)
		} else {
			if gymIds == nil {
				// if there is no gym to clear we are done with gyms
				gymsDone = true
			} else {
				// we need to clear removed gyms (not seen for 60 minutes)
				errGyms2 := db.ClearOldGyms(ctx, dbDetails, gymIds)
				if errGyms2 != nil {
					log.Errorf("Unable to clear old gyms '%v': %s", gymIds, errGyms2)
				} else {
					// if there are all gyms cleared we are done with gyms
					gymsDone = true
					log.Infof("Cleared old Gym(s) in cell %d: %v", cellId, gymIds)
					CreateFortWebhooks(ctx, dbDetails, gymIds, GYM, REMOVAL)
				}
			}
		}
		var stopsDone = false
		stopIds, stopsErr := db.FindOldPokestops(ctx, dbDetails, int64(cellId))
		if stopsErr != nil {
			log.Errorf("Unable to clear old stops: %s", stopsErr)
		} else {
			if stopIds == nil {
				// iff there is no stop to clear we update stops
				stopsDone = true
			} else {
				// we need to clear removed stops (not seen for 60 minutes)
				stopsErr2 := db.ClearOldPokestops(ctx, dbDetails, stopIds)
				if stopsErr2 != nil {
					log.Errorf("Unable to clear old stops '%v': %s", stopIds, stopsErr2)
				} else {
					// if there are all gyms cleared we are done with gyms
					stopsDone = true
					log.Infof("Cleared old Stop(s) in cell %d: %v", cellId, stopIds)
					CreateFortWebhooks(ctx, dbDetails, stopIds, POKESTOP, REMOVAL)
				}
			}
		}

		if gymsDone && stopsDone {
			s2CellLookup.Store(cellId, now)
		}
		s2cellMutex.Unlock()
	}
}

func shouldSkipCellCheck(cellId uint64, now int64) bool {
	cachedCell, ok := s2CellLookup.Load(cellId)
	var timestamp int64
	if ok {
		timestamp = cachedCell.(int64)
	} else {
		s2CellLookup.Store(cellId, now-2000) // add it with timestamp in the past, because we need to check twice
		return false
	}
	if timestamp > now-1800 {
		return true
	}
	return false
}
