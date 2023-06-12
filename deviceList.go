package main

import (
	"github.com/puzpuzpuz/xsync/v2"
	"time"
)

type DeviceLocation struct {
	Latitude    float64
	Longitude   float64
	LastUpdate  int64
	ScanContext string
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
