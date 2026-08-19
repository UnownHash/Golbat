package decoder

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
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

	"golbat/ottercache"
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
	FortId    string
	Lat       float64
	Lon       float64
}

type webhooksSenderInterface interface {
	AddMessage(whType webhooks.WebhookType, message any, areas []geo.AreaName)
}

var webhooksSender webhooksSenderInterface

// statsCollector is read on hot decode/save paths and swapped by a handful
// of tests (see setStatsCollectorForTest in init_test.go); atomic.Pointer
// keeps concurrent Store/Load race-free without a mutex.
//
// It is a *atomic.Pointer rather than a plain one so the noop seed can live
// in this variable's own initializer. That is what makes the seeding actually
// ordered: Go initializes package-level variables in dependency order, and
// dependency analysis follows function calls, so any other variable in this
// package whose initializer reaches getStatsCollector is ordered after this
// one. Seeding from init() would not give that — package-level variable
// initializers all run before init() does — which is what the comment here
// used to claim it did.
var statsCollector = newSeededStatsCollector()

func newSeededStatsCollector() *atomic.Pointer[stats_collector.StatsCollector] {
	var p atomic.Pointer[stats_collector.StatsCollector]
	noop := stats_collector.NewNoopStatsCollector()
	p.Store(&noop)
	return &p
}

// getStatsCollector returns the current StatsCollector. Never nil:
// statsCollector is seeded with a noop collector in its own initializer at
// package load, and SetStatsCollector later swaps in the real one once main()
// has read config. Before that seeding was added, callers on paths that could
// run before SetStatsCollector (e.g. the eviction-drop hook InitDataCache
// wires up, which main() calls well before SetStatsCollector — see main.go)
// had to nil-check or risk a nil-interface panic; StartWorkerBacklogReporter's
// ticker did, DroppedEvictionsHook did not. Seeding closes that window for
// both, so nothing here needs to treat nil as a meaningful state anymore.
func getStatsCollector() stats_collector.StatsCollector {
	return *statsCollector.Load()
}

var pokestopCache *ottercache.OtterCache[FortId, *Pokestop]
var gymCache *ottercache.OtterCache[FortId, *Gym]
var stationCache *ottercache.OtterCache[FortId, *Station]
var tappableCache *ottercache.OtterCache[uint64, *Tappable]
var weatherCache *ottercache.OtterCache[int64, *Weather]
var weatherConsensusCache *ottercache.OtterCache[int64, *WeatherConsensusState]
var s2CellCache *ottercache.OtterCache[uint64, *S2Cell]
var spawnpointCache *ottercache.OtterCache[int64, *Spawnpoint]
var pokemonCache *ottercache.OtterCache[uint64, *Pokemon]
var incidentCache *ottercache.OtterCache[string, *Incident]
var playerCache *ottercache.OtterCache[string, *Player]
var routeCache *ottercache.OtterCache[string, *Route]
var getMapFortsCache *ottercache.OtterCache[FortId, *pogo.GetMapFortsOutProto_FortProto]

var ProactiveIVSwitchSem chan bool

var ohbem *gohbem.Ohbem

func init() {
	// The statsCollector noop seed is NOT here — it is in that variable's own
	// initializer, which runs strictly earlier. See its doc comment.

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
		getStatsCollector().AddCacheEvictionsDropped(cacheName, float64(dropped))
	}

	// Fort caches: touch-on-hit keeps actively-seen forts resident past
	// their (jittered, set-at-save) TTLs; otter touches via the timing
	// wheel, so per-read touch is ~free (no hysteresis workaround needed).
	pokestopCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[FortId, *Pokestop]{
		Name:       "pokestop",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})

	gymCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[FortId, *Gym]{
		Name:       "gym",
		DefaultTTL: fortCacheTTL,
		TouchOnHit: true,
	})

	stationCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[FortId, *Station]{
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

	getMapFortsCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[FortId, *pogo.GetMapFortsOutProto_FortProto]{
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

// SetStatsCollector swaps in the real collector once main() has read config.
//
// A nil collector is refused here rather than stored. Storing one would put a
// nil interface behind an atomic.Pointer that every caller dereferences
// without checking (see getStatsCollector), so the failure would surface as a
// nil-interface panic on whichever decode goroutine happened to record a stat
// first — arbitrarily far from the mistake. Panicking in the setter puts it
// at the call site, at boot.
func SetStatsCollector(collector stats_collector.StatsCollector) {
	if collector == nil {
		log.Panic("decoder.SetStatsCollector: nil collector. Pass stats_collector.NewNoopStatsCollector() to disable stats")
	}
	statsCollector.Store(&collector)
	statsCollectorSet.Store(true)
}

// statsCollectorSet records whether SetStatsCollector has run, purely so
// InitWriteBehindQueue below can enforce its ordering requirement.
var statsCollectorSet atomic.Bool

// InitWriteBehindQueue initializes the typed write-behind queues. It must be
// called after SetStatsCollector, and now says so rather than only documenting
// it.
//
// The requirement is real because the collector is passed by value: the queues
// keep whatever InitTypedQueues was handed, so calling this first hands them
// the noop seed permanently and every write-behind metric silently reads zero
// for the life of the process. That used to fail loudly instead — the
// pre-seeding collector was nil, so the first batch flush panicked — and
// trading a panic for silence is a bad trade for a boot-ordering mistake that
// is always a programming error. Check it where the ordering is required.
//
// Note for anyone maintaining a fork with its own main(): this is a new crash
// mode. golbat's main() calls SetStatsCollector before this (see main.go), so
// the panic is unreachable here, but a main() that ordered the two the other
// way round used to boot with silently dead write-behind metrics and now
// fails at startup instead. Swapping the two calls is the fix, not removing
// this check.
func InitWriteBehindQueue(ctx context.Context, dbDetails db.DbDetails) {
	if !statsCollectorSet.Load() {
		log.Panic("decoder.InitWriteBehindQueue called before decoder.SetStatsCollector: the write-behind queues would keep the noop collector for the life of the process")
	}
	// Use the new typed queue system
	InitTypedQueues(ctx, dbDetails, getStatsCollector())
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
