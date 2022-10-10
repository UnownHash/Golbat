package config

type configDefinition struct {
	Port      int       `toml:"port"`
	Webhooks  []webhook `toml:"webhooks"`
	Database  database  `toml:"database"`
	Stats     bool      `toml:"stats"`
	Logging   logging   `toml:"logging"`
	InMemory  bool      `toml:"in_memory"`
	Cleanup   cleanup   `toml:"cleanup"`
	RawBearer string    `toml:"raw_bearer"`
	ApiSecret string    `toml:"api_secret"`
}

type cleanup struct {
	Pokemon   bool `toml:"pokemon"`
	Quests    bool `toml:"quests"`
	Incidents bool `toml:"incidents"`
}

type webhook struct {
	Url   string   `toml:"url"`
	Types []string `toml:"types"`
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
