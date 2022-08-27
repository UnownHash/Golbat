package config

type configDefinition struct {
	Port     int       `json:"port"`
	Webhooks []webhook `json:"webhooks"`
	Database database  `json:"database"`
	Stats    bool      `json:"stats"`
	Logging  logging   `json:"logging"`
	InMemory bool      `json:"inMemory"`
	Cleanup  cleanup   `json:"cleanup"`
}

type cleanup struct {
	Pokemon   bool `json:"pokemon"`
	Quests    bool `json:"quests"`
	Incidents bool `json:"incidents"`
}

type webhook struct {
	Url   string   `json:"url"`
	Types []string `json:"types"`
}

type logging struct {
	Debug bool `json:"debug"`
}

type database struct {
	Addr     string `json:"address"`
	User     string `json:"user"`
	Password string `json:"password"`
	Db       string `json:"db"`
}

var Config configDefinition
