package config

import (
	"time"

	"golbat/geo"
)

type configDefinition struct {
	Port                    int            `koanf:"port"`
	GrpcPort                int            `koanf:"grpc_port"`
	Webhooks                []Webhook      `koanf:"webhooks"`
	Database                database       `koanf:"database"`
	Logging                 logging        `koanf:"logging"`
	Sentry                  sentry         `koanf:"sentry"`
	Pyroscope               pyroscope      `koanf:"pyroscope"`
	Prometheus              Prometheus     `koanf:"prometheus"`
	PokemonMemoryOnly       bool           `koanf:"pokemon_memory_only"`
	PokemonInternalToDb     bool           `koanf:"pokemon_internal_to_db"`
	PreserveInMemoryPokemon bool           `koanf:"preserve_pokemon"` // Save/restore pokemon cache on shutdown/startup
	Preload                 bool           `koanf:"preload"`          // Pre-load forts, stations, spawnpoints into cache on startup
	FortInMemory            bool           `koanf:"fort_in_memory"`   // Keep forts in memory with rtree for spatial lookups
	Cleanup                 cleanup        `koanf:"cleanup"`
	RawBearer               string         `koanf:"raw_bearer"`
	ApiSecret               string         `koanf:"api_secret"`
	ApiDocs                 bool           `koanf:"api_docs"` // Serve /docs, /openapi.json and /schemas (no secret required)
	Pvp                     pvp            `koanf:"pvp"`
	Koji                    koji           `koanf:"koji"`
	Tuning                  tuning         `koanf:"tuning"`
	ProtoEngine             protoEngine    `koanf:"proto_engine"`
	Weather                 weather        `koanf:"weather"`
	ScanRules               []scanRule     `koanf:"scan_rules"`
	StatsIntervals          statsIntervals `koanf:"stats_intervals"`
}

func (configDefinition configDefinition) GetWebhookInterval() time.Duration {
	// not currently configurable.
	return time.Second
}

func (configDefinition configDefinition) GetWebhooks() []Webhook {
	return configDefinition.Webhooks
}

func (configDefinition configDefinition) GetPrometheus() Prometheus {
	return configDefinition.Prometheus
}

type koji struct {
	Url         string `koanf:"url"`
	BearerToken string `koanf:"bearer_token"`
}

type cleanup struct {
	Pokemon             bool  `koanf:"pokemon"`
	Quests              bool  `koanf:"quests"`
	Incidents           bool  `koanf:"incidents"`
	StationBattles      bool  `koanf:"station_battles"`
	Tappables           bool  `koanf:"tappables"`
	Stats               bool  `koanf:"stats"`
	StatsDays           int   `koanf:"stats_days"`
	DeviceHours         int   `koanf:"device_hours"`
	FortsStaleThreshold int64 `koanf:"forts_stale_threshold"` // seconds, default 3600 (1 hour)
	FortsMinMissCount   int   `koanf:"forts_min_miss_count"`  // consecutive cell-scan misses before staleness (default 1)
}

type Webhook struct {
	Url              string            `koanf:"url"`
	Types            []string          `koanf:"types"`
	Areas            []string          `koanf:"areas"`
	ExcludeAreas     []string          `koanf:"exclude_areas"`
	Headers          []string          `koanf:"headers"`
	HeaderMap        map[string]string `koanf:"-"`
	AreaNames        []geo.AreaName    `koanf:"-"`
	ExcludeAreaNames []geo.AreaName    `koanf:"-"`
}

type pvp struct {
	Enabled               bool   `koanf:"enabled"`
	IncludeHundosUnderCap bool   `koanf:"include_hundos_under_cap"`
	LevelCaps             []int  `koanf:"level_caps"`
	RankingComparator     string `koanf:"ranking_comparator"`
}

type sentry struct {
	DSN              string  `koanf:"dsn"`
	SampleRate       float64 `koanf:"sample_rate"`
	EnableTracing    bool    `koanf:"enable_tracing"`
	TracesSampleRate float64 `koanf:"traces_sample_rate"`
}

type pyroscope struct {
	ApplicationName      string `koanf:"application_name"`
	ServerAddress        string `koanf:"server_address"`
	BasicAuthUser        string `koanf:"basic_auth_user"`
	BasicAuthPassword    string `koanf:"basic_auth_password"`
	Logger               bool   `koanf:"logger"`
	MutexProfileFraction int    `koanf:"mutex_profile_fraction"`
	BlockProfileRate     int    `koanf:"block_profile_rate"`

	// Deprecated
	ApiKey string `koanf:"api_key"`
}

type Prometheus struct {
	Enabled        bool      `koanf:"enabled"`
	Token          string    `koanf:"token"`
	BucketSize     []float64 `koanf:"bucket_size"`
	LiveStats      bool      `koanf:"live_stats"`
	LiveStatsSleep int       `koanf:"live_stats_sleep"`
}

type logging struct {
	Debug bool `koanf:"debug"`
	// ApiRequestLogging logs the raw request/response bodies of every Huma-served
	// /api endpoint. Independent of Debug because these bodies can be very large;
	// off by default, enable only when debugging a specific caller.
	ApiRequestLogging bool `koanf:"api_request_logging"`
	SaveLogs          bool `koanf:"save_logs"`
	MaxSize           int  `koanf:"max_size"`
	MaxBackups        int  `koanf:"max_backups"`
	MaxAge            int  `koanf:"max_age"`
	Compress          bool `koanf:"compress"`
}

type database struct {
	Addr     string `koanf:"address"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Db       string `koanf:"db"`
	MaxPool  int    `koanf:"max_pool"`
}

type tuning struct {
	ExtendedTimeout                bool    `koanf:"extended_timeout"`
	MaxPokemonResults              int     `koanf:"max_pokemon_results"`
	MaxPokemonDistance             float64 `koanf:"max_pokemon_distance"`
	ProfileRoutes                  bool    `koanf:"profile_routes"`
	ProfileContention              bool    `koanf:"profile_contention"` // Enable mutex/block profiling (has overhead)
	MaxConcurrentProactiveIVSwitch int     `koanf:"max_concurrent_proactive_iv_switch"`
	ReduceUpdates                  bool    `koanf:"reduce_updates"`
	WriteBehindStartupDelay        int     `koanf:"write_behind_startup_delay"`  // seconds, default: 120
	WriteBehindWorkerCount         int     `koanf:"write_behind_worker_count"`   // concurrent writers, default: 50
	WriteBehindBatchSize           int     `koanf:"write_behind_batch_size"`     // entries per batch, default: 50
	WriteBehindBatchTimeoutMs      int     `koanf:"write_behind_batch_timeout"`  // max wait for batch in ms, default: 100
	S2CellLookup                   bool    `koanf:"s2_cell_lookup"`              // Pre-compute S2 cell lookup for faster geofence matching. Trades memory (~60x geofence file size) for ~7x faster lookups, default: false
	RawProcessingConcurrency       int     `koanf:"raw_processing_concurrency"`  // max concurrent raw-proto processing goroutines; 0 = auto (4x CPUs, capped at 96), -1 = unlimited
	RawProcessingQueueFactor       int     `koanf:"raw_processing_queue_factor"` // parked decode queue cap, as a multiple of the concurrency limit; 0 = default (32). Sized to ride out brief DB stalls without shedding.
	SlowDbQueryMs                  int     `koanf:"slow_db_query_ms"`            // log [DB_SLOW] for entity queries and write-behind batch flushes slower than this (ms); 0 = default (1000), -1 = disabled
	GoGCPercent                    int     `koanf:"gogc_percent"`                // runtime GC target percent (Go default 100). Higher = fewer GC cycles, more peak heap: cost ~1/(1+n/100). Large-RAM instances with big live heaps can win 10%+ CPU at 300-400. 0 = leave Go default.
	GoMemLimitMiB                  int     `koanf:"go_mem_limit_mib"`            // runtime soft memory limit (GOMEMLIMIT), MiB. 0 = off.
}

// protoEngine selects the client-proto decode engine per method and the
// shadow-verification sampling rate. "hyperpb" = arena decoding via
// buf.build/go/hyperpb behind pogoshim accessors; "std" = protobuf-go.
//
// Resolution order (see engineFor in protoengine.go): Gmo/Encounter/
// DiskEncounter, if explicitly non-empty, win outright for backward config
// compatibility with pre-Wave-3 deployments; otherwise Overrides[method];
// otherwise Default. Gmo/Encounter/DiskEncounter default to "" (inherit) so
// an existing config.toml with no [proto_engine] section at all keeps
// today's effective behavior (Default's "hyperpb" default) unchanged.
type protoEngine struct {
	Gmo              string            `koanf:"gmo"`
	Encounter        string            `koanf:"encounter"`
	DiskEncounter    string            `koanf:"disk_encounter"`
	Default          string            `koanf:"default"`
	Overrides        map[string]string `koanf:"overrides"`
	ShadowSampleRate float64           `koanf:"shadow_sample_rate"`
	Pgo              bool              `koanf:"pgo"`
}

type scanRule struct {
	Areas                    []string       `koanf:"areas"`
	AreaNames                []geo.AreaName `koanf:"-"`
	ScanContext              []string       `koanf:"context"`
	ProcessPokemon           *bool          `koanf:"pokemon"`
	ProcessWilds             *bool          `koanf:"wild_pokemon"`
	ProcessNearby            *bool          `koanf:"nearby_pokemon"`
	ProcessNearbyCell        *bool          `koanf:"nearby_cell_pokemon"`
	ProcessWeather           *bool          `koanf:"weather"`
	ProcessCells             *bool          `koanf:"cells"`
	ProcessPokestops         *bool          `koanf:"pokestops"`
	ProcessGyms              *bool          `koanf:"gyms"`
	ProcessStations          *bool          `koanf:"stations"`
	ProcessTappables         *bool          `koanf:"tappables"`
	ProactiveIVSwitching     *bool          `koanf:"proactive_iv_switching"`
	ProactiveIVSwitchingToDB *bool          `koanf:"proactive_iv_switching_to_db"`
}

type weather struct {
	ProactiveIVSwitching     bool `koanf:"proactive_iv_switching"`
	ProactiveIVSwitchingToDB bool `koanf:"proactive_iv_switching_to_db"`
}

type statsIntervals struct {
	PokemonStatsIntervalMinutes  int `koanf:"pokemon_stats_interval_minutes"`
	PokemonCountIntervalMinutes  int `koanf:"pokemon_count_interval_minutes"`
	RaidStatsIntervalMinutes     int `koanf:"raid_stats_interval_minutes"`
	InvasionStatsIntervalMinutes int `koanf:"invasion_stats_interval_minutes"`
	QuestStatsIntervalMinutes    int `koanf:"quest_stats_interval_minutes"`
}

var Config configDefinition
