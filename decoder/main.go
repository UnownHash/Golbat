package decoder

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"time"

	"golbat/intstripedmutex"

	"github.com/UnownHash/gohbem"
	"github.com/jellydator/ttlcache/v3"
	stripedmutex "github.com/nmvalera/striped-mutex"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"

	"golbat/config"
	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"golbat/stats_collector"
	"golbat/webhooks"
)

type RawFortData struct {
	Cell      uint64
	Data      *pogo.PokemonFortProto
	Timestamp int64
}

type RawStationData struct {
	Cell uint64
	Data *pogo.StationProto
}

type RawWildPokemonData struct {
	Cell      uint64
	Data      *pogo.WildPokemonProto
	Timestamp int64
}

type RawNearbyPokemonData struct {
	Cell      uint64
	Data      *pogo.NearbyPokemonProto
	Timestamp int64
}

type RawMapPokemonData struct {
	Cell      uint64
	Data      *pogo.MapPokemonProto
	Timestamp int64
}

type webhooksSenderInterface interface {
	AddMessage(whType webhooks.WebhookType, message any, areas []geo.AreaName)
}

var webhooksSender webhooksSenderInterface
var statsCollector stats_collector.StatsCollector
var pokestopCache *ttlcache.Cache[string, Pokestop]
var gymCache *ttlcache.Cache[string, Gym]
var stationCache *ttlcache.Cache[string, Station]
var tappableCache *ttlcache.Cache[uint64, Tappable]
var weatherCache *ttlcache.Cache[int64, Weather]
var weatherConsensusCache *ttlcache.Cache[int64, *WeatherConsensusState]
var s2CellCache *ttlcache.Cache[uint64, S2Cell]
var spawnpointCache *ttlcache.Cache[int64, Spawnpoint]
var pokemonCache []*ttlcache.Cache[uint64, Pokemon]
var incidentCache *ttlcache.Cache[string, Incident]
var playerCache *ttlcache.Cache[string, Player]
var routeCache *ttlcache.Cache[string, Route]
var diskEncounterCache *ttlcache.Cache[uint64, *pogo.DiskEncounterOutProto]
var getMapFortsCache *ttlcache.Cache[string, *pogo.GetMapFortsOutProto_FortProto]

var gymStripedMutex = stripedmutex.New(128)
var pokestopStripedMutex = stripedmutex.New(128)
var stationStripedMutex = stripedmutex.New(128)
var tappableStripedMutex = intstripedmutex.New(563)
var incidentStripedMutex = stripedmutex.New(128)
var pokemonStripedMutex = intstripedmutex.New(1103)
var weatherStripedMutex = intstripedmutex.New(157)
var routeStripedMutex = stripedmutex.New(128)

var ProactiveIVSwitchSem chan bool

var ohbem *gohbem.Ohbem

func init() {
	initDataCache()
	initLiveStats()
}

func InitProactiveIVSwitchSem() {
	ProactiveIVSwitchSem = make(chan bool, config.Config.MaxConcurrentProactiveIVSwitch)
}

type gohbemLogger struct{}

func (cl *gohbemLogger) Print(message string) {
	log.Info("Gohbem - ", message)
}

func getPokemonCache(key uint64) *ttlcache.Cache[uint64, Pokemon] {
	return pokemonCache[key%uint64(len(pokemonCache))]
}

func setPokemonCache(key uint64, value Pokemon, ttl time.Duration) {
	getPokemonCache(key).Set(key, value, ttl)
}

func getPokemonFromCache(key uint64) *ttlcache.Item[uint64, Pokemon] {
	return getPokemonCache(key).Get(key)
}

func deletePokemonFromCache(key uint64) {
	getPokemonCache(key).Delete(key)
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

	stationCache = ttlcache.New[string, Station](
		ttlcache.WithTTL[string, Station](60 * time.Minute),
	)
	go stationCache.Start()

	tappableCache = ttlcache.New[uint64, Tappable](
		ttlcache.WithTTL[uint64, Tappable](60 * time.Minute),
	)
	go tappableCache.Start()

	weatherCache = ttlcache.New[int64, Weather](
		ttlcache.WithTTL[int64, Weather](60 * time.Minute),
	)
	go weatherCache.Start()

	weatherConsensusCache = ttlcache.New[int64, *WeatherConsensusState](
		ttlcache.WithTTL[int64, *WeatherConsensusState](2 * time.Hour),
	)
	go weatherConsensusCache.Start()

	s2CellCache = ttlcache.New[uint64, S2Cell](
		ttlcache.WithTTL[uint64, S2Cell](60 * time.Minute),
	)
	go s2CellCache.Start()

	spawnpointCache = ttlcache.New[int64, Spawnpoint](
		ttlcache.WithTTL[int64, Spawnpoint](60 * time.Minute),
	)
	go spawnpointCache.Start()

	// pokemon is the most active table. Use an array of caches to increase concurrency for querying ttlcache, which places a global lock for each Get/Set operation
	// Initialize pokemon cache array: by picking it to be nproc, we should expect ~nproc*(1-1/e) ~ 63% concurrency
	pokemonCache = make([]*ttlcache.Cache[uint64, Pokemon], runtime.NumCPU())
	for i := 0; i < len(pokemonCache); i++ {
		pokemonCache[i] = ttlcache.New[uint64, Pokemon](
			ttlcache.WithTTL[uint64, Pokemon](60*time.Minute),
			ttlcache.WithDisableTouchOnHit[uint64, Pokemon](), // Pokemon will last 60 mins from when we first see them not last see them
		)
		go pokemonCache[i].Start()
	}
	initPokemonRtree()
	initFortRtree()

	incidentCache = ttlcache.New[string, Incident](
		ttlcache.WithTTL[string, Incident](60 * time.Minute),
	)
	go incidentCache.Start()

	playerCache = ttlcache.New[string, Player](
		ttlcache.WithTTL[string, Player](60 * time.Minute),
	)
	go playerCache.Start()

	diskEncounterCache = ttlcache.New[uint64, *pogo.DiskEncounterOutProto](
		ttlcache.WithTTL[uint64, *pogo.DiskEncounterOutProto](10*time.Minute),
		ttlcache.WithDisableTouchOnHit[uint64, *pogo.DiskEncounterOutProto](),
	)
	go diskEncounterCache.Start()

	getMapFortsCache = ttlcache.New[string, *pogo.GetMapFortsOutProto_FortProto](
		ttlcache.WithTTL[string, *pogo.GetMapFortsOutProto_FortProto](5*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, *pogo.GetMapFortsOutProto_FortProto](),
	)
	go getMapFortsCache.Start()

	routeCache = ttlcache.New[string, Route](
		ttlcache.WithTTL[string, Route](60 * time.Minute),
	)
	go routeCache.Start()
}

func InitialiseOhbem() {
	if config.Config.Pvp.Enabled {
		log.Info("Initialising Ohbem for PVP")
		if len(config.Config.Pvp.LevelCaps) == 0 {
			log.Errorf("PVP level caps not configured")
			return
		}
		leagues := map[string]gohbem.League{
			"little": {
				Cap:            500,
				LittleCupRules: false,
			},
			"great": {
				Cap:            1500,
				LittleCupRules: false,
			},
			"ultra": {
				Cap:            2500,
				LittleCupRules: false,
			},
		}

		gohbemLogger := &gohbemLogger{}
		cacheFileLocation := "cache/master-latest-basics.json"
		o := &gohbem.Ohbem{Leagues: leagues, LevelCaps: config.Config.Pvp.LevelCaps,
			IncludeHundosUnderCap: config.Config.Pvp.IncludeHundosUnderCap,
			MasterFileCachePath:   cacheFileLocation, Logger: gohbemLogger}
		switch config.Config.Pvp.RankingComparator {
		case "prefer_higher_cp":
			o.RankingComparator = gohbem.RankingComparatorPreferHigherCp
		case "prefer_lower_cp":
			o.RankingComparator = gohbem.RankingComparatorPreferLowerCp
		default:
			o.RankingComparator = gohbem.RankingComparatorDefault
		}

		if err := o.FetchPokemonData(); err != nil {
			if err2 := o.LoadPokemonData(cacheFileLocation); err2 != nil {
				_ = o.LoadPokemonData("pogo/master-latest-basics.json")
				log.Errorf("ohbem.FetchPokemonData failed. ohbem.LoadPokemonData from cache failed: %s. Loading from pogo/master-latest-basics.json instead.", err2)
			} else {
				log.Warnf("ohbem.FetchPokemonData failed, loaded from cache: %s", err)
			}
		}

		if o.PokemonData.Initialized == true {
			_ = o.SavePokemonData(cacheFileLocation)
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
	return math.Abs(a-b) < tolerance
}

func nullFloatAlmostEqual(a, b null.Float, tolerance float64) bool {
	if a.Valid {
		return b.Valid && math.Abs(a.Float64-b.Float64) < tolerance
	} else {
		return !b.Valid
	}
}

func UpdateFortBatch(ctx context.Context, db db.DbDetails, scanParameters ScanParameters, p []RawFortData) {
	// Logic is:
	// 1. Filter out pokestops that are unchanged (last modified time)
	// 2. Fetch current stops from database
	// 3. Generate batch of inserts as needed (with on duplicate saveGymRecord)

	//var stopsToModify []string

	for _, fort := range p {
		fortId := fort.Data.FortId
		if fort.Data.FortType == pogo.FortType_CHECKPOINT && scanParameters.ProcessPokestops {
			pokestopMutex, _ := pokestopStripedMutex.GetLock(fortId)

			pokestopMutex.Lock()
			pokestop, err := GetPokestopRecord(ctx, db, fortId) // should check error
			if err != nil {
				log.Errorf("getPokestopRecord: %s", err)
				pokestopMutex.Unlock()
				continue
			}

			isNewPokestop := pokestop == nil
			if isNewPokestop {
				pokestop = &Pokestop{}
			}
			pokestop.updatePokestopFromFort(fort.Data, fort.Cell, fort.Timestamp/1000)

			// If this is a new pokestop, check if it was converted from a gym and copy shared fields
			if isNewPokestop {
				gym, _ := GetGymRecord(ctx, db, fortId)
				if gym != nil {
					pokestop.copySharedFieldsFrom(gym)
				}
			}

			savePokestopRecord(ctx, db, pokestop)

			incidents := fort.Data.PokestopDisplays
			if incidents == nil && fort.Data.PokestopDisplay != nil {
				incidents = []*pogo.PokestopIncidentDisplayProto{fort.Data.PokestopDisplay}
			}

			if incidents != nil {
				for _, incidentProto := range incidents {
					incidentMutex, _ := incidentStripedMutex.GetLock(incidentProto.IncidentId)

					incidentMutex.Lock()
					incident, err := getIncidentRecord(ctx, db, incidentProto.IncidentId)
					if err != nil {
						log.Errorf("getIncident: %s", err)
						incidentMutex.Unlock()
						continue
					}
					if incident == nil {
						incident = &Incident{
							PokestopId: fortId,
						}
					}
					incident.updateFromPokestopIncidentDisplay(incidentProto)
					saveIncidentRecord(ctx, db, incident)

					incidentMutex.Unlock()
				}
			}
			pokestopMutex.Unlock()
		}

		if fort.Data.FortType == pogo.FortType_GYM && scanParameters.ProcessGyms {
			gymMutex, _ := gymStripedMutex.GetLock(fortId)

			gymMutex.Lock()
			gym, err := GetGymRecord(ctx, db, fortId)
			if err != nil {
				log.Errorf("GetGymRecord: %s", err)
				gymMutex.Unlock()
				continue
			}

			isNewGym := gym == nil
			if isNewGym {
				gym = &Gym{}
			}

			gym.updateGymFromFort(fort.Data, fort.Cell)

			// If this is a new gym, check if it was converted from a pokestop and copy shared fields
			if isNewGym {
				pokestop, _ := GetPokestopRecord(ctx, db, fortId)
				if pokestop != nil {
					gym.copySharedFieldsFrom(pokestop)
				}
			}

			saveGymRecord(ctx, db, gym)
			gymMutex.Unlock()
		}
	}
}

func UpdateStationBatch(ctx context.Context, db db.DbDetails, scanParameters ScanParameters, p []RawStationData) {
	for _, stationProto := range p {
		stationId := stationProto.Data.Id
		stationMutex, _ := stationStripedMutex.GetLock(stationId)
		stationMutex.Lock()
		station, err := getStationRecord(ctx, db, stationId)
		if err != nil {
			log.Errorf("getStationRecord: %s", err)
			stationMutex.Unlock()
			continue
		}
		if station == nil {
			station = &Station{}
		}
		station.updateFromStationProto(stationProto.Data, stationProto.Cell)
		saveStationRecord(ctx, db, station)
		stationMutex.Unlock()
	}
}

func UpdatePokemonBatch(ctx context.Context, db db.DbDetails, scanParameters ScanParameters, wildPokemonList []RawWildPokemonData, nearbyPokemonList []RawNearbyPokemonData, mapPokemonList []RawMapPokemonData, weather []*pogo.ClientWeatherProto, username string) {
	weatherLookup := make(map[int64]pogo.GameplayWeatherProto_WeatherCondition)
	for _, weatherProto := range weather {
		weatherLookup[weatherProto.S2CellId] = weatherProto.GameplayWeather.GameplayCondition
	}

	for _, wild := range wildPokemonList {
		encounterId := wild.Data.EncounterId
		pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
		pokemonMutex.Lock()

		spawnpointUpdateFromWild(ctx, db, wild.Data, wild.Timestamp)

		if scanParameters.ProcessWild {
			pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId)
			if err != nil {
				log.Errorf("getOrCreatePokemonRecord: %s", err)
			} else {
				updateTime := wild.Timestamp / 1000
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
								log.Debugf("DELAYED UPDATE: Updating pokemon %d from wild", encounterId)

								pokemon.updateFromWild(ctx, db, wildPokemon, cellId, weatherLookup, timestampMs, username)
								savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, updateTime)
							}
						}
					}(wild.Data, int64(wild.Cell), wild.Timestamp)
				}
			}
		}
		pokemonMutex.Unlock()
	}

	if scanParameters.ProcessNearby {
		for _, nearby := range nearbyPokemonList {
			encounterId := nearby.Data.EncounterId
			pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
			pokemonMutex.Lock()

			pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId)
			if err != nil {
				log.Printf("getOrCreatePokemonRecord: %s", err)
			} else {
				pokemon.updateFromNearby(ctx, db, nearby.Data, int64(nearby.Cell), weatherLookup, nearby.Timestamp, username)
				savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, nearby.Timestamp/1000)
			}

			pokemonMutex.Unlock()
		}
	}

	for _, mapPokemon := range mapPokemonList {
		encounterId := mapPokemon.Data.EncounterId
		pokemonMutex, _ := pokemonStripedMutex.GetLock(encounterId)
		pokemonMutex.Lock()

		pokemon, err := getOrCreatePokemonRecord(ctx, db, encounterId)
		if err != nil {
			log.Printf("getOrCreatePokemonRecord: %s", err)
		} else {
			pokemon.updateFromMap(ctx, db, mapPokemon.Data, int64(mapPokemon.Cell), weatherLookup, mapPokemon.Timestamp, username)
			storedDiskEncounter := diskEncounterCache.Get(encounterId)
			if storedDiskEncounter != nil {
				diskEncounter := storedDiskEncounter.Value()
				diskEncounterCache.Delete(encounterId)
				pokemon.updatePokemonFromDiskEncounterProto(ctx, db, diskEncounter, username)
				//log.Infof("Processed stored disk encounter")
			}
			savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, mapPokemon.Timestamp/1000)
		}
		pokemonMutex.Unlock()
	}
}

func UpdateClientWeatherBatch(ctx context.Context, db db.DbDetails, p []*pogo.ClientWeatherProto, timestampMs int64, account string) (updates []WeatherUpdate) {
	hourKey := timestampMs / time.Hour.Milliseconds()
	for _, weatherProto := range p {
		weatherMutex, _ := weatherStripedMutex.GetLock(uint64(weatherProto.S2CellId))
		weatherMutex.Lock()

		weather, err := getWeatherRecord(ctx, db, weatherProto.S2CellId)
		if err != nil {
			log.Printf("getWeatherRecord: %s", err)
		} else if weather == nil || timestampMs >= weather.UpdatedMs {
			state := getWeatherConsensusState(weatherProto.S2CellId, hourKey)
			if state != nil {
				publish, publishProto := state.applyObservation(hourKey, account, weatherProto)
				if publish {
					if publishProto == nil {
						publishProto = weatherProto
					}
					if weather == nil {
						weather = &Weather{}
					}
					weather.UpdatedMs = timestampMs
					oldWeather := weather.updateWeatherFromClientWeatherProto(publishProto)
					saveWeatherRecord(ctx, db, weather)
					if oldWeather != weather.GameplayCondition {
						updates = append(updates, WeatherUpdate{
							S2CellId:   publishProto.S2CellId,
							NewWeather: int32(publishProto.GetGameplayWeather().GetGameplayCondition()),
						})
					}
				}
			}
		}

		weatherMutex.Unlock()
	}
	return updates
}

func UpdateClientMapS2CellBatch(ctx context.Context, db db.DbDetails, cellIds []uint64) {
	saveS2CellRecords(ctx, db, cellIds)
}

func UpdateIncidentLineup(ctx context.Context, db db.DbDetails, protoReq *pogo.OpenInvasionCombatSessionProto, protoRes *pogo.OpenInvasionCombatSessionOutProto) string {
	incidentMutex, _ := incidentStripedMutex.GetLock(protoReq.IncidentLookup.IncidentId)

	incidentMutex.Lock()
	incident, err := getIncidentRecord(ctx, db, protoReq.IncidentLookup.IncidentId)
	if err != nil {
		incidentMutex.Unlock()
		return fmt.Sprintf("getIncident: %s", err)
	}
	if incident == nil {
		log.Debugf("Updating lineup before it was saved: %s", protoReq.IncidentLookup.IncidentId)
		incident = &Incident{
			Id:         protoReq.IncidentLookup.IncidentId,
			PokestopId: protoReq.IncidentLookup.FortId,
		}
	}
	incident.updateFromOpenInvasionCombatSessionOut(protoRes)

	saveIncidentRecord(ctx, db, incident)
	incidentMutex.Unlock()
	return ""
}

func ConfirmIncident(ctx context.Context, db db.DbDetails, proto *pogo.StartIncidentOutProto) string {
	incidentMutex, _ := incidentStripedMutex.GetLock(proto.Incident.IncidentId)

	incidentMutex.Lock()
	incident, err := getIncidentRecord(ctx, db, proto.Incident.IncidentId)
	if err != nil {
		incidentMutex.Unlock()
		return fmt.Sprintf("getIncident: %s", err)
	}
	if incident == nil {
		log.Debugf("Confirming incident before it was saved: %s", proto.Incident.IncidentId)
		incident = &Incident{
			Id:         proto.Incident.IncidentId,
			PokestopId: proto.Incident.FortId,
		}
	}
	incident.updateFromStartIncidentOut(proto)

	if incident == nil {
		incidentMutex.Unlock()
		// I only saw this once during testing but I couldn't reproduce it so just in case
		return "Unable to process incident"
	}
	saveIncidentRecord(ctx, db, incident)
	incidentMutex.Unlock()
	return ""
}

func SetWebhooksSender(whSender webhooksSenderInterface) {
	webhooksSender = whSender
}

func SetStatsCollector(collector stats_collector.StatsCollector) {
	statsCollector = collector
}
