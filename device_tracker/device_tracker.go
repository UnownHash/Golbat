package device_tracker

import (
	"context"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type DeviceTracker interface {
	// UpdateDevice location updates a device location. If any of deviceId, lat,
	// and lng are their zero values, no update will occur.
	UpdateDeviceLocation(deviceId string, lat, lon float64, scanContext string)
	// GetAllDevices returns info about all devices.
	GetAllDevices() map[string]ApiDeviceLocation
	// Run runs the automatic cleanup process, blocking until `ctx` is cancelled.
	Run(ctx context.Context)
}

type ApiDeviceLocation struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	LastUpdate  int64   `json:"last_update"`
	ScanContext string  `json:"scan_context"`
}

type DeviceLocation struct {
	Latitude    float64
	Longitude   float64
	LastUpdate  int64
	ScanContext string
}

type deviceTracker struct {
	maxDeviceTTL   time.Duration
	deviceLocation *ttlcache.Cache[string, DeviceLocation]
}

func (tracker *deviceTracker) UpdateDeviceLocation(deviceId string, lat, lon float64, scanContext string) {
	if lat == 0 || lon == 0 || deviceId == "" {
		return
	}
	tracker.deviceLocation.Set(deviceId, DeviceLocation{
		Latitude:    lat,
		Longitude:   lon,
		LastUpdate:  time.Now().Unix(),
		ScanContext: scanContext,
	}, tracker.maxDeviceTTL)
}

func (tracker *deviceTracker) GetAllDevices() map[string]ApiDeviceLocation {
	locations := map[string]ApiDeviceLocation{}
	for _, key := range tracker.deviceLocation.Items() {
		deviceLocation := key.Value()
		locations[key.Key()] = ApiDeviceLocation(deviceLocation)
	}
	return locations
}

func (tracker *deviceTracker) Run(ctx context.Context) {
	ctx, cancel_fn := context.WithCancel(ctx)
	defer cancel_fn()
	go func() {
		defer tracker.deviceLocation.Stop()
		<-ctx.Done()
	}()
	tracker.deviceLocation.Start()
}

func NewDeviceTracker(maxDeviceTTLHours int) DeviceTracker {
	maxDeviceTTL := time.Hour * time.Duration(maxDeviceTTLHours)
	tracker := &deviceTracker{
		maxDeviceTTL: maxDeviceTTL,
		deviceLocation: ttlcache.New[string, DeviceLocation](
			ttlcache.WithTTL[string, DeviceLocation](maxDeviceTTL),
		),
	}
	return tracker
}
