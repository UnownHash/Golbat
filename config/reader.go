package config

import (
	"fmt"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
	"golbat/geo"
	"strconv"
	"strings"
)

var k = koanf.New(".")

func ReadConfig() {
	// Default values
	defaultErr := k.Load(structs.Provider(configDefinition{
		Sentry: sentry{
			SampleRate:       1.0,
			TracesSampleRate: 1.0,
		},
		Pyroscope: pyroscope{
			ApplicationName:      "golbat",
			MutexProfileFraction: 5,
			BlockProfileRate:     5,
		},
		Logging: logging{
			SaveLogs: false,
		},
		Cleanup: cleanup{
			StatsDays: 7,
		},
		Database: database{
			MaxPool: 100,
		},
	}, "koanf"), nil)
	if defaultErr != nil {
		fmt.Println(fmt.Errorf("failed to load default config: %w", defaultErr))
	}

	readConfigErr := k.Load(file.Provider("config.toml"), toml.Parser())
	if readConfigErr != nil && readConfigErr.Error() != "open config.toml: no such file or directory" {
		fmt.Println(fmt.Errorf("failed to read config file: %w", readConfigErr))
	}

	envLoadingErr := k.Load(ProviderWithValue("GOLBAT.", ".", func(rawKey string, value string, currentMap map[string]interface{}) (string, interface{}) {
		key := strings.ToLower(strings.TrimPrefix(rawKey, "GOLBAT."))

		if strings.HasPrefix(key, "webhooks") {
			parseEnvVarToSlice("webhooks", key, value, currentMap)

			return "", nil
		} else if strings.HasPrefix(key, "pvp.leagues") {
			parseEnvVarToSlice("pvp.leagues", key, value, currentMap)

			return "", nil
		} else if strings.HasPrefix(key, "scan_rules") {
			parseEnvVarToSlice("scan_rules", key, value, currentMap)

			return "", nil
		}

		return key, value
	}), nil)

	if envLoadingErr != nil {
		fmt.Println(fmt.Errorf("%w", envLoadingErr))
	}

	unmarshalError := k.Unmarshal("", &Config)
	if unmarshalError != nil {
		panic(fmt.Errorf("failed to Unmarshal config: %w", unmarshalError))
		return
	}

	// translate webhook areas to array of geo.AreaName struct
	for i := 0; i < len(Config.Webhooks); i++ {
		hook := &Config.Webhooks[i]
		hook.AreaNames = splitIntoAreaAndFenceName(hook.Areas)
	}

	// translate scan areas to array of geo.AreaName struct
	for i := 0; i < len(Config.ScanRules); i++ {
		rule := &Config.ScanRules[i]
		rule.AreaNames = splitIntoAreaAndFenceName(rule.Areas)
	}
}

func parseEnvVarToSlice(sliceName string, key string, value string, currentMap map[string]interface{}) {
	splitPath := strings.Split(key, ".")
	lastPart := splitPath[len(splitPath)-1]
	index, _ := strconv.Atoi(splitPath[len(splitPath)-2])

	// create the slice if it doesn't exist
	if currentMap[sliceName] == nil {
		currentMap[sliceName] = make([]interface{}, 0)
	}
	// create the element at index
	if len(currentMap[sliceName].([]interface{})) <= index {
		currentMap[sliceName] = append(currentMap[sliceName].([]interface{}), map[string]interface{}{})
	}

	// set the value in map at index in slice
	currentMap[sliceName].([]interface{})[index].(map[string]interface{})[lastPart] = value
}

func splitIntoAreaAndFenceName(areaNames []string) (areas []geo.AreaName) {
	for _, areaName := range areaNames {
		splitted := strings.Split(areaName, "/") // "London/*", "London/Chelsea", "Chelsea"
		if len(splitted) == 2 {
			areas = append(areas, geo.AreaName{Parent: splitted[0], Name: splitted[1]})
		} else {
			areas = append(areas, geo.AreaName{Parent: "*", Name: areaName})
		}
	}
	return
}
