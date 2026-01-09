package device_tracker

import (
	"context"
	"golbat/geo"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type DeviceLocation struct {
	Latitude    float64
	Longitude   float64
	LastUpdate  int64
	ScanContext string
}

type DeviceTracker struct {
	maxDeviceTTL   time.Duration
	deviceLocation *ttlcache.Cache[string, DeviceLocation]
}

func (tracker *DeviceTracker) UpdateDeviceLocation(deviceId string, location geo.Location, scanContext string) {
	if location.IsZero() || deviceId == "" {
		return
	}
	tracker.deviceLocation.Set(deviceId, DeviceLocation{
		Latitude:    location.Latitude,
		Longitude:   location.Longitude,
		LastUpdate:  time.Now().Unix(),
		ScanContext: scanContext,
	}, tracker.maxDeviceTTL)
}

func (tracker *DeviceTracker) IterateDevices(yield func(string, DeviceLocation) bool) {
	for _, key := range tracker.deviceLocation.Items() {
		if !yield(key.Key(), key.Value()) {
			return
		}
	}
}

func (tracker *DeviceTracker) Run(ctx context.Context) {
	ctx, cancelFn := context.WithCancel(ctx)
	defer cancelFn()
	go func() {
		defer tracker.deviceLocation.Stop()
		<-ctx.Done()
	}()
	tracker.deviceLocation.Start()
}

func NewDeviceTracker(maxDeviceTTLHours int) *DeviceTracker {
	maxDeviceTTL := time.Hour * time.Duration(maxDeviceTTLHours)
	tracker := &DeviceTracker{
		maxDeviceTTL: maxDeviceTTL,
		deviceLocation: ttlcache.New[string, DeviceLocation](
			ttlcache.WithTTL[string, DeviceLocation](maxDeviceTTL),
		),
	}
	return tracker
}
