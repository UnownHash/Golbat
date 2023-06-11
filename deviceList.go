package main

import (
	"github.com/puzpuzpuz/xsync/v2"
	"time"
)

type DeviceLocation struct {
	Latitude   float64
	Longitude  float64
	LastUpdate int64
}

var deviceLocation = xsync.NewMapOf[DeviceLocation]()

func UpdateDeviceLocation(deviceId string, lat, lon float64) {
	deviceLocation.Store(deviceId, DeviceLocation{
		Latitude:   lat,
		Longitude:  lon,
		LastUpdate: time.Now().Unix(),
	})
}

func GetAllDevices() map[string]DeviceLocation {
	locations := map[string]DeviceLocation{}
	deviceLocation.Range(func(key string, value DeviceLocation) bool {
		locations[key] = value
		return true
	})
	return locations
}
