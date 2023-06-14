package main

import (
	"time"

	"github.com/go-co-op/gocron"
	"github.com/puzpuzpuz/xsync/v2"
	log "github.com/sirupsen/logrus"
)

type DeviceLocation struct {
	Latitude    float64
	Longitude   float64
	LastUpdate  int64
	ScanContext string
}

func init() {
	s := gocron.NewScheduler(time.UTC)
	s.Every(1).Hour().Do(clearOldDevices)
	s.StartAsync()
}

var deviceLocation = xsync.NewMapOf[DeviceLocation]()

func UpdateDeviceLocation(deviceId string, lat, lon float64, scanContext string) {
	deviceLocation.Store(deviceId, DeviceLocation{
		Latitude:    lat,
		Longitude:   lon,
		LastUpdate:  time.Now().Unix(),
		ScanContext: scanContext,
	})
}

type ApiDeviceLocation struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	LastUpdate  int64   `json:"last_update"`
	ScanContext string  `json:"scan_context"`
}

func GetAllDevices() map[string]ApiDeviceLocation {
	locations := map[string]ApiDeviceLocation{}
	deviceLocation.Range(func(key string, value DeviceLocation) bool {
		locations[key] = ApiDeviceLocation{
			Latitude:    value.Latitude,
			Longitude:   value.Longitude,
			LastUpdate:  value.LastUpdate,
			ScanContext: value.ScanContext,
		}
		return true
	})
	return locations
}

func clearOldDevices() {
	log.Infof("[DEVICES] Clearing devices not seen in the last 24hr, current count: %d", deviceLocation.Size())
	deviceLocation.Range(func(key string, value DeviceLocation) bool {
		if time.Now().Unix()-value.LastUpdate > 60*60*24 {
			deviceLocation.Delete(key)
		}
		return true
	})
	log.Infof("[DEVICES] Cleared old devices, new count: %d", deviceLocation.Size())
}
