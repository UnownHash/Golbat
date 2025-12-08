package config

import (
	"time"

	"golbat/geo"
)

type configDefinition struct {
	Port                           int        `koanf:"port"`
	GrpcPort                       int        `koanf:"grpc_port"`
	Webhooks                       []Webhook  `koanf:"webhooks"`
	Database                       database   `koanf:"database"`
	Logging                        logging    `koanf:"logging"`
	Sentry                         sentry     `koanf:"sentry"`
	Pyroscope                      pyroscope  `koanf:"pyroscope"`
	Prometheus                     Prometheus `koanf:"prometheus"`
	PokemonMemoryOnly              bool       `koanf:"pokemon_memory_only"`
	PokemonInternalToDb            bool       `koanf:"pokemon_internal_to_db"`
	TestFortInMemory               bool       `koanf:"test_fort_in_memory"`
	Cleanup                        cleanup    `koanf:"cleanup"`
	RawBearer                      string     `koanf:"raw_bearer"`
	ApiSecret                      string     `koanf:"api_secret"`
	Pvp                            pvp        `koanf:"pvp"`
	Koji                           koji       `koanf:"koji"`
	Tuning                         tuning     `koanf:"tuning"`
	Weather                        weather    `koanf:"weather"`
	ScanRules                      []scanRule `koanf:"scan_rules"`
	MaxConcurrentProactiveIVSwitch int        `koanf:"max_concurrent_proactive_iv_switch"`
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
	Tappables           bool  `koanf:"tappables"`
	Stats               bool  `koanf:"stats"`
	StatsDays           int   `koanf:"stats_days"`
	DeviceHours         int   `koanf:"device_hours"`
	FortsStaleThreshold int64 `koanf:"forts_stale_threshold"` // seconds, default 3600 (1 hour)
}

type Webhook struct {
	Url       string            `koanf:"url"`
	Types     []string          `koanf:"types"`
	Areas     []string          `koanf:"areas"`
	Headers   []string          `koanf:"headers"`
	HeaderMap map[string]string `koanf:"-"`
	AreaNames []geo.AreaName    `koanf:"-"`
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
	Debug      bool `koanf:"debug"`
	SaveLogs   bool `koanf:"save_logs"`
	MaxSize    int  `koanf:"max_size"`
	MaxBackups int  `koanf:"max_backups"`
	MaxAge     int  `koanf:"max_age"`
	Compress   bool `koanf:"compress"`
}

type database struct {
	Addr     string `koanf:"address"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Db       string `koanf:"db"`
	MaxPool  int    `koanf:"max_pool"`
}

type tuning struct {
	ExtendedTimeout    bool    `koanf:"extended_timeout"`
	MaxPokemonResults  int     `koanf:"max_pokemon_results"`
	MaxPokemonDistance float64 `koanf:"max_pokemon_distance"`
	ProfileRoutes      bool    `koanf:"profile_routes"`
}

type scanRule struct {
	Areas                    []string       `koanf:"areas"`
	AreaNames                []geo.AreaName `koanf:"-"`
	ScanContext              []string       `koanf:"context"`
	ProcessPokemon           *bool          `koanf:"pokemon"`
	ProcessWilds             *bool          `koanf:"wild_pokemon"`
	ProcessNearby            *bool          `koanf:"nearby_pokemon"`
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

var Config configDefinition
