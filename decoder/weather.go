package decoder

import (
	"context"
	"database/sql"
	"golbat/db"
	"golbat/pogo"
	"golbat/webhooks"

	"github.com/golang/geo/s2"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// Weather struct.
// REMINDER! Keep hasChangesWeather updated after making changes
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

func getWeatherRecord(ctx context.Context, db db.DbDetails, weatherId int64) (*Weather, error) {
	inMemoryWeather := weatherCache.Get(weatherId)
	if inMemoryWeather != nil {
		weather := inMemoryWeather.Value()
		return &weather, nil
	}
	weather := Weather{}

	err := db.GeneralDb.GetContext(ctx, &weather, "SELECT id, latitude, longitude, level, gameplay_condition, wind_direction, cloud_level, rain_level, wind_level, snow_level, fog_level, special_effect_level, severity, warn_weather, updated FROM weather WHERE id = ?", weatherId)

	statsCollector.IncDbQuery("select weather", err)
	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	weatherCache.Set(weatherId, weather, ttlcache.DefaultTTL)
	return &weather, nil
}

func weatherCellIdFromLatLon(lat, lon float64) int64 {
	return int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lon)).Parent(10))
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

// hasChangesWeather compares two Weather structs
// Float tolerance: Latitude, Longitude
func hasChangesWeather(old *Weather, new *Weather) bool {
	return old.Id != new.Id ||
		old.Level != new.Level ||
		old.GameplayCondition != new.GameplayCondition ||
		old.WindDirection != new.WindDirection ||
		old.CloudLevel != new.CloudLevel ||
		old.RainLevel != new.RainLevel ||
		old.WindLevel != new.WindLevel ||
		old.SnowLevel != new.SnowLevel ||
		old.FogLevel != new.FogLevel ||
		old.SpecialEffectLevel != new.SpecialEffectLevel ||
		old.Severity != new.Severity ||
		old.WarnWeather != new.WarnWeather ||
		old.Updated != new.Updated ||
		!floatAlmostEqual(old.Latitude, new.Latitude, floatTolerance) ||
		!floatAlmostEqual(old.Longitude, new.Longitude, floatTolerance)
}

func createWeatherWebhooks(oldWeather *Weather, weather *Weather) {
	if oldWeather == nil || oldWeather.GameplayCondition.ValueOrZero() != weather.GameplayCondition.ValueOrZero() ||
		oldWeather.WarnWeather.ValueOrZero() != weather.WarnWeather.ValueOrZero() {

		s2cell := s2.CellFromCellID(s2.CellID(weather.Id))
		var polygon [4][2]float64
		for i := range []int{0, 1, 2, 3} {
			vertex := s2cell.Vertex(i)
			latLng := s2.LatLngFromPoint(vertex)
			polygon[i] = [...]float64{latLng.Lat.Degrees(), latLng.Lng.Degrees()}
		}
		weatherHook := map[string]interface{}{
			"s2_cell_id":           weather.Id,
			"latitude":             weather.Latitude,
			"longitude":            weather.Longitude,
			"polygon":              polygon,
			"gameplay_condition":   weather.GameplayCondition.ValueOrZero(),
			"wind_direction":       weather.WindDirection.ValueOrZero(),
			"cloud_level":          weather.CloudLevel.ValueOrZero(),
			"rain_level":           weather.RainLevel.ValueOrZero(),
			"wind_level":           weather.WindLevel.ValueOrZero(),
			"snow_level":           weather.SnowLevel.ValueOrZero(),
			"fog_level":            weather.FogLevel.ValueOrZero(),
			"special_effect_level": weather.SpecialEffectLevel.ValueOrZero(),
			"severity":             weather.Severity.ValueOrZero(),
			"warn_weather":         weather.WarnWeather.ValueOrZero(),
			"updated":              weather.Updated,
		}
		areas := MatchStatsGeofence(weather.Latitude, weather.Longitude)
		webhooksSender.AddMessage(webhooks.Weather, weatherHook, areas)
	}
}

func saveWeatherRecord(ctx context.Context, db db.DbDetails, weather *Weather) {
	oldWeather, _ := getWeatherRecord(ctx, db, weather.Id)
	if oldWeather != nil && !hasChangesWeather(oldWeather, weather) {
		return
	}

	if oldWeather == nil {
		res, err := db.GeneralDb.NamedExecContext(ctx,
			"INSERT INTO weather ("+
				"id, latitude, longitude, level, gameplay_condition, wind_direction, cloud_level, rain_level, "+
				"wind_level, snow_level, fog_level, special_effect_level, severity, warn_weather, updated)"+
				"VALUES ("+
				":id, :latitude, :longitude, :level, :gameplay_condition, :wind_direction, :cloud_level, :rain_level, "+
				":wind_level, :snow_level, :fog_level, :special_effect_level, :severity, :warn_weather, "+
				"UNIX_TIMESTAMP())",
			weather)
		statsCollector.IncDbQuery("insert weather", err)
		if err != nil {
			log.Errorf("insert weather: %s", err)
			return
		}
		_ = res
	} else {
		res, err := db.GeneralDb.NamedExecContext(ctx, "UPDATE weather SET "+
			"latitude = :latitude, "+
			"longitude = :longitude, "+
			"level = :level, "+
			"gameplay_condition = :gameplay_condition, "+
			"wind_direction = :wind_direction, "+
			"cloud_level = :cloud_level, "+
			"rain_level = :rain_level, "+
			"wind_level = :wind_level, "+
			"snow_level = :snow_level, "+
			"fog_level = :fog_level, "+
			"special_effect_level = :special_effect_level, "+
			"severity = :severity, "+
			"warn_weather = :warn_weather, "+
			"updated = UNIX_TIMESTAMP() "+
			"WHERE id = :id",
			weather)
		statsCollector.IncDbQuery("update weather", err)
		if err != nil {
			log.Errorf("update weather: %s", err)
			return
		}
		_ = res
	}
	weatherCache.Set(weather.Id, *weather, ttlcache.DefaultTTL)
	createWeatherWebhooks(oldWeather, weather)
}
