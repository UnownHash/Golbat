package decoder

import (
	"context"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
)

type WeatherUpdate struct {
	S2CellId   int64
	NewWeather int32
}

var boostedWeatherLookup = []uint8{0, 8, 16, 32, 16, 2, 8, 4, 128, 64, 2, 4, 2, 4, 32, 64, 32, 128, 16}

func findBoostedWeathers(data MasterFileData, pokemonId, form int16) (result uint8) {
	pokemon, ok := data.Pokemon[int(pokemonId)]
	if !ok {
		log.Warnf("Unknown PokemonId %d", pokemonId)
		return
	}
	if form > 0 {
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

func ProactiveIVSwitch(ctx context.Context, db db.DbDetails, weatherUpdate WeatherUpdate, toDB bool, timestamp int64) {
	data := getMasterFileData()
	if !data.Initialized {
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
	//var pokemon *Pokemon
	pokemonTree2.Search([2]float64{cellLo.Lng.Degrees(), cellLo.Lat.Degrees()}, [2]float64{cellHi.Lng.Degrees(), cellHi.Lat.Degrees()}, func(min, max [2]float64, pokemonId uint64) bool {
		if !weatherCell.ContainsPoint(s2.PointFromLatLng(s2.LatLngFromDegrees(min[1], min[0]))) {
			return true
		}
		pokemonExamined++
		pokemonLookup, found := pokemonLookupCache.Load(pokemonId)
		if !found || !pokemonLookup.PokemonLookup.HasEncounterValues {
			return true
		}
		boostedWeathers := findBoostedWeathers(data, pokemonLookup.PokemonLookup.PokemonId, pokemonLookup.PokemonLookup.Form)
		if boostedWeathers == 0 {
			return true
		}
		var newWeather int32
		if boostedWeathers&uint8(1)<<weatherUpdate.NewWeather != 0 {
			newWeather = weatherUpdate.NewWeather
		}
		if int8(newWeather) == pokemonLookup.PokemonLookup.Weather {
			return true
		}

		pokemon, unlock, _ := peekPokemonRecordReadOnly(pokemonId)
		if pokemon != nil {
			pokemonLocked++
			if pokemonLookup.PokemonLookup.PokemonId == pokemon.PokemonId && (pokemon.IsDitto || int64(pokemonLookup.PokemonLookup.Form) == pokemon.Form.ValueOrZero()) && int64(newWeather) != pokemon.Weather.ValueOrZero() && pokemon.ExpireTimestamp.ValueOrZero() >= startUnix && pokemon.Updated.ValueOrZero() < timestamp {
				pokemon.snapshotOldValues()
				pokemon.repopulateIv(int64(newWeather), pokemon.IsStrong.ValueOrZero())
				if !pokemon.Cp.Valid {
					pokemon.Weather = null.IntFrom(int64(newWeather))
					pokemon.recomputeCpIfNeeded(ctx, db, map[int64]pogo.GameplayWeatherProto_WeatherCondition{
						weatherUpdate.S2CellId: pogo.GameplayWeatherProto_WeatherCondition(newWeather),
					})
					savePokemonRecordAsAtTime(ctx, db, pokemon, false, toDB && pokemon.Cp.Valid, pokemon.Cp.Valid, timestamp)
					pokemonUpdated++
					if pokemon.Cp.Valid {
						pokemonCpUpdated++
					}
				}
			}
			unlock()
		}
		return true
	})
	if pokemonCpUpdated > 0 {
		log.Infof("ProactiveIVSwitch - %d->%d, scan time %s (locked time %s), %d/%d/%d/%d scanned/locked/updated/cp updated", weatherUpdate.S2CellId, weatherUpdate.NewWeather, time.Since(start), lockedTime, pokemonExamined, pokemonLocked, pokemonUpdated, pokemonCpUpdated)
	} else {
		log.Debugf("ProactiveIVSwitch - %d->%d, scan time %s (locked time %s), %d/%d/%d scanned/locked/updated", weatherUpdate.S2CellId, weatherUpdate.NewWeather, time.Since(start), lockedTime, pokemonExamined, pokemonLocked, pokemonUpdated)
	}
}
