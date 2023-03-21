package config

import "golbat/geo"

type configDefinition struct {
	Port            int       `toml:"port"`
	Webhooks        []webhook `toml:"webhooks"`
	Database        database  `toml:"database"`
	Stats           bool      `toml:"stats"`
	Logging         logging   `toml:"logging"`
	InMemory        bool      `toml:"in_memory"`
	Cleanup         cleanup   `toml:"cleanup"`
	RawBearer       string    `toml:"raw_bearer"`
	ApiSecret       string    `toml:"api_secret"`
	Pvp             pvp       `toml:"pvp"`
	KojiBearerToken string    `toml:"koji_bearer_token"`
	KojiUrl         string    `toml:"koji_url"`
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

type logging struct {
	Debug    bool `toml:"debug"`
	SaveLogs bool `toml:"save_logs" default:"true"`
}

type database struct {
	Addr     string `toml:"address"`
	User     string `toml:"user"`
	Password string `toml:"password"`
	Db       string `toml:"db"`
}

var Config configDefinition
