package main

import (
	"golbat/config"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type DeviceLocation struct {
	Latitude    float64
	Longitude   float64
	LastUpdate  int64
	ScanContext string
}

var deviceLocation *ttlcache.Cache[string, DeviceLocation]

func InitDeviceCache() {
	deviceLocation = ttlcache.New[string, DeviceLocation](
		ttlcache.WithTTL[string, DeviceLocation](time.Hour * time.Duration(config.Config.Cleanup.DeviceHours)),
	)
	go deviceLocation.Start()
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
	for _, key := range deviceLocation.Items() {
		deviceLocation := key.Value()
		locations[key.Key()] = ApiDeviceLocation{
			Latitude:    deviceLocation.Latitude,
			Longitude:   deviceLocation.Longitude,
			LastUpdate:  deviceLocation.LastUpdate,
			ScanContext: deviceLocation.ScanContext,
		}
	}
	return locations
}
