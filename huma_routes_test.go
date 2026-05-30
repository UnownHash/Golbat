package main

import (
	"net/http"
	"strings"
	"testing"

	"golbat/config"

	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/gin-gonic/gin"
	gojson "github.com/goccy/go-json"
)

// emptyScanBody is a representative empty-filter scan request body. It matches
// against the empty in-memory rtree (no DB), yielding zero results.
const emptyScanBody = `{"min":{"latitude":0,"longitude":0},"max":{"latitude":1,"longitude":1},"limit":10,"filters":[]}`

// TestHumaScanEndpointsE2E exercises the full HTTP pipeline (binding, auth,
// status, serialization) for the migrated v2/v3 pokemon scan endpoints without
// a database. It guards wire-compatibility: the legacy responses had no
// `$schema` field, so Huma's schema-link transformer must stay disabled.
func TestHumaScanEndpointsE2E(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerHumaRoutes(api)

	t.Run("v2 without secret is 401", func(t *testing.T) {
		resp := api.Post("/api/pokemon/v2/scan", strings.NewReader(emptyScanBody))
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.Code)
		}
	})

	t.Run("v2 with secret returns bare array, no $schema", func(t *testing.T) {
		resp := api.Post("/api/pokemon/v2/scan", "X-Golbat-Secret: topsecret", strings.NewReader(emptyScanBody))
		if resp.Code != http.StatusAccepted {
			t.Fatalf("got %d, want 202; body=%s", resp.Code, resp.Body.String())
		}
		body := strings.TrimSpace(resp.Body.String())
		if body != "[]" {
			t.Errorf("v2 body = %q, want \"[]\"", body)
		}
		if strings.Contains(body, "$schema") {
			t.Errorf("v2 body must not contain $schema: %s", body)
		}
	})

	t.Run("v3 with secret returns envelope object, no $schema", func(t *testing.T) {
		resp := api.Post("/api/pokemon/v3/scan", "X-Golbat-Secret: topsecret", strings.NewReader(emptyScanBody))
		if resp.Code != http.StatusAccepted {
			t.Fatalf("got %d, want 202; body=%s", resp.Code, resp.Body.String())
		}
		body := resp.Body.String()

		var m map[string]any
		if err := gojson.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("v3 body is not a JSON object: %v; body=%s", err, body)
		}
		for _, key := range []string{"pokemon", "examined", "skipped", "total"} {
			if _, ok := m[key]; !ok {
				t.Errorf("v3 body missing key %q: %s", key, body)
			}
		}
		if _, ok := m["$schema"]; ok {
			t.Errorf("v3 body must not contain $schema (regression): %s", body)
		}
	})
}

// TestHumaScanAcceptsLatLonSpellings verifies the bounding box accepts both the
// canonical lat/lon (used by other Golbat endpoints) and the legacy
// latitude/longitude spelling. Before the ApiLatLon fix, lat/lon got a 422.
func TestHumaScanAcceptsLatLonSpellings(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerHumaRoutes(api)

	bodies := map[string]string{
		"lat/lon":            `{"min":{"lat":0,"lon":0},"max":{"lat":1,"lon":1},"filters":[]}`,
		"latitude/longitude": `{"min":{"latitude":0,"longitude":0},"max":{"latitude":1,"longitude":1},"filters":[]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			resp := api.Post("/api/pokemon/v2/scan", "X-Golbat-Secret: topsecret", strings.NewReader(body))
			if resp.Code != http.StatusAccepted {
				t.Errorf("%s: got %d, want 202; body=%s", name, resp.Code, resp.Body.String())
			}
		})
	}
}

// fortScanBody is an empty-filter fort scan request that matches against the
// empty in-memory rtree (no DB), yielding zero results.
const fortScanBody = `{"min":{"lat":0,"lon":0},"max":{"lat":1,"lon":1},"filters":[]}`

// TestFortScanEndpoints exercises the HTTP pipeline for the migrated fort scan
// endpoints: success (200 with the expected envelope), the FortInMemory 503
// guard, and the auth requirement (401). No database is required.
func TestFortScanEndpoints(t *testing.T) {
	prevSecret := config.Config.ApiSecret
	prevInMem := config.Config.FortInMemory
	defer func() {
		config.Config.ApiSecret = prevSecret
		config.Config.FortInMemory = prevInMem
	}()

	config.Config.ApiSecret = ""
	config.Config.FortInMemory = true

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerHumaRoutes(api)
	registerFortScanRoutes(api)

	t.Run("gym scan returns 200 envelope", func(t *testing.T) {
		resp := api.Post("/api/gym/scan", strings.NewReader(fortScanBody))
		if resp.Code != http.StatusOK {
			t.Fatalf("got %d, want 200; body=%s", resp.Code, resp.Body.String())
		}
		var m map[string]any
		if err := gojson.Unmarshal(resp.Body.Bytes(), &m); err != nil {
			t.Fatalf("body is not a JSON object: %v; body=%s", err, resp.Body.String())
		}
		for _, key := range []string{"gyms", "examined", "skipped", "total"} {
			if _, ok := m[key]; !ok {
				t.Errorf("body missing key %q: %s", key, resp.Body.String())
			}
		}
	})

	t.Run("503 when fort_in_memory disabled", func(t *testing.T) {
		config.Config.FortInMemory = false
		defer func() { config.Config.FortInMemory = true }()
		resp := api.Post("/api/gym/scan", strings.NewReader(fortScanBody))
		if resp.Code != http.StatusServiceUnavailable {
			t.Errorf("got %d, want 503; body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("401 without secret when auth configured", func(t *testing.T) {
		config.Config.ApiSecret = "secret"
		defer func() { config.Config.ApiSecret = "" }()
		resp := api.Post("/api/gym/scan", strings.NewReader(fortScanBody))
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401; body=%s", resp.Code, resp.Body.String())
		}
	})
}

// TestFortScanDraftBadge asserts the four fort scan operations carry the
// x-badges extension (draft marker) in the OpenAPI spec, while an existing
// pokemon operation does not.
func TestFortScanDraftBadge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := humagin.New(r, newHumaConfig("test"))
	registerHumaRoutes(api)
	registerFortScanRoutes(api)

	raw, err := gojson.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}

	var doc struct {
		Paths map[string]map[string]struct {
			Badges any `json:"x-badges"`
		} `json:"paths"`
	}
	if err := gojson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal openapi: %v", err)
	}

	for _, path := range []string{"/api/gym/scan", "/api/pokestop/scan", "/api/station/scan", "/api/fort/scan"} {
		op, ok := doc.Paths[path]["post"]
		if !ok {
			t.Errorf("path %q has no post operation", path)
			continue
		}
		if op.Badges == nil {
			t.Errorf("path %q post is missing x-badges (draft marker)", path)
		}
	}

	pokemonOp, ok := doc.Paths["/api/pokemon/v2/scan"]["post"]
	if !ok {
		t.Fatalf("pokemon v2 scan op not found")
	}
	if pokemonOp.Badges != nil {
		t.Errorf("pokemon v2 scan must NOT carry x-badges, got %v", pokemonOp.Badges)
	}
}
