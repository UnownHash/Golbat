package config

import (
	"encoding/json"
	"github.com/pelletier/go-toml/v2"
	"golbat/geo"
	"io/ioutil"
	"os"
	"strings"
)

func ReadJsonConfig() {
	jsonFile, err := os.Open("config.json")
	// if we os.Open returns an error then handle it
	if err != nil {
		panic(err)
	}
	// defer the closing of our jsonFile so that we can parse it later on
	defer jsonFile.Close()

	byteValue, _ := ioutil.ReadAll(jsonFile)

	err = json.Unmarshal([]byte(byteValue), &Config)
	if err != nil {
		panic(err)

	}
}

func ReadConfig() {
	tomlFile, err := os.Open("config.toml")
	// if we os.Open returns an error then handle it
	if err != nil {
		panic(err)
	}
	// defer the closing of our tomlFile so that we can parse it later on
	defer tomlFile.Close()

	byteValue, _ := ioutil.ReadAll(tomlFile)

	// Provide a default value
	Config.Logging.SaveLogs = true
	Config.Cleanup.StatsDays = 7
	Config.Database.MaxPool = 50

	err = toml.Unmarshal([]byte(byteValue), &Config)
	if err != nil {
		panic(err)
	}
	// translate webhook areas to array of geo.AreaName struct
	for i := 0; i < len(Config.Webhooks); i++ {
		hook := &Config.Webhooks[i]
		hook.AreaNames = splitIntoAreaAndFenceName(hook.Areas)
	}
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
