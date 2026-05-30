package main

import (
	"net/http"
	"strings"
	"testing"

	"golbat/config"

	"github.com/danielgtaylor/huma/v2/humatest"
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
