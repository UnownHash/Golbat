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
// REMINDER! Dirty flag pattern - use setter methods to modify fields
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
	UpdatedMs          int64     `db:"updated"`

	dirty     bool `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-" json:"-"` // Not persisted - tracks if this is a new record

	oldValues WeatherOldValues `db:"-" json:"-"` // Old values for webhook comparison
}

// WeatherOldValues holds old field values for webhook comparison
type WeatherOldValues struct {
	GameplayCondition null.Int
	WarnWeather       null.Bool
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

// IsDirty returns true if any field has been modified
func (weather *Weather) IsDirty() bool {
	return weather.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (weather *Weather) ClearDirty() {
	weather.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (weather *Weather) IsNewRecord() bool {
	return weather.newRecord
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (weather *Weather) snapshotOldValues() {
	weather.oldValues = WeatherOldValues{
		GameplayCondition: weather.GameplayCondition,
		WarnWeather:       weather.WarnWeather,
	}
}

// --- Set methods with dirty tracking ---

func (weather *Weather) SetId(v int64) {
	if weather.Id != v {
		weather.Id = v
		weather.dirty = true
	}
}

func (weather *Weather) SetLatitude(v float64) {
	if !floatAlmostEqual(weather.Latitude, v, floatTolerance) {
		weather.Latitude = v
		weather.dirty = true
	}
}

func (weather *Weather) SetLongitude(v float64) {
	if !floatAlmostEqual(weather.Longitude, v, floatTolerance) {
		weather.Longitude = v
		weather.dirty = true
	}
}

func (weather *Weather) SetLevel(v null.Int) {
	if weather.Level != v {
		weather.Level = v
		weather.dirty = true
	}
}

func (weather *Weather) SetGameplayCondition(v null.Int) {
	if weather.GameplayCondition != v {
		weather.GameplayCondition = v
		weather.dirty = true
	}
}

func (weather *Weather) SetWindDirection(v null.Int) {
	if weather.WindDirection != v {
		weather.WindDirection = v
		weather.dirty = true
	}
}

func (weather *Weather) SetCloudLevel(v null.Int) {
	if weather.CloudLevel != v {
		weather.CloudLevel = v
		weather.dirty = true
	}
}

func (weather *Weather) SetRainLevel(v null.Int) {
	if weather.RainLevel != v {
		weather.RainLevel = v
		weather.dirty = true
	}
}

func (weather *Weather) SetWindLevel(v null.Int) {
	if weather.WindLevel != v {
		weather.WindLevel = v
		weather.dirty = true
	}
}

func (weather *Weather) SetSnowLevel(v null.Int) {
	if weather.SnowLevel != v {
		weather.SnowLevel = v
		weather.dirty = true
	}
}

func (weather *Weather) SetFogLevel(v null.Int) {
	if weather.FogLevel != v {
		weather.FogLevel = v
		weather.dirty = true
	}
}

func (weather *Weather) SetSpecialEffectLevel(v null.Int) {
	if weather.SpecialEffectLevel != v {
		weather.SpecialEffectLevel = v
		weather.dirty = true
	}
}

func (weather *Weather) SetSeverity(v null.Int) {
	if weather.Severity != v {
		weather.Severity = v
		weather.dirty = true
	}
}

func (weather *Weather) SetWarnWeather(v null.Bool) {
	if weather.WarnWeather != v {
		weather.WarnWeather = v
		weather.dirty = true
	}
}

func getWeatherRecord(ctx context.Context, db db.DbDetails, weatherId int64) (*Weather, error) {
	inMemoryWeather := weatherCache.Get(weatherId)
	if inMemoryWeather != nil {
		weather := inMemoryWeather.Value()
		weather.snapshotOldValues()
		return weather, nil
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

	weather.UpdatedMs *= 1000
	weather.snapshotOldValues()
	return &weather, nil
}

func weatherCellIdFromLatLon(lat, lon float64) int64 {
	return int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lon)).Parent(10))
}

func (weather *Weather) updateWeatherFromClientWeatherProto(clientWeather *pogo.ClientWeatherProto) {
	weather.SetId(clientWeather.S2CellId)
	s2cell := s2.CellFromCellID(s2.CellID(clientWeather.S2CellId))
	weather.SetLatitude(s2cell.CapBound().RectBound().Center().Lat.Degrees())
	weather.SetLongitude(s2cell.CapBound().RectBound().Center().Lng.Degrees())
	weather.SetLevel(null.IntFrom(int64(s2cell.Level())))
	weather.SetGameplayCondition(null.IntFrom(int64(clientWeather.GameplayWeather.GameplayCondition)))
	weather.SetWindDirection(null.IntFrom(int64(clientWeather.DisplayWeather.WindDirection)))
	weather.SetCloudLevel(null.IntFrom(int64(clientWeather.DisplayWeather.CloudLevel)))
	weather.SetRainLevel(null.IntFrom(int64(clientWeather.DisplayWeather.RainLevel)))
	weather.SetWindLevel(null.IntFrom(int64(clientWeather.DisplayWeather.WindLevel)))
	weather.SetSnowLevel(null.IntFrom(int64(clientWeather.DisplayWeather.SnowLevel)))
	weather.SetFogLevel(null.IntFrom(int64(clientWeather.DisplayWeather.FogLevel)))
	weather.SetSpecialEffectLevel(null.IntFrom(int64(clientWeather.DisplayWeather.SpecialEffectLevel)))
	for _, alert := range clientWeather.Alerts {
		weather.SetSeverity(null.IntFrom(int64(alert.Severity)))
		weather.SetWarnWeather(null.BoolFrom(alert.WarnWeather))
	}
}

type WeatherWebhook struct {
	S2CellId           int64         `json:"s2_cell_id"`
	Latitude           float64       `json:"latitude"`
	Longitude          float64       `json:"longitude"`
	Polygon            [4][2]float64 `json:"polygon"`
	GameplayCondition  int64         `json:"gameplay_condition"`
	WindDirection      int64         `json:"wind_direction"`
	CloudLevel         int64         `json:"cloud_level"`
	RainLevel          int64         `json:"rain_level"`
	WindLevel          int64         `json:"wind_level"`
	SnowLevel          int64         `json:"snow_level"`
	FogLevel           int64         `json:"fog_level"`
	SpecialEffectLevel int64         `json:"special_effect_level"`
	Severity           int64         `json:"severity"`
	WarnWeather        bool          `json:"warn_weather"`
	Updated            int64         `json:"updated"`
}

func createWeatherWebhooks(weather *Weather) {
	old := &weather.oldValues
	isNew := weather.IsNewRecord()

	if isNew || old.GameplayCondition.ValueOrZero() != weather.GameplayCondition.ValueOrZero() ||
		old.WarnWeather.ValueOrZero() != weather.WarnWeather.ValueOrZero() {

		s2cell := s2.CellFromCellID(s2.CellID(weather.Id))
		var polygon [4][2]float64
		for i := range []int{0, 1, 2, 3} {
			vertex := s2cell.Vertex(i)
			latLng := s2.LatLngFromPoint(vertex)
			polygon[i] = [...]float64{latLng.Lat.Degrees(), latLng.Lng.Degrees()}
		}

		weatherHook := WeatherWebhook{
			S2CellId:           weather.Id,
			Latitude:           weather.Latitude,
			Longitude:          weather.Longitude,
			Polygon:            polygon,
			GameplayCondition:  weather.GameplayCondition.ValueOrZero(),
			WindDirection:      weather.WindDirection.ValueOrZero(),
			CloudLevel:         weather.CloudLevel.ValueOrZero(),
			RainLevel:          weather.RainLevel.ValueOrZero(),
			WindLevel:          weather.WindLevel.ValueOrZero(),
			SnowLevel:          weather.SnowLevel.ValueOrZero(),
			FogLevel:           weather.FogLevel.ValueOrZero(),
			SpecialEffectLevel: weather.SpecialEffectLevel.ValueOrZero(),
			Severity:           weather.Severity.ValueOrZero(),
			WarnWeather:        weather.WarnWeather.ValueOrZero(),
			Updated:            weather.UpdatedMs / 1000,
		}
		areas := MatchStatsGeofence(weather.Latitude, weather.Longitude)
		webhooksSender.AddMessage(webhooks.Weather, weatherHook, areas)
	}
}

func saveWeatherRecord(ctx context.Context, db db.DbDetails, weather *Weather) {
	// Skip save if not dirty and not new
	if !weather.IsDirty() && !weather.IsNewRecord() {
		return
	}

	if weather.IsNewRecord() {
		res, err := db.GeneralDb.NamedExecContext(ctx,
			"INSERT INTO weather ("+
				"id, latitude, longitude, level, gameplay_condition, wind_direction, cloud_level, rain_level, "+
				"wind_level, snow_level, fog_level, special_effect_level, severity, warn_weather, updated)"+
				"VALUES ("+
				":id, :latitude, :longitude, :level, :gameplay_condition, :wind_direction, :cloud_level, :rain_level, "+
				":wind_level, :snow_level, :fog_level, :special_effect_level, :severity, :warn_weather, "+
				":updated/1000)",
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
			"updated = :updated/1000 "+
			"WHERE id = :id",
			weather)
		statsCollector.IncDbQuery("update weather", err)
		if err != nil {
			log.Errorf("update weather: %s", err)
			return
		}
		_ = res
	}
	createWeatherWebhooks(weather)
	weather.ClearDirty()
	if weather.IsNewRecord() {
		weatherCache.Set(weather.Id, weather, ttlcache.DefaultTTL)
		weather.newRecord = false
	}
}
