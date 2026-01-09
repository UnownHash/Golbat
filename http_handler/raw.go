package http_handler

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"golbat/config"
)

func (h *HTTPHandler) Raw(c *gin.Context) {
	w := c.Writer
	r := c.Request

	statsCollector := h.statsCollector

	requestReceivedMs := time.Now().UnixMilli()

	authHeader := r.Header.Get("Authorization")
	if config.Config.RawBearer != "" {
		if authHeader != "Bearer "+config.Config.RawBearer {
			statsCollector.IncRawRequests("error", "auth")
			log.Errorf("Raw: Incorrect authorisation received (%s)", authHeader)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 5*1048576))
	if err != nil {
		statsCollector.IncRawRequests("error", "io_error")
		log.Errorf("Raw: Error (1) during HTTP receive %s", err)
		return
	}

	if err := r.Body.Close(); err != nil {
		statsCollector.IncRawRequests("error", "io_close_error")
		log.Errorf("Raw: Error (2) during HTTP receive %s", err)
		return
	}

	ctx := context.Background()
	if err := h.rawDecoder.DecodeRaw(ctx, r.Header, body, requestReceivedMs); err != nil {
		statsCollector.IncRawRequests("error", "decode")
		userAgent := r.Header.Get("User-Agent")
		log.Infof("Raw: Data could not be decoded. From User agent %s - Received data %s, err: %s", userAgent, body, err)
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	statsCollector.IncRawRequests("ok", "")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusCreated)
}
