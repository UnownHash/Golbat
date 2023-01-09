package config

import (
	"encoding/json"
	"github.com/pelletier/go-toml/v2"
	"io"
	"os"
)

//goland:noinspection GoUnusedExportedFunction
func ReadJsonConfig() {
	jsonFile, err := os.Open("config.json")
	// if we os.Open returns an error then handle it
	if err != nil {
		panic(err)
	}
	// defer the closing of our jsonFile so that we can parse it later on
	//goland:noinspection GoUnhandledErrorResult
	defer jsonFile.Close()

	byteValue, _ := io.ReadAll(jsonFile)

	err = json.Unmarshal(byteValue, &Config)
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
	//goland:noinspection GoUnhandledErrorResult
	defer tomlFile.Close()

	byteValue, _ := io.ReadAll(tomlFile)

	// Provide a default value
	Config.Logging.SaveLogs = true

	err = toml.Unmarshal(byteValue, &Config)
	if err != nil {
		panic(err)
	}
}
