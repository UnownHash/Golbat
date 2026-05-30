package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golbat/config"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func TestHumaScanRequestLogging(t *testing.T) {
	prevLogging := config.Config.Logging.ApiRequestLogging
	prevSecret := config.Config.ApiSecret
	config.Config.Logging.ApiRequestLogging = true
	config.Config.ApiSecret = "" // disable auth so the request reaches the handler
	defer func() {
		config.Config.Logging.ApiRequestLogging = prevLogging
		config.Config.ApiSecret = prevSecret
	}()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := setupHumaAPI(r)
	registerHumaRoutes(api)

	// A deliberately bad body (lat/lon ok, but an unknown field) to confirm we log
	// the rejected payload AND the 422 reason that the handler never sees.
	body := `{"min":{"lat":1.25,"lon":2.5},"max":{"lat":3,"lon":4},"filters":[],"bogus":1}`
	req := httptest.NewRequest("POST", "/api/pokemon/v2/scan", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	logged := buf.String()
	for _, want := range []string{
		"[huma scan]",
		"/api/pokemon/v2/scan",
		"1.25",                // request body coordinate captured (logrus escapes quotes)
		"bogus",               // the offending field is visible in the request
		"422",                 // status captured
		"unexpected property", // Huma's rejection reason captured from the response
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log missing %q.\n--- log ---\n%s", want, logged)
		}
	}
}

func TestHumaScanRequestLoggingSilentWhenDisabled(t *testing.T) {
	prevLogging := config.Config.Logging.ApiRequestLogging
	config.Config.Logging.ApiRequestLogging = false
	defer func() { config.Config.Logging.ApiRequestLogging = prevLogging }()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := setupHumaAPI(r)
	registerHumaRoutes(api)

	req := httptest.NewRequest("POST", "/api/pokemon/v2/scan",
		strings.NewReader(`{"min":{"lat":0,"lon":0},"max":{"lat":1,"lon":1},"filters":[]}`))
	r.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(buf.String(), "[huma scan]") {
		t.Errorf("expected no scan log when debug off, got: %s", buf.String())
	}
}
