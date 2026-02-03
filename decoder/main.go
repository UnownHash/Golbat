package decoder

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"time"

	"github.com/UnownHash/gohbem"
	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"

	"golbat/config"
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
var pokestopCache *ShardedCache[string, *Pokestop]
var gymCache *ShardedCache[string, *Gym]
var stationCache *ShardedCache[string, *Station]
var tappableCache *ttlcache.Cache[uint64, *Tappable]
var weatherCache *ttlcache.Cache[int64, *Weather]
var weatherConsensusCache *ttlcache.Cache[int64, *WeatherConsensusState]
var s2CellCache *ttlcache.Cache[uint64, *S2Cell]
var spawnpointCache *ShardedCache[int64, *Spawnpoint]
var pokemonCache *ShardedCache[uint64, *Pokemon]
var incidentCache *ttlcache.Cache[string, *Incident]
var playerCache *ttlcache.Cache[string, *Player]
var routeCache *ttlcache.Cache[string, *Route]
var diskEncounterCache *ttlcache.Cache[uint64, *pogo.DiskEncounterOutProto]
var getMapFortsCache *ttlcache.Cache[string, *pogo.GetMapFortsOutProto_FortProto]

var ProactiveIVSwitchSem chan bool

var ohbem *gohbem.Ohbem

func init() {
	initDataCache()
	initLiveStats()
}

func InitProactiveIVSwitchSem() {
	ProactiveIVSwitchSem = make(chan bool, config.Config.Tuning.MaxConcurrentProactiveIVSwitch)
}

type gohbemLogger struct{}

func (cl *gohbemLogger) Print(message string) {
	log.Info("Gohbem - ", message)
}

func initDataCache() {
	// Sharded caches for high-concurrency tables
	pokestopCache = NewShardedCache(ShardedCacheConfig[string, *Pokestop]{
		NumShards:  runtime.NumCPU(),
		TTL:        60 * time.Minute,
		KeyToShard: StringKeyToShard,
	})
	if config.Config.FortInMemory {
		pokestopCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Pokestop]) {
			p := item.Value()
			removeFortFromTree(p.Id, p.Lat, p.Lon)
		})
	}

	gymCache = NewShardedCache(ShardedCacheConfig[string, *Gym]{
		NumShards:  runtime.NumCPU(),
		TTL:        60 * time.Minute,
		KeyToShard: StringKeyToShard,
	})
	if config.Config.FortInMemory {
		gymCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Gym]) {
			g := item.Value()
			removeFortFromTree(g.Id, g.Lat, g.Lon)
		})
	}

	stationCache = NewShardedCache(ShardedCacheConfig[string, *Station]{
		NumShards:  runtime.NumCPU(),
		TTL:        60 * time.Minute,
		KeyToShard: StringKeyToShard,
	})

	tappableCache = ttlcache.New[uint64, *Tappable](
		ttlcache.WithTTL[uint64, *Tappable](60 * time.Minute),
	)
	go tappableCache.Start()

	weatherCache = ttlcache.New[int64, *Weather](
		ttlcache.WithTTL[int64, *Weather](60 * time.Minute),
	)
	go weatherCache.Start()

	weatherConsensusCache = ttlcache.New[int64, *WeatherConsensusState](
		ttlcache.WithTTL[int64, *WeatherConsensusState](2 * time.Hour),
	)
	go weatherConsensusCache.Start()

	s2CellCache = ttlcache.New[uint64, *S2Cell](
		ttlcache.WithTTL[uint64, *S2Cell](60 * time.Minute),
	)
	go s2CellCache.Start()

	spawnpointCache = NewShardedCache(ShardedCacheConfig[int64, *Spawnpoint]{
		NumShards:  runtime.NumCPU(),
		TTL:        60 * time.Minute,
		KeyToShard: Int64KeyToShard,
	})

	// Pokemon cache: sharded for high concurrency
	// By picking NumShards to be nproc, we should expect ~nproc*(1-1/e) ~ 63% concurrency
	pokemonCache = NewShardedCache(ShardedCacheConfig[uint64, *Pokemon]{
		NumShards:         runtime.NumCPU(),
		TTL:               60 * time.Minute,
		KeyToShard:        Uint64KeyToShard,
		DisableTouchOnHit: true, // Pokemon will last 60 mins from when we first see them not last see them
	})
	initPokemonRtree()
	initFortRtree()

	incidentCache = ttlcache.New[string, *Incident](
		ttlcache.WithTTL[string, *Incident](60 * time.Minute),
	)
	go incidentCache.Start()

	playerCache = ttlcache.New[string, *Player](
		ttlcache.WithTTL[string, *Player](60 * time.Minute),
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

	routeCache = ttlcache.New[string, *Route](
		ttlcache.WithTTL[string, *Route](60 * time.Minute),
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

// GetUpdateThreshold returns the number of seconds that should be used as a
// debounce/last-seen threshold. Pass the default seconds for normal operation
// If ReduceUpdates is enabled in the loaded config.Config, this returns 43200 (12 hours).
func GetUpdateThreshold(defaultSeconds int64) int64 {
	if config.Config.Tuning.ReduceUpdates {
		return 43200 // 12 hours
	}
	return defaultSeconds
}
