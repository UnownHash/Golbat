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

// weatherBoosts reports whether a findBoostedWeathers mask covers this weather.
//
// The mask is a bitmask of weather conditions, so membership is
// `mask & (1 << weather)` and the parentheses are load-bearing. Written inline
// as `mask&uint8(1)<<weather` it means something else entirely: Go gives << and
// & equal precedence and associates them left to right, so that parses as
// `(mask & 1) << weather`. Every value in boostedWeatherLookup is a power of
// two from 2 upwards, so bit 0 is never set, so `mask & 1` is always 0 — and
// the test is false for every species under every weather.
//
// The bounds check matters: a negative shift count panics in Go, and this runs
// inside the weather sweep over every pokemon in a cell.
func weatherBoosts(mask uint8, weather int32) bool {
	if weather < 0 || weather > 7 {
		return false
	}
	return mask&(uint8(1)<<uint(weather)) != 0
}

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

// displayedWeatherCondition returns the weather_boosted_condition that belongs
// in the weather COLUMN for this record under cellWeather — the value the game
// itself would report for what the record DISPLAYS.
//
// For anything but a disguised Ditto that is ownCondition, the caller's own
// findBoostedWeathers answer for the record's species, returned unchanged.
//
// A disguised Ditto is the exception, and the reason this function exists. Its
// PokemonId is 132, so ownCondition describes a Normal type, not the disguise.
// The column must hold the disguise's condition: an overworld client matches a
// sighting on the species and weather it can see, both of which are the
// disguise's, so storing the Ditto's own state there would make the record
// unmatchable. The same value is what repopulateIv takes for a Ditto, since a
// caught Ditto is boosted only in PP (see dittoCaughtBoosted), and
// recomputeCpIfNeeded reads the column to find the 0P case (Ditto boosted,
// disguise not) and compute the disguise's CP from unboosted values.
//
// An undisguised Ditto — a real wild one, IsDitto false or no DisplayPokemonId —
// displays as itself, so its own condition is already the displayed one.
func displayedWeatherCondition(data MasterFileData, pokemon *Pokemon, cellWeather int32, ownCondition int64) int64 {
	if !pokemon.IsDitto || !pokemon.DisplayPokemonId.Valid {
		return ownCondition
	}
	disguiseBoosted := findBoostedWeathers(data,
		int16(pokemon.DisplayPokemonId.ValueOrZero()),
		int16(int64OrZero(pokemon.DisplayPokemonForm)))
	if weatherBoosts(disguiseBoosted, cellWeather) {
		return narrowUint8(int64(cellWeather))
	}
	return int64(pogo.GameplayWeatherProto_NONE)
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
	// Weather flips are rare; take a fresh copy rather than the shared ≤1s
	// scan snapshot so pokemon added in the final second before the flip
	// are included in the sweep. Copy() mutates the source tree's COW
	// stamp — full lock required.
	pokemonTreeMutex.Lock()
	pokemonTree2 := pokemonTree.Copy()
	pokemonTreeMutex.Unlock()
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
		// Narrowed once, here, and used by both comparisons below. The
		// weather column is a tinyint, so a stored weather has been through
		// narrowUint8 already; comparing the raw proto value against it would
		// be two different equivalence relations on the same value in
		// adjacent lines, and the cheap lookup-cache skip would then disagree
		// with the entity comparison about what "unchanged" means.
		var newWeather int64
		if weatherBoosts(boostedWeathers, weatherUpdate.NewWeather) {
			newWeather = narrowUint8(int64(weatherUpdate.NewWeather))
		}
		// The lookup and the entity spell "no weather" differently: the
		// lookup carries lookupInt8's absence marker, -1, while the entity
		// guard below reads the same absence through int64OrZero as 0.
		// Compare in the entity's encoding, because the entity guard is the
		// one that decides — this skip exists only to avoid reaching it, so
		// the two have to agree on what "unchanged" means or the skip stops
		// skipping anything. Left at -1, a NULL-weather pokemon never
		// matches a no-boost update (0 == -1 is false), so every weather
		// flip in its cell takes the entity lock for a guard that then
		// compares 0 != 0 and does nothing.
		storedWeather := int64(pokemonLookup.PokemonLookup.Weather)
		if storedWeather < 0 {
			storedWeather = 0
		}
		// This skip cannot decide anything for a Ditto. newWeather describes
		// species 132 — findBoostedWeathers ran on the record's own PokemonId
		// — while the stored column carries its DISGUISE's condition, which is
		// what a Ditto's stats actually follow. The two are different
		// quantities, so agreeing or disagreeing says nothing about whether
		// there is work to do. Resolving the disguise's condition needs
		// DisplayPokemonId and therefore the lock; Dittos are rare, so take it
		// and let the guard below, which can see both, decide.
		if pokemonLookup.PokemonLookup.PokemonId != int16(pogo.HoloPokemonId_DITTO) && newWeather == storedWeather {
			return true
		}

		pokemon, unlock, _ := peekPokemonRecordReadOnly(pokemonId, "ProactiveIVSwitch")
		if pokemon != nil {
			pokemonLocked++
			if pokemonLookup.PokemonLookup.PokemonId == pokemon.PokemonId && (pokemon.IsDitto || int64(pokemonLookup.PokemonLookup.Form) == int64OrZero(pokemon.Form)) && (pokemon.IsDitto || newWeather != int64OrZero(pokemon.Weather)) && int64OrZero(pokemon.ExpireTimestamp) >= startUnix && int64OrZero(pokemon.Updated) < timestamp {
				// For a Ditto, newWeather describes species 132 — Normal, so
				// boosted by partly cloudy — and that is NOT what its stats
				// follow. A caught Ditto is boosted only in PP, where the
				// weather boosts its DISGUISE as well, so the disguise's
				// condition drives both the stats and the column. See
				// displayedWeatherCondition.
				storedCondition := displayedWeatherCondition(data, pokemon, weatherUpdate.NewWeather, newWeather)
				pokemon.snapshotOldValues()
				pokemon.repopulateIv(storedCondition, pokemon.IsStrong.ValueOrZero())
				if !pokemon.Cp.Valid {
					// Deliberately SetWeather, not a bare pokemon.Weather = ...
					// field assignment: SetWeather marks the entity dirty, which
					// matters here specifically because savePokemonRecordAsAtTime
					// below early-returns when `!newRecord && !IsDirty()` (see its
					// first lines). A bare assignment updates only the in-memory
					// struct; if nothing else in this branch happened to dirty the
					// pokemon, the save call would no-op and updatePokemonLookup
					// inside it would never run, leaving the R-tree scan lookup
					// (PokemonLookup.Weather) stale while the entity's own field
					// had already changed. Going through the setter closes that
					// gap.
					pokemon.SetWeather(null.IntFrom(storedCondition))
					// The CELL's weather, not a boost condition: the only reader
					// is recomputeCpIfNeeded's Ditto arm, and it asks whether the
					// cell is PARTLY_CLOUDY.
					pokemon.recomputeCpIfNeeded(ctx, db, map[int64]pogo.GameplayWeatherProto_WeatherCondition{
						weatherUpdate.S2CellId: pogo.GameplayWeatherProto_WeatherCondition(weatherUpdate.NewWeather),
					})
					savePokemonRecordAsAtTime(ctx, db, pokemon, false, toDB && pokemon.Cp.Valid, pokemon.Cp.Valid, timestamp, "")
					pokemonUpdated++
					if pokemon.Cp.Valid {
						pokemonCpUpdated++
					}
				} else if storedCondition != int64OrZero(pokemon.Weather) {
					// The boosting condition changed without the boost state
					// changing — RAINY to WINDY over a Gyarados, which both
					// boost, or RAINY to SNOW over a Ditto's Water disguise.
					// repopulateIv rightly kept the IVs and CP, but the
					// condition the API and webhooks report is stale, so it is
					// written and saved.
					pokemon.SetWeather(null.IntFrom(storedCondition))
					savePokemonRecordAsAtTime(ctx, db, pokemon, false, toDB, true, timestamp, "")
					pokemonUpdated++
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
