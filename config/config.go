package config

type configDefinition struct {
	Port     int      `json:"port"`
	Webhooks []string `json:"webhooks"`
	Database database `json:"database"`
	Archive  bool     `json:"archive"`
	Stats    bool     `json:"stats"`
	DebugLog bool     `json:"debug"`
	InMemory bool     `json:"inMemory"`
}

type database struct {
	Addr     string `json:"address"`
	User     string `json:"user"`
	Password string `json:"password"`
	Db       string `json:"db"`
}

var Config configDefinition
