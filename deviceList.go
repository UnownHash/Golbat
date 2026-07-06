package main

import (
	"golbat/config"
	"time"

	"golbat/cache"
)

type DeviceLocation struct {
	Latitude    float64
	Longitude   float64
	LastUpdate  int64
	ScanContext string
}

var deviceLocation *cache.OtterCache[string, DeviceLocation]

func InitDeviceCache() {
	deviceLocation = cache.NewOtterCache(cache.OtterCacheConfig[string, DeviceLocation]{
		Name:       "device_location",
		DefaultTTL: time.Hour * time.Duration(config.Config.Cleanup.DeviceHours),
		TouchOnHit: true,
	})
}

func UpdateDeviceLocation(deviceId string, lat, lon float64, scanContext string) {
	deviceLocation.Set(deviceId, DeviceLocation{
		Latitude:    lat,
		Longitude:   lon,
		LastUpdate:  time.Now().Unix(),
		ScanContext: scanContext,
	}, time.Hour*time.Duration(config.Config.Cleanup.DeviceHours))
}

type ApiDeviceLocation struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	LastUpdate  int64   `json:"last_update"`
	ScanContext string  `json:"scan_context"`
}

func GetAllDevices() map[string]ApiDeviceLocation {
	locations := map[string]ApiDeviceLocation{}
	deviceLocation.Range(func(deviceId string, loc DeviceLocation) bool {
		locations[deviceId] = ApiDeviceLocation(loc)
		return true
	})
	return locations
}
