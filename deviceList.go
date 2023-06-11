package main

import "github.com/puzpuzpuz/xsync/v2"

type DeviceLocation struct {
	Latitude  float64
	Longitude float64
}

var deviceLocation = xsync.NewMapOf[DeviceLocation]()

func UpdateDeviceLocation(deviceId string, lat, lon float64) {
	deviceLocation.Store(deviceId, DeviceLocation{
		Latitude:  lat,
		Longitude: lon,
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
