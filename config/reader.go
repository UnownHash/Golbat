package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"golbat/geo"
)

var k = koanf.New(".")

// DefaultConfigPath is used when no config file is given on the command line.
// A missing file at this path is not an error: the whole config can be supplied
// through GOLBAT_* environment variables instead.
const DefaultConfigPath = "config.toml"

// ReadConfig loads configuration from defaults, then the TOML file at
// configPath, then GOLBAT_* environment variables, each layer overriding the
// previous one. Pass DefaultConfigPath for the standard location.
func ReadConfig(configPath string) (configDefinition, error) {
	// Default values
	defaultErr := k.Load(structs.Provider(configDefinition{
		ApiDocs: true,
		Sentry: sentry{
			SampleRate:       1.0,
			TracesSampleRate: 1.0,
		},
		Pyroscope: pyroscope{
			ApplicationName:      "golbat",
			MutexProfileFraction: 5,
			BlockProfileRate:     5,
		},
		Prometheus: Prometheus{
			BucketSize:     []float64{.00005, .000075, .0001, .00025, .0005, .00075, .001, .0025, .005, .01, .05, .1, .25, .5, 1, 2.5, 5, 10},
			LiveStatsSleep: 120,
		},
		Logging: logging{
			Debug:             false,
			ApiRequestLogging: false,
			SaveLogs:          false,
			MaxSize:           50,
			MaxBackups:        10,
			MaxAge:            30,
			Compress:          true,
		},
		Cleanup: cleanup{
			Pokemon:        true,
			Quests:         true,
			Incidents:      true,
			StationBattles: true,
			Tappables:      true,
			StatsDays:      7,
			DeviceHours:    24,
		},
		Database: database{
			MaxPool: 100,
		},
		Tuning: tuning{
			MaxPokemonResults:              3000,
			MaxFortResults:                 9000,
			MaxPokemonDistance:             100,
			MaxConcurrentProactiveIVSwitch: 6,
			ReduceUpdates:                  false,
			WriteBehindStartupDelay:        120, // 2 minutes
			WriteBehindWorkerCount:         16,  // concurrent writers (see config.toml.example)
			WriteBehindBatchSize:           50,  // entries per batch
			WriteBehindBatchTimeoutMs:      100, // ms to wait for batch to fill
		},
		Weather: weather{
			ProactiveIVSwitching:     true,
			ProactiveIVSwitchingToDB: false,
		},
		Pvp: pvp{
			LevelCaps: []int{50, 51},
		},
		StatsIntervals: statsIntervals{
			PokemonStatsIntervalMinutes:  1,
			PokemonCountIntervalMinutes:  10,
			RaidStatsIntervalMinutes:     10,
			InvasionStatsIntervalMinutes: 15,
			QuestStatsIntervalMinutes:    15,
		},
	}, "koanf"), nil)
	if defaultErr != nil {
		fmt.Println(fmt.Errorf("failed to load default config: %w", defaultErr))
	}

	readConfigErr := k.Load(file.Provider(configPath), toml.Parser())
	if readConfigErr != nil {
		if !errors.Is(readConfigErr, fs.ErrNotExist) {
			fmt.Println(fmt.Errorf("failed to read config file %s: %w", configPath, readConfigErr))
		} else if configPath != DefaultConfigPath {
			// The default file is optional, but a file the user explicitly asked
			// for must exist - otherwise we would silently start on defaults.
			return Config, fmt.Errorf("config file %s not found", configPath)
		}
	}

	envLoadingErr := k.Load(ProviderWithValue("GOLBAT.", ".", func(rawKey string, value string, currentMap map[string]interface{}) (string, interface{}) {
		key := strings.ToLower(strings.TrimPrefix(rawKey, "GOLBAT."))

		if strings.HasPrefix(key, "webhooks") {
			parseEnvVarToSlice("webhooks", key, value, currentMap)

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
		return Config, fmt.Errorf("failed to Unmarshal config: %w", unmarshalError)
	}

	// translate webhook areas to array of geo.AreaName struct
	for i := 0; i < len(Config.Webhooks); i++ {
		hook := &Config.Webhooks[i]
		hook.AreaNames = splitIntoAreaAndFenceName(hook.Areas)
		hook.ExcludeAreaNames = splitIntoAreaAndFenceName(hook.ExcludeAreas)
		hook.HeaderMap = splitIntoHeaderMap(hook.Headers)
	}

	// translate scan areas to array of geo.AreaName struct
	for i := 0; i < len(Config.ScanRules); i++ {
		rule := &Config.ScanRules[i]
		rule.AreaNames = splitIntoAreaAndFenceName(rule.Areas)
	}

	return Config, nil
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

func splitIntoHeaderMap(rawHeader []string) map[string]string {
	headerMap := make(map[string]string)
	for _, header := range rawHeader {
		split := strings.Split(header, ":")
		if len(split) == 2 {
			headerMap[split[0]] = split[1]
		} else {
			fmt.Println(fmt.Errorf("invalid header: %s", header))
		}
	}
	return headerMap
}
