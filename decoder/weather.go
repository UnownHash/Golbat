package decoder

import (
	"database/sql"
	"github.com/golang/geo/s2"
	"github.com/google/go-cmp/cmp"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/pogo"
	"gopkg.in/guregu/null.v4"
)

type Weather struct {
	Id                 int64     `db:"id"`
	Latitude           float64   `db:"latitude"`
	Longitude          float64   `db:"longitude"`
	Level              null.Int  `db:"level"`
	GameplayCondition  null.Int  `db:"gameplay_condition"`
	WindDirection      null.Int  `db:"wind_direction"`
	CloudLevel         null.Int  `db:"cloud_level"`
	RainLevel          null.Int  `db:"rain_level"`
	WindLevel          null.Int  `db:"wind_level"`
	SnowLevel          null.Int  `db:"snow_level"`
	FogLevel           null.Int  `db:"fog_level"`
	SpecialEffectLevel null.Int  `db:"special_effect_level"`
	Severity           null.Int  `db:"severity"`
	WarnWeather        null.Bool `db:"warn_weather"`
	Updated            int64     `db:"updated"`
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

	err := db.GeneralDb.Get(&weather, "SELECT id, latitude, longitude, level, gameplay_condition, wind_direction, cloud_level, rain_level, wind_level, snow_level, fog_level, special_effect_level, severity, warn_weather, updated FROM weather WHERE id = ?", weatherId)

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
	s2cell := s2.CellFromCellID(s2.CellID(clientWeather.S2CellId))
	weather.Latitude = s2cell.CapBound().RectBound().Center().Lat.Degrees()
	weather.Longitude = s2cell.CapBound().RectBound().Center().Lng.Degrees()
	weather.Level = null.IntFrom(int64(s2cell.Level()))
	weather.GameplayCondition = null.IntFrom(int64(clientWeather.GameplayWeather.GameplayCondition))
	weather.WindDirection = null.IntFrom(int64(clientWeather.DisplayWeather.WindDirection))
	weather.CloudLevel = null.IntFrom(int64(clientWeather.DisplayWeather.CloudLevel))
	weather.RainLevel = null.IntFrom(int64(clientWeather.DisplayWeather.RainLevel))
	weather.WindLevel = null.IntFrom(int64(clientWeather.DisplayWeather.WindLevel))
	weather.SnowLevel = null.IntFrom(int64(clientWeather.DisplayWeather.SnowLevel))
	weather.FogLevel = null.IntFrom(int64(clientWeather.DisplayWeather.FogLevel))
	weather.SpecialEffectLevel = null.IntFrom(int64(clientWeather.DisplayWeather.SpecialEffectLevel))
	for _, alert := range clientWeather.Alerts {
		weather.Severity = null.IntFrom(int64(alert.Severity))
		weather.WarnWeather = null.BoolFrom(alert.WarnWeather)
	}
	return weather
}

func hasChangesWeather(old *Weather, new *Weather) bool {
	return !cmp.Equal(old, new, ignoreNearFloats)
}

func createWeatherWebhooks(oldWeather *Weather, weather *Weather) {
	//TODO
}

func saveWeatherRecord(db db.DbDetails, weather *Weather) {
	oldWeather, _ := getWeatherRecord(db, weather.Id)
	if oldWeather != nil && !hasChangesWeather(oldWeather, weather) {
		return
	}

	if oldWeather == nil {
		res, err := db.GeneralDb.NamedExec(
			"INSERT INTO weather ("+
				"id, latitude, longitude, level, gameplay_condition, wind_direction, cloud_level, rain_level, "+
				"wind_level, snow_level, fog_level, special_effect_level, severity, warn_weather, updated)"+
				"VALUES ("+
				":id, :latitude, :longitude, :level, :gameplay_condition, :wind_direction, :cloud_level, :rain_level, "+
				":wind_level, :snow_level, :fog_level, :special_effect_level, :severity, :warn_weather, "+
				"UNIX_TIMESTAMP())",
			weather)
		if err != nil {
			log.Errorf("insert weather: %s", err)
			return
		}
		_ = res
	} else {
		res, err := db.GeneralDb.NamedExec(
			"UPDATE weather SET"+
				"latitude = :latitude"+
				"longitude = :longitude"+
				"level = :level"+
				"gameplay_condition = :gameplay_condition"+
				"wind_direction = :wind_direction"+
				"cloud_level = :cloud_level"+
				"rain_level = :rain_level"+
				"wind_level = :wind_level"+
				"snow_level = :snow_level"+
				"fog_level = :fog_level"+
				"special_effect_level = :special_effect_level"+
				"severity = :severity"+
				"warn_weather = :warn_weather"+
				"updated = UNIX_TIMESTAMP()"+
				"WHERE id = :id",
			weather)
		if err != nil {
			log.Errorf("update weather: %s", err)
			return
		}
		_ = res
	}
	weatherCache.Set(weather.Id, *weather, ttlcache.DefaultTTL)
	createWeatherWebhooks(oldWeather, weather)
}
