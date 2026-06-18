package main

import (
	"bytes"
	"io"
	"strings"

	"golbat/config"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// humaLogMaxBody caps how much of each body is written to the log.
const humaLogMaxBody = 4096

// humaApiRequestLogger logs the raw request body and the response (status + body)
// for every Huma-served /api request, but only when logging.api_request_logging is
// enabled. It is off by default and independent of logging.debug because these
// bodies can be very large. This is a debugging aid for callers whose requests are
// rejected: Huma validates the body against the OpenAPI schema and returns 422
// *before* the operation handler runs, so the handler never sees a rejected payload
// — only this transport-level hook does. The 422 response body itself lists exactly
// which properties were unexpected or missing.
func humaApiRequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.Config.Logging.ApiRequestLogging || !isHumaApiPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		// Read the body for logging, then restore it in full for Huma to parse.
		var reqBody []byte
		if c.Request.Body != nil {
			reqBody, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewReader(reqBody))
		}

		rec := &humaResponseRecorder{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = rec

		c.Next()

		log.Infof("[huma api] %s %s -> %d\n  request:  %s\n  response: %s",
			c.Request.Method, c.Request.URL.Path, rec.Status(),
			truncateForLog(reqBody), truncateForLog(rec.body.Bytes()))
	}
}

// isHumaApiPath matches every /api/ request. The logging middleware is installed
// only ahead of the Huma routes (in setupHumaAPI), so in practice this captures
// the Huma-served operations and not the few remaining gin /api routes, nor the
// /docs and /openapi.json doc routes (which are not under /api).
func isHumaApiPath(p string) bool {
	return strings.HasPrefix(p, "/api/")
}

func truncateForLog(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > humaLogMaxBody {
		return s[:humaLogMaxBody] + "…(truncated)"
	}
	return s
}

// humaResponseRecorder tees the response body into a buffer while still writing
// it to the client, so the logger can report what was sent (including 422s).
type humaResponseRecorder struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *humaResponseRecorder) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *humaResponseRecorder) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}
