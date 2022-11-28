package decoder

import (
	"database/sql"
	"github.com/google/go-cmp/cmp"
	"github.com/jellydator/ttlcache/v3"
	"golbat/db"
	"golbat/pogo"
)

type Weather struct {
	Id        int64   `db:"id"`
	Latitude  float64 `db:"latitude"`
	Longitude float64 `db:"longitude"`

	Updated int64 `db:"updated"`
}

// CREATE TABLE `weather` (
//  `id` bigint NOT NULL,
//  `level` tinyint unsigned DEFAULT NULL,
//  `latitude` double(18,14) NOT NULL DEFAULT '0.00000000000000',
//  `longitude` double(18,14) NOT NULL DEFAULT '0.00000000000000',
//  `gameplay_condition` tinyint unsigned DEFAULT NULL,
//  `wind_direction` mediumint DEFAULT NULL,
//  `cloud_level` tinyint unsigned DEFAULT NULL,
//  `rain_level` tinyint unsigned DEFAULT NULL,
//  `wind_level` tinyint unsigned DEFAULT NULL,
//  `snow_level` tinyint unsigned DEFAULT NULL,
//  `fog_level` tinyint unsigned DEFAULT NULL,
//  `special_effect_level` tinyint unsigned DEFAULT NULL,
//  `severity` tinyint unsigned DEFAULT NULL,
//  `warn_weather` tinyint unsigned DEFAULT NULL,
//  `updated` int unsigned NOT NULL,
//  PRIMARY KEY (`id`)
//)

func getWeatherRecord(db db.DbDetails, weatherId int64) (*Weather, error) {
	inMemoryWeather := weatherCache.Get(weatherId)
	if inMemoryWeather != nil {
		weather := inMemoryWeather.Value()
		return &weather, nil
	}
	weather := Weather{}

	err := db.GeneralDb.Get(&weather, "SELECT * FROM weather WHERE id = ?", weatherId)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	weatherCache.Set(weatherId, weather, ttlcache.DefaultTTL)
	return &weather, nil
}

func (weather *Weather) updateWeatherFromClientWeatherProto(clientWeather *pogo.ClientWeatherProto) *Weather {
	weather.Id = clientWeather.S2CellId
	return weather
}

func hasChangesWeather(old *Weather, new *Weather) bool {
	return !cmp.Equal(old, new, ignoreNearFloats)
}

func createWeatherWebhooks(oldWeather *Weather, weather *Weather) {

}

func saveWeatherRecord(db db.DbDetails, weather *Weather) {
	oldWeather, _ := getWeatherRecord(db, weather.Id)
	if oldWeather != nil && !hasChangesWeather(oldWeather, weather) {
		return
	}

	if oldWeather == nil {

	} else {

	}

	weatherCache.Set(weather.Id, *weather, ttlcache.DefaultTTL)
	createWeatherWebhooks(oldWeather, weather)
}
