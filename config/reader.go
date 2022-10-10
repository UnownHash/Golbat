package config

import (
	"encoding/json"
	"github.com/pelletier/go-toml/v2"
	"io/ioutil"
	"os"
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

	err = toml.Unmarshal([]byte(byteValue), &Config)
	if err != nil {
		panic(err)
	}
}
