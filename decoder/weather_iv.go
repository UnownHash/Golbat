package decoder

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/golang/geo/s2"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/pogo"
	"gopkg.in/guregu/null.v4"
	"net/http"
	"reflect"
	"time"
)

const masterFileURL = "https://raw.githubusercontent.com/WatWowMap/Masterfile-Generator/master/master-latest-rdm.json"

var errMasterFileFetch = errors.New("can't fetch remote Weather MasterFile")

type MasterFileData struct {
	Initialized bool                      `json:"-"`
	Pokemon     map[int]MasterFilePokemon `json:"pokemon"`
	Costumes    map[int]bool              `json:"costumes"`
}

type MasterFilePokemon struct {
	Name  string                 `json:"name"`
	Types []int                  `json:"types"`
	Forms map[int]MasterFileForm `json:"forms"`
}

type MasterFileForm struct {
	Types []int `json:"types"`
}

func fetchMasterFile() (MasterFileData, error) {
	req, err := http.NewRequest("GET", masterFileURL, nil)
	if err != nil {
		return MasterFileData{}, errMasterFileFetch
	}
	req.Header.Set("User-Agent", "Golbat")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return MasterFileData{}, errMasterFileFetch
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()

	var data MasterFileData
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return MasterFileData{}, errors.New("can't decode remote Weather MasterFile")
	}
	data.Initialized = true
	return data, nil
}

var watcherChan chan bool
var masterFileData MasterFileData

func WatchMasterFileData() error {
	if watcherChan != nil {
		return errors.New("Weather MasterFile watcher is already running")
	}

	log.Infof("Weather MasterFile Watcher Started")
	watcherChan = make(chan bool)
	var interval time.Duration

	interval = 60 * time.Minute

	go func() {
		ticker := time.NewTicker(interval)

		for {
			select {
			case <-watcherChan:
				log.Infof("MasterFile Watcher Stopped")
				ticker.Stop()
				return
			case <-ticker.C:
				log.Infof("Checking remote Weather MasterFile")
				pokemonData, err := fetchMasterFile()
				if err != nil {
					log.Infof("Remote Weather MasterFile fetch failed")
					continue
				}
				if reflect.DeepEqual(masterFileData, pokemonData) {
					continue
				} else {
					log.Infof("New Weather MasterFile found! Updating PokemonData")
					masterFileData = pokemonData // overwrite PokemonData using new MasterFile
				}
			}
		}
	}()
	return nil
}

type WeatherUpdate struct {
	S2CellId   int64
	NewWeather int32
}

var boostedWeatherLookup = []uint8{0, 8, 16, 32, 16, 2, 8, 4, 128, 64, 2, 4, 2, 4, 32, 64, 32, 128, 16}

func findBoostedWeathers(pokemonId int16, form int64, isDitto bool) (result uint8) {
	pokemon, ok := masterFileData.Pokemon[int(pokemonId)]
	if !ok {
		log.Warnf("Unknown PokemonId %d", pokemonId)
		return
	}
	if !isDitto && form > 0 {
		formData, ok := pokemon.Forms[int(form)]
		if !ok {
			log.Warnf("Unknown Form %d for PokemonId %d", form, pokemonId)
		} else if formData.Types != nil {
			for _, t := range formData.Types {
				result |= boostedWeatherLookup[t]
			}
			return
		}
	}
	for _, t := range pokemon.Types {
		result |= boostedWeatherLookup[t]
	}
	return
}

func ProactiveIVSwitch(ctx context.Context, db db.DbDetails, weatherUpdate WeatherUpdate, toDB bool) {
	if !masterFileData.Initialized {
		return
	}
	weatherCell := s2.CellFromCellID(s2.CellID(weatherUpdate.S2CellId))
	cellBound := weatherCell.CapBound().RectBound()
	cellLo := cellBound.Lo()
	cellHi := cellBound.Hi()

	start := time.Now()
	pokemonTreeMutex.RLock()
	pokemonTree2 := pokemonTree.Copy()
	pokemonTreeMutex.RUnlock()
	lockedTime := time.Since(start)

	startUnix := start.Unix()
	pokemonExamined := 0
	pokemonLocked := 0
	pokemonUpdated := 0
	pokemonCpUpdated := 0
	var pokemon Pokemon
	pokemonTree2.Search([2]float64{cellLo.Lng.Degrees(), cellLo.Lat.Degrees()}, [2]float64{cellHi.Lng.Degrees(), cellHi.Lat.Degrees()}, func(min, max [2]float64, pokemonId uint64) bool {
		pokemonExamined++
		pokemonEntry := pokemonCache.Get(pokemonId)
		if pokemonEntry == nil {
			return true
		}
		pokemon = pokemonEntry.Value()
		if !pokemon.AtkIv.Valid && len(pokemon.GolbatInternal) == 0 && len(pokemon.internal.ScanHistory) == 0 || // skip if no encounter history
			pokemon.ExpireTimestamp.ValueOrZero() < startUnix {
			return true
		}
		cachedPokemonId := pokemon.PokemonId
		cachedForm := pokemon.Form.ValueOrZero()
		boostedWeathers := findBoostedWeathers(cachedPokemonId, cachedForm, pokemon.IsDitto)
		if boostedWeathers == 0 {
			return true
		}
		var newWeather int32
		if boostedWeathers&uint8(1)<<weatherUpdate.NewWeather != 0 {
			newWeather = weatherUpdate.NewWeather
		}
		if int64(newWeather) == pokemon.Weather.ValueOrZero() ||
			!weatherCell.ContainsPoint(s2.PointFromLatLng(s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon))) {
			return true
		}
		pokemonMutex, _ := pokemonStripedMutex.GetLock(pokemonId)
		pokemonMutex.Lock()
		pokemonLocked++
		pokemonEntry = pokemonCache.Get(pokemonId) // refresh copy after acquiring mutex
		if pokemonEntry != nil {
			pokemon = pokemonEntry.Value()
			if cachedPokemonId == pokemon.PokemonId && cachedForm == pokemon.Form.ValueOrZero() && int64(newWeather) != pokemon.Weather.ValueOrZero() && pokemon.ExpireTimestamp.ValueOrZero() >= startUnix {
				pokemon.repopulateIv(int64(newWeather), pokemon.IsStrong.ValueOrZero())
				if !pokemon.Cp.Valid {
					pokemon.Weather = null.IntFrom(int64(newWeather))
					pokemon.recomputeCpIfNeeded(ctx, db, map[int64]pogo.GameplayWeatherProto_WeatherCondition{
						weatherUpdate.S2CellId: pogo.GameplayWeatherProto_WeatherCondition(newWeather),
					})
					savePokemonRecordAsAtTime(ctx, db, &pokemon, false, toDB && pokemon.Cp.Valid, startUnix)
					pokemonUpdated++
					if pokemon.Cp.Valid {
						pokemonCpUpdated++
					}
				}
			}
		}
		pokemonMutex.Unlock()
		return true
	})
	log.Infof("ProactiveIVSwitch - scan time %s (locked time %s), %d/%d/%d/%d scanned/locked/updated/cp updated", time.Since(start), lockedTime, pokemonExamined, pokemonLocked, pokemonUpdated, pokemonCpUpdated)
}
