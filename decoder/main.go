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
	"golbat/pogoshim"
	"golbat/stats_collector"
	"golbat/webhooks"

	"golbat/ottercache"
)

type RawFortData struct {
	Cell      uint64
	Data      pogoshim.PokemonFortProto
	Timestamp int64
}

type RawStationData struct {
	Cell uint64
	Data pogoshim.StationProto
}

type RawWildPokemonData struct {
	Cell      uint64
	Data      pogoshim.WildPokemonProto
	Timestamp int64
}

type RawNearbyPokemonData struct {
	Cell      uint64
	Data      pogoshim.NearbyPokemonProto
	Timestamp int64
}

type RawMapPokemonData struct {
	Cell      uint64
	Data      pogoshim.MapPokemonProto
	Timestamp int64
}

type webhooksSenderInterface interface {
	AddMessage(whType webhooks.WebhookType, message any, areas []geo.AreaName)
}

var webhooksSender webhooksSenderInterface
var statsCollector stats_collector.StatsCollector
var pokestopCache *ottercache.OtterCache[string, *Pokestop]
var gymCache *ottercache.OtterCache[string, *Gym]
var stationCache *ottercache.OtterCache[string, *Station]
var tappableCache *ottercache.OtterCache[uint64, *Tappable]
var weatherCache *ottercache.OtterCache[int64, *Weather]
var weatherConsensusCache *ottercache.OtterCache[int64, *WeatherConsensusState]
var s2CellCache *ottercache.OtterCache[uint64, *S2Cell]
var spawnpointCache *ottercache.OtterCache[int64, *Spawnpoint]
var pokemonCache *ottercache.OtterCache[uint64, *Pokemon]
var incidentCache *ottercache.OtterCache[string, *Incident]
var playerCache *ottercache.OtterCache[string, *Player]
var routeCache *ottercache.OtterCache[string, *Route]
var diskEncounterCache *ottercache.OtterCache[uint64, []byte]

// diskEncounterDecodeFunc decodes a raw DISK_ENCOUNTER payload (cached as
// bytes, not a parsed shim: a hyperpb-backed shim's arena does not outlive
// the decode call that produced it) via the configured proto engine
// (hyperpb or std) and returns the result of process. decoder cannot import
// package main, where the engine selection (decodeWithArena) lives, so this
// is dependency-injected from main the same way SetWebhooksSender/
// SetStatsCollector are — see SetDiskEncounterDecoder below.
type diskEncounterDecodeFunc func(payload []byte, process func(pogoshim.DiskEncounterOutProto) string) (string, error)

var diskEncounterDecoder diskEncounterDecodeFunc

// getMapFortsCache retains the decoded *pogo.GetMapFortsOutProto_FortProto
// itself (not just extracted fields), so GET_MAP_FORTS must stay on the std
// engine (or this cache must be changed to copy out the values it needs)
// before that method is ever flipped to hyperpb -- see the hyperpb migration
// plan before changing proto_engine for this method.
var getMapFortsCache *ottercache.OtterCache[string, *pogo.GetMapFortsOutProto_FortProto]

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

	// Cache eviction-event drops are the one non-self-healing loss; feed
	// them to prometheus alongside the [CACHE_EVICT] log line.
	ottercache.DroppedEvictionsHook = func(cacheName string, dropped int64) {
		statsCollector.AddCacheEvictionsDropped(cacheName, float64(dropped))
	}

	// Fort caches: touch-on-hit keeps actively-seen forts resident past
	// their (jittered, set-at-save) TTLs; otter touches via the timing
	// wheel, so per-read touch is ~free (no hysteresis workaround needed).
	pokestopCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Pokestop]{
		Name:       "pokestop",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})

	gymCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Gym]{
		Name:       "gym",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})

	stationCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Station]{
		Name:       "station",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})
	// OnEviction registrations for pokestopCache/gymCache/stationCache are
	// registered in initFortRtree() (called below), after fortTreeEvictor
	// and fortLookupCache exist, so they can never fire against a nil
	// evictor/lookup cache.

	tappableCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[uint64, *Tappable]{
		Name:       "tappable",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	weatherCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[int64, *Weather]{
		Name:       "weather",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	weatherConsensusCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[int64, *WeatherConsensusState]{
		Name:       "weather_consensus",
		DefaultTTL: 2 * time.Hour,
		TouchOnHit: true,
	})

	s2CellCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[uint64, *S2Cell]{
		Name:       "s2cell",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	// Spawnpoints are read once per wild sighting; touch-on-hit keeps
	// active spawnpoints resident.
	spawnpointCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[int64, *Spawnpoint]{
		Name:       "spawnpoint",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	// Pokemon TTLs encode despawn times (remainingDuration) and must never
	// extend on read: writing-based expiry only.
	pokemonCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[uint64, *Pokemon]{
		Name:       "pokemon",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: false,
	})
	initPokemonRtree()
	initFortRtree()
	initStationBattleCache()

	incidentCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Incident]{
		Name:       "incident",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	playerCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Player]{
		Name:       "player",
		DefaultTTL: 60 * time.Minute,
		TouchOnHit: true,
	})

	diskEncounterCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[uint64, []byte]{
		Name:       "disk_encounter",
		DefaultTTL: 10 * time.Minute,
		TouchOnHit: false,
	})

	getMapFortsCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *pogo.GetMapFortsOutProto_FortProto]{
		Name:       "map_forts",
		DefaultTTL: 5 * time.Minute,
		TouchOnHit: false,
	})

	routeCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Route]{
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

// SetDiskEncounterDecoder wires the proto-engine decode capability needed by
// the GMO path's disk-encounter cache replay (gmo_decode.go). Must be called
// from main() during startup, alongside SetWebhooksSender/SetStatsCollector.
func SetDiskEncounterDecoder(f diskEncounterDecodeFunc) {
	diskEncounterDecoder = f
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
