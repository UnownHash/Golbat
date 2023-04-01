package config

import "golbat/geo"

type configDefinition struct {
	Port      int        `toml:"port"`
	Webhooks  []webhook  `toml:"webhooks"`
	Database  database   `toml:"database"`
	Stats     bool       `toml:"stats"`
	Logging   logging    `toml:"logging"`
	Sentry    sentry     `toml:"sentry"`
	Pyroscope pyroscope  `toml:"pyroscope"`
	InMemory  bool       `toml:"in_memory"`
	Cleanup   cleanup    `toml:"cleanup"`
	RawBearer string     `toml:"raw_bearer"`
	ApiSecret string     `toml:"api_secret"`
	Pvp       pvp        `toml:"pvp"`
	Koji      koji       `toml:"koji"`
	Tuning    tuning     `toml:"tuning"`
	ScanRules []scanRule `toml:"scan_rules"`
}

type koji struct {
	Url         string `toml:"url"`
	BearerToken string `toml:"bearer_token"`
}

type cleanup struct {
	Pokemon   bool `toml:"pokemon"`
	Quests    bool `toml:"quests"`
	Incidents bool `toml:"incidents"`
	Stats     bool `toml:"stats"`
	StatsDays int  `toml:"stats_days"`
}

type webhook struct {
	Url       string         `toml:"url"`
	Types     []string       `toml:"types"`
	Areas     []string       `toml:"areas"`
	AreaNames []geo.AreaName `toml:"-"`
}

type pvp struct {
	Enabled               bool         `toml:"enabled"`
	IncludeHundosUnderCap bool         `toml:"include_hundos_under_cap"`
	LevelCaps             []int        `toml:"level_caps"`
	Leagues               []pvpLeagues `toml:"leagues"`
}

type pvpLeagues struct {
	Name           string `toml:"name"`
	Cap            int    `toml:"cap"`
	LittleCupRules bool   `toml:"little"`
}

type sentry struct {
	DSN              string  `toml:"dsn"`
	SampleRate       float64 `toml:"sample_rate"`
	EnableTracing    bool    `toml:"enable_tracing"`
	TracesSampleRate float64 `toml:"traces_sample_rate"`
}

type pyroscope struct {
	ApplicationName      string `toml:"application_name"`
	ServerAddress        string `toml:"server_address"`
	ApiKey               string `toml:"api_key"`
	Logger               bool   `toml:"logger"`
	MutexProfileFraction int    `toml:"mutex_profile_fraction"`
	BlockProfileRate     int    `toml:"block_profile_rate"`
}

type logging struct {
	Debug    bool `toml:"debug"`
	SaveLogs bool `toml:"save_logs" default:"true"`
}

type database struct {
	Addr     string `toml:"address"`
	User     string `toml:"user"`
	Password string `toml:"password"`
	Db       string `toml:"db"`
	MaxPool  int    `toml:"max_pool"`
}

type tuning struct {
	ExtendedTimeout bool `toml:"extended_timeout"`
	ProcessWilds    bool `toml:"process_wild_pokemon"`
	ProcessNearby   bool `toml:"process_nearby_pokemon"`
}

type scanRule struct {
	Areas            []string       `toml:"areas"`
	AreaNames        []geo.AreaName `toml:"-"`
	ScanContext      []string       `toml:"context"`
	ProcessPokemon   *bool          `toml:"pokemon"`
	ProcessWilds     *bool          `toml:"wild_pokemon"`
	ProcessNearby    *bool          `toml:"nearby_pokemon"`
	ProcessWeather   *bool          `toml:"weather"`
	ProcessCells     *bool          `toml:"cells"`
	ProcessPokestops *bool          `toml:"pokestops"`
	ProcessGyms      *bool          `toml:"gyms"`
}

var Config = configDefinition{
	Sentry: sentry{
		SampleRate:       1.0,
		TracesSampleRate: 1.0,
	},
	Pyroscope: pyroscope{
		ApplicationName:      "golbat",
		MutexProfileFraction: 5,
		BlockProfileRate:     5,
	},
}
