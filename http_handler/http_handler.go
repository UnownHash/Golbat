package http_handler

import (
	"golbat/db"
	"golbat/device_tracker"
	"golbat/raw_decoder/http_raw_decoder"
	"golbat/stats_collector"
)

type HTTPHandler struct {
	rawDecoder     *http_raw_decoder.HTTPRawDecoder
	dbDetails      db.DbDetails
	statsCollector stats_collector.StatsCollector
	deviceTracker  *device_tracker.DeviceTracker
}

func NewHTTPHandler(rawDecoder *http_raw_decoder.HTTPRawDecoder, dbDetails db.DbDetails, statsCollector stats_collector.StatsCollector, deviceTracker *device_tracker.DeviceTracker) *HTTPHandler {
	return &HTTPHandler{
		rawDecoder:     rawDecoder,
		dbDetails:      dbDetails,
		statsCollector: statsCollector,
		deviceTracker:  deviceTracker,
	}
}
