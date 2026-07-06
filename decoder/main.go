package decoder

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/UnownHash/gohbem"
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

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
var pokestopCache *OtterCache[string, *Pokestop]
var gymCache *OtterCache[string, *Gym]
var stationCache *OtterCache[string, *Station]
var tappableCache *OtterCache[uint64, *Tappable]
var weatherCache *OtterCache[int64, *Weather]
var weatherConsensusCache *OtterCache[int64, *WeatherConsensusState]
var s2CellCache *OtterCache[uint64, *S2Cell]
var spawnpointCache *OtterCache[int64, *Spawnpoint]
var pokemonCache *OtterCache[uint64, *Pokemon]
var incidentCache *OtterCache[string, *Incident]
var playerCache *OtterCache[string, *Player]
var routeCache *OtterCache[string, *Route]
var diskEncounterCache *OtterCache[uint64, *pogo.DiskEncounterOutProto]
var getMapFortsCache *OtterCache[string, *pogo.GetMapFortsOutProto_FortProto]

var ProactiveIVSwitchSem chan bool

var ohbem *gohbem.Ohbem

func init() {
	// initLiveStats is config-independent, so package-init timing is fine.
	// Entity caches are NOT — they must be built after config load via
	// InitDataCache (see below).
	initLiveStats()
}

var initDataCacheOnce sync.Once

// InitDataCache constructs all entity caches and spatial-index plumbing.
// Must be called from main() AFTER config is loaded — cache shard counts,
// fort TTLs, and fort eviction-callback registration all read config.
// (Package init() is too early: it runs before config.ReadConfig().)
func InitDataCache() {
	initDataCacheOnce.Do(initDataCache)
}

func InitProactiveIVSwitchSem() {
	ProactiveIVSwitchSem = make(chan bool, config.Config.Tuning.MaxConcurrentProactiveIVSwitch)
}

type gohbemLogger struct{}

func (cl *gohbemLogger) Print(message string) {
	log.Info("Gohbem - ", message)
}

// fortCacheEntryTTL is the per-entry TTL for pokestop/gym/station cache
// inserts. Jittered so a restart's preload cohort (stamped within minutes)
// doesn't expire as one mass burst of downstream work — tree deletes,
// fort-tracker events, DB reload churn. (With otter there is no
// reader-blocking sweep to defend against; the jitter survives purely as
// burst smoothing.) Touch-on-hit refreshes each entry to its own jittered
// TTL, so actively-seen forts never expire.
func fortCacheEntryTTL() time.Duration {
	if config.Config.FortInMemory {
		return 25*time.Hour + rand.N(2*time.Hour)
	}
	return time.Hour + rand.N(10*time.Minute)
}

func initDataCache() {
	// Sharded caches for high-concurrency tables
	// When fort_in_memory is enabled, extend TTL to 25 hours so that the
	// rtree stays populated between daily quest resets.
	fortCacheTTL := 60 * time.Minute
	if config.Config.FortInMemory {
		fortCacheTTL = 25 * time.Hour
	}

	// Fort caches: touch-on-hit keeps actively-seen forts resident past
	// their (jittered, set-at-save) TTLs; otter touches via the timing
	// wheel, so per-read touch is ~free (no hysteresis workaround needed).
	pokestopCache = NewOtterCache(OtterCacheConfig[string, *Pokestop]{
		Name:       "pokestop",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})

	gymCache = NewOtterCache(OtterCacheConfig[string, *Gym]{
		Name:       "gym",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})

	stationCache = NewOtterCache(OtterCacheConfig[string, *Station]{
		Name:       "station",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})
	// OnEviction registrations for pokestopCache/gymCache/stationCache are
	// registered in initFortRtree() (called below), after fortTreeEvictor
	// and fortLookupCache exist, so they can never fire against a nil
	// evictor/lookup cache.

	tappableCache = NewOtterCache(OtterCacheConfig[uint64, *Tappable]{
		Name:       "tappable",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	weatherCache = NewOtterCache(OtterCacheConfig[int64, *Weather]{
		Name:       "weather",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	weatherConsensusCache = NewOtterCache(OtterCacheConfig[int64, *WeatherConsensusState]{
		Name:       "weather_consensus",
		DefaultTTL: 2 * time.Hour,
		TouchOnHit: true,
	})

	s2CellCache = NewOtterCache(OtterCacheConfig[uint64, *S2Cell]{
		Name:       "s2cell",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	// Spawnpoints are read once per wild sighting; touch-on-hit keeps
	// active spawnpoints resident.
	spawnpointCache = NewOtterCache(OtterCacheConfig[int64, *Spawnpoint]{
		Name:       "spawnpoint",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	// Pokemon TTLs encode despawn times (remainingDuration) and must never
	// extend on read: writing-based expiry only.
	pokemonCache = NewOtterCache(OtterCacheConfig[uint64, *Pokemon]{
		Name:       "pokemon",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: false,
	})
	initPokemonRtree()
	initFortRtree()
	initStationBattleCache()

	incidentCache = NewOtterCache(OtterCacheConfig[string, *Incident]{
		Name:       "incident",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	playerCache = NewOtterCache(OtterCacheConfig[string, *Player]{
		Name:       "player",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	diskEncounterCache = NewOtterCache(OtterCacheConfig[uint64, *pogo.DiskEncounterOutProto]{
		Name:       "disk_encounter",
		DefaultTTL: 10 * time.Minute,
		TouchOnHit: false,
	})

	getMapFortsCache = NewOtterCache(OtterCacheConfig[string, *pogo.GetMapFortsOutProto_FortProto]{
		Name:       "map_forts",
		DefaultTTL: 5 * time.Minute,
		TouchOnHit: false,
	})

	routeCache = NewOtterCache(OtterCacheConfig[string, *Route]{
		Name:       "route",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})
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
		cacheFileLocation := masterFileCachePath
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

		if err := o.LoadPokemonData(cacheFileLocation); err != nil {
			log.Warnf("ohbem.LoadPokemonData from cache failed: %v", err)
			if errFetch := o.FetchPokemonData(); errFetch != nil {
				log.Warnf("ohbem.FetchPokemonData failed: %v", errFetch)
				if errFallback := o.LoadPokemonData("pogo/master-latest-basics.json"); errFallback != nil {
					log.Errorf("ohbem.LoadPokemonData from fallback failed: %v", errFallback)
					return
				}
				log.Warnf("ohbem.LoadPokemonData loaded from pogo/master-latest-basics.json instead.")
			} else if errSave := o.SavePokemonData(cacheFileLocation); errSave != nil {
				log.Warnf("ohbem.SavePokemonData to cache failed: %v", errSave)
			}
		}

		ohbem = o
	}
}

func reloadOhbemFromMasterFile() {
	if ohbem == nil {
		return
	}
	if err := ohbem.LoadPokemonData(masterFileCachePath); err != nil {
		log.Warnf("ohbem reload from MasterFile failed: %v", err)
	} else {
		log.Infof("ohbem reloaded from MasterFile cache")
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

// Ptrable is an interface for any type that has a Ptr() method returning *T
// specifically these are the null objects
type Ptrable[T any] interface {
	Ptr() *T
}

// FormatNull returns "NULL" if the nullable value is not valid, otherwise formats the value
func FormatNull[T any](n Ptrable[T]) string {
	if ptr := n.Ptr(); ptr != nil {
		return fmt.Sprintf("%v", *ptr)
	}
	return "NULL"
}

func SetWebhooksSender(whSender webhooksSenderInterface) {
	webhooksSender = whSender
}

func SetStatsCollector(collector stats_collector.StatsCollector) {
	statsCollector = collector
}

// InitWriteBehindQueue initializes the typed write-behind queues
// Should be called after SetStatsCollector
func InitWriteBehindQueue(ctx context.Context, dbDetails db.DbDetails) {
	// Use the new typed queue system
	InitTypedQueues(ctx, dbDetails, statsCollector)
}

// FlushWriteBehindQueue flushes all pending writes (for shutdown)
func FlushWriteBehindQueue() {
	FlushTypedQueues()
}

// GetUpdateThreshold returns the number of seconds that should be used as a
// debounce/last-seen threshold. Pass the default seconds for normal operation
// If ReduceUpdates is enabled in the loaded config.Config, this returns 43200 (12 hours).
func GetUpdateThreshold(defaultSeconds int64) int64 {
	if config.Config.Tuning.ReduceUpdates {
		return 43200 // 12 hours
	}
	return defaultSeconds
}
