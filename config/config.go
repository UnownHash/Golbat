package config

import "golbat/geo"

type configDefinition struct {
	Port      int        `koanf:"port"`
	Webhooks  []webhook  `koanf:"webhooks"`
	Database  database   `koanf:"database"`
	Stats     bool       `koanf:"stats"`
	Logging   logging    `koanf:"logging"`
	Sentry    sentry     `koanf:"sentry"`
	Pyroscope pyroscope  `koanf:"pyroscope"`
	InMemory  bool       `koanf:"in_memory"`
	Cleanup   cleanup    `koanf:"cleanup"`
	RawBearer string     `koanf:"raw_bearer"`
	ApiSecret string     `koanf:"api_secret"`
	Pvp       pvp        `koanf:"pvp"`
	Koji      koji       `koanf:"koji"`
	Tuning    tuning     `koanf:"tuning"`
	ScanRules []scanRule `koanf:"scan_rules"`
}

type koji struct {
	Url         string `koanf:"url"`
	BearerToken string `koanf:"bearer_token"`
}

type cleanup struct {
	Pokemon   bool `koanf:"pokemon"`
	Quests    bool `koanf:"quests"`
	Incidents bool `koanf:"incidents"`
	Stats     bool `koanf:"stats"`
	StatsDays int  `koanf:"stats_days"`
}

type webhook struct {
	Url       string         `koanf:"url"`
	Types     []string       `koanf:"types"`
	Areas     []string       `koanf:"areas"`
	AreaNames []geo.AreaName `koanf:"-"`
}

type pvp struct {
	Enabled               bool         `koanf:"enabled"`
	IncludeHundosUnderCap bool         `koanf:"include_hundos_under_cap"`
	LevelCaps             []int        `koanf:"level_caps"`
	Leagues               []pvpLeagues `koanf:"leagues"`
}

type pvpLeagues struct {
	Name           string `koanf:"name"`
	Cap            int    `koanf:"cap"`
	LittleCupRules bool   `koanf:"little"`
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
	ApiKey               string `koanf:"api_key"`
	Logger               bool   `koanf:"logger"`
	MutexProfileFraction int    `koanf:"mutex_profile_fraction"`
	BlockProfileRate     int    `koanf:"block_profile_rate"`
}

type logging struct {
	Debug    bool `koanf:"debug"`
	SaveLogs bool `koanf:"save_logs" default:"true"`
}

type database struct {
	Addr     string `koanf:"address"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Db       string `koanf:"db"`
	MaxPool  int    `koanf:"max_pool"`
}

type tuning struct {
	ExtendedTimeout bool `koanf:"extended_timeout"`
}

type scanRule struct {
	Areas            []string       `koanf:"areas"`
	AreaNames        []geo.AreaName `koanf:"-"`
	ScanContext      []string       `koanf:"context"`
	ProcessPokemon   *bool          `koanf:"pokemon"`
	ProcessWilds     *bool          `koanf:"wild_pokemon"`
	ProcessNearby    *bool          `koanf:"nearby_pokemon"`
	ProcessWeather   *bool          `koanf:"weather"`
	ProcessCells     *bool          `koanf:"cells"`
	ProcessPokestops *bool          `koanf:"pokestops"`
	ProcessGyms      *bool          `koanf:"gyms"`
}

var Config configDefinition
