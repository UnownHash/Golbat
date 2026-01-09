package http_handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type DeviceLocation struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	LastUpdate  int64   `json:"last_update"`
	ScanContext string  `json:"scan_context"`
}

func (h *HTTPHandler) GetAllDevices(ginContext *gin.Context) {
	devices := map[string]DeviceLocation{}
	for deviceId, location := range h.deviceTracker.IterateDevices {
		devices[deviceId] = DeviceLocation(location)
	}
	ginContext.JSON(http.StatusOK, gin.H{"devices": devices})
}
