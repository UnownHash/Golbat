package main

import (
	"net/http"
	"strconv"
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

// TestHumaScanAcceptsPartialRanges verifies a DNF range with only one bound
// passes schema validation, as the legacy gin BindJSON did (the missing bound
// binds to 0).
func TestHumaScanAcceptsPartialRanges(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = ""
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerHumaRoutes(api)

	body := `{"min":{"lat":0,"lon":0},"max":{"lat":1,"lon":1},"filters":[{"cp":{"min":100,"max":5000},"iv":{"min":90}}]}`
	resp := api.Post("/api/pokemon/v2/scan", strings.NewReader(body))
	if resp.Code != http.StatusAccepted {
		t.Errorf("got %d, want 202; body=%s", resp.Code, resp.Body.String())
	}
}

// TestPokemonReadEndpoints exercises the migrated pokemon search and by-id read
// endpoints over the HTTP pipeline without a database: get-pokemon for an absent
// id is 404, and search-pokemon returns a 202 bare JSON array (empty against the
// empty in-memory rtree).
func TestPokemonReadEndpoints(t *testing.T) {
	prev := config.Config.ApiSecret
	prevDist := config.Config.Tuning.MaxPokemonDistance
	config.Config.ApiSecret = ""
	// The 1x1 degree bounding box spans ~157km; allow it through the distance guard.
	config.Config.Tuning.MaxPokemonDistance = 100000
	defer func() {
		config.Config.ApiSecret = prev
		config.Config.Tuning.MaxPokemonDistance = prevDist
	}()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerPokemonReadRoutes(api)

	t.Run("get-pokemon for unknown id is 404", func(t *testing.T) {
		resp := api.Get("/api/pokemon/id/123456789")
		if resp.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404; body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("search-pokemon center-only body passes validation", func(t *testing.T) {
		// Legacy mode: min/max omitted, results ordered by distance from center
		// within a small default radius.
		body := `{"center":{"lat":0,"lon":0},"searchIds":[25]}`
		resp := api.Post("/api/pokemon/search", strings.NewReader(body))
		if resp.Code != http.StatusAccepted {
			t.Errorf("got %d, want 202; body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("search-pokemon returns 202 bare array", func(t *testing.T) {
		body := `{"min":{"lat":0,"lon":0},"max":{"lat":1,"lon":1},"searchIds":[25]}`
		resp := api.Post("/api/pokemon/search", strings.NewReader(body))
		if resp.Code != http.StatusAccepted {
			t.Fatalf("got %d, want 202; body=%s", resp.Code, resp.Body.String())
		}
		var arr []any
		if err := gojson.Unmarshal(resp.Body.Bytes(), &arr); err != nil {
			t.Fatalf("body is not a JSON array: %v; body=%s", err, resp.Body.String())
		}
	})

	t.Run("available-pokemon returns 202", func(t *testing.T) {
		resp := api.Get("/api/pokemon/available")
		if resp.Code != http.StatusAccepted {
			t.Fatalf("got %d, want 202; body=%s", resp.Code, resp.Body.String())
		}
	})
}

// TestTier3ReadEndpoints exercises the migrated tier-3 read endpoints over the
// HTTP pipeline without a database: gym/query with an empty ids list returns a
// 200 empty array, and an oversized ids list returns 413. (gym/id 404 needs a
// DB fallback so it is covered by the registration smoke test instead.)
func TestTier3ReadEndpoints(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = ""
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerTier3Routes(api)

	t.Run("gym/query with empty ids returns 200 empty array", func(t *testing.T) {
		resp := api.Post("/api/gym/query", strings.NewReader(`{"ids":[]}`))
		if resp.Code != http.StatusOK {
			t.Fatalf("got %d, want 200; body=%s", resp.Code, resp.Body.String())
		}
		body := strings.TrimSpace(resp.Body.String())
		if body != "[]" {
			t.Errorf("body = %q, want \"[]\"", body)
		}
	})

	t.Run("pokestop/id for unknown id is 404", func(t *testing.T) {
		// PeekPokestopRecord is cache-only (no DB fallback), so a missing id is
		// a clean 404 with no database.
		resp := api.Get("/api/pokestop/id/does-not-exist")
		if resp.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404; body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("tappable/id for unknown id is 404", func(t *testing.T) {
		// PeekTappableRecord is cache-only, so a missing id is a clean 404.
		resp := api.Get("/api/tappable/id/123456789")
		if resp.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404; body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("gym/query accepts an empty object body", func(t *testing.T) {
		resp := api.Post("/api/gym/query", strings.NewReader(`{}`))
		if resp.Code != http.StatusOK {
			t.Fatalf("got %d, want 200; body=%s", resp.Code, resp.Body.String())
		}
		if body := strings.TrimSpace(resp.Body.String()); body != "[]" {
			t.Errorf("body = %q, want \"[]\"", body)
		}
	})

	t.Run("gym/search with no filters is a 400, not a schema 422", func(t *testing.T) {
		resp := api.Post("/api/gym/search", strings.NewReader(`{}`))
		if resp.Code != http.StatusBadRequest {
			t.Errorf("got %d, want 400; body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("gym/query rejecting >500 ids returns 413", func(t *testing.T) {
		ids := make([]string, 0, 501)
		for i := 0; i < 501; i++ {
			ids = append(ids, "id"+strconv.Itoa(i))
		}
		raw, _ := gojson.Marshal(map[string][]string{"ids": ids})
		resp := api.Post("/api/gym/query", strings.NewReader(string(raw)))
		if resp.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("got %d, want 413; body=%s", resp.Code, resp.Body.String())
		}
	})
}

// TestTier3RoutesRegisterInSpec asserts all eight tier-3 operations appear in
// the OpenAPI spec at their expected method+path (registration smoke test for
// the endpoints that need a DB and so are not exercised end-to-end here).
func TestTier3RoutesRegisterInSpec(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := humagin.New(r, newHumaConfig("test"))
	registerTier3Routes(api)

	raw, err := gojson.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := gojson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal openapi: %v", err)
	}

	want := []struct{ method, path string }{
		{"post", "/api/gym/query"},
		{"post", "/api/station/query"},
		{"post", "/api/gym/search"},
		{"get", "/api/gym/id/{gym_id}"},
		{"get", "/api/station/id/{station_id}"},
		{"get", "/api/pokestop/id/{fort_id}"},
		{"get", "/api/tappable/id/{tappable_id}"},
		{"post", "/api/pokestop-positions"},
	}
	for _, w := range want {
		if _, ok := doc.Paths[w.path][w.method]; !ok {
			t.Errorf("missing %s %s in OpenAPI spec", w.method, w.path)
		}
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

// TestTier4RoutesRegisterInSpec asserts the two geofence-body quest operations
// register in the OpenAPI spec at their expected method+path. These hit the DB
// so they are not exercised end-to-end here.
func TestTier4RoutesRegisterInSpec(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := humagin.New(r, newHumaConfig("test"))
	registerTier4Routes(api)

	raw, err := gojson.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := gojson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal openapi: %v", err)
	}

	want := []struct{ method, path string }{
		{"post", "/api/quest-status"},
		{"post", "/api/clear-quests"},
	}
	for _, w := range want {
		if _, ok := doc.Paths[w.path][w.method]; !ok {
			t.Errorf("missing %s %s in OpenAPI spec", w.method, w.path)
		}
	}
}

// TestQuestStatusMalformedFenceIs400 verifies that quest-status rejects a
// malformed geofence body with 400 before reaching the database:
// NormaliseFenceFromBytes fails to parse garbage bytes, so no DB call occurs.
func TestQuestStatusMalformedFenceIs400(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = ""
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerTier4Routes(api)

	resp := api.Post("/api/quest-status", strings.NewReader(`{"bad":`))
	if resp.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
}

// TestTier4OperationalEndpoints exercises the migrated tier-4 operational
// endpoints over the HTTP pipeline without a database: devices/all, reload
// -geojson, skip-preserve-pokemon, and the nil-FortTracker 503 guard.
func TestTier4OperationalEndpoints(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = ""
	defer func() { config.Config.ApiSecret = prev }()

	// GetAllDevices() reads from the global device cache; initialise it (no DB
	// required) so devices/all does not nil-deref.
	if config.Config.Cleanup.DeviceHours == 0 {
		config.Config.Cleanup.DeviceHours = 1
	}
	InitDeviceCache()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerTier4Routes(api)

	t.Run("devices/all returns 200 with devices object", func(t *testing.T) {
		resp := api.Get("/api/devices/all")
		if resp.Code != http.StatusOK {
			t.Fatalf("got %d, want 200; body=%s", resp.Code, resp.Body.String())
		}
		var m map[string]any
		if err := gojson.Unmarshal(resp.Body.Bytes(), &m); err != nil {
			t.Fatalf("body is not a JSON object: %v; body=%s", err, resp.Body.String())
		}
		if _, ok := m["devices"]; !ok {
			t.Errorf("body missing key %q: %s", "devices", resp.Body.String())
		}
	})

	t.Run("reload-geojson POST returns 202 status ok", func(t *testing.T) {
		resp := api.Post("/api/reload-geojson", strings.NewReader(""))
		if resp.Code != http.StatusAccepted {
			t.Fatalf("got %d, want 202; body=%s", resp.Code, resp.Body.String())
		}
		var m map[string]any
		if err := gojson.Unmarshal(resp.Body.Bytes(), &m); err != nil {
			t.Fatalf("body is not a JSON object: %v; body=%s", err, resp.Body.String())
		}
		if m["status"] != "ok" {
			t.Errorf("status = %v, want \"ok\"; body=%s", m["status"], resp.Body.String())
		}
	})

	t.Run("skip-preserve-pokemon GET returns 200", func(t *testing.T) {
		resp := api.Get("/api/skip-preserve-pokemon")
		if resp.Code != http.StatusOK {
			t.Fatalf("got %d, want 200; body=%s", resp.Code, resp.Body.String())
		}
		var m map[string]any
		if err := gojson.Unmarshal(resp.Body.Bytes(), &m); err != nil {
			t.Fatalf("body is not a JSON object: %v; body=%s", err, resp.Body.String())
		}
		if m["status"] != "ok" {
			t.Errorf("status = %v, want \"ok\"; body=%s", m["status"], resp.Body.String())
		}
	})

	t.Run("fort-tracker/cell with nil tracker returns 503", func(t *testing.T) {
		// GetFortTracker() is nil in this DB-free test (no Preload), so the
		// handler short-circuits to 503 before any cell lookup.
		resp := api.Get("/api/fort-tracker/cell/1234567890")
		if resp.Code != http.StatusServiceUnavailable {
			t.Errorf("got %d, want 503; body=%s", resp.Code, resp.Body.String())
		}
	})
}

// TestHumaStationByIdRoute verifies the station by-id route is registered and
// requires the secret; the 404-for-unknown-id path needs a DB, so it is covered
// by the registration smoke test (TestTier3RoutesRegisterInSpec) instead.
func TestHumaStationByIdRoute(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerTier3Routes(api)

	t.Run("no secret is 401", func(t *testing.T) {
		resp := api.Get("/api/station/id/does-not-exist")
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.Code)
		}
	})
}

func TestHumaGymAvailableRoute(t *testing.T) {
	prevSecret := config.Config.ApiSecret
	prevFim := config.Config.FortInMemory
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prevSecret; config.Config.FortInMemory = prevFim }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerFortScanRoutes(api)

	config.Config.FortInMemory = false
	if resp := api.Get("/api/gym/available", "X-Golbat-Secret: topsecret"); resp.Code != http.StatusServiceUnavailable {
		t.Errorf("fim off: got %d, want 503", resp.Code)
	}
	config.Config.FortInMemory = true
	resp := api.Get("/api/gym/available", "X-Golbat-Secret: topsecret")
	if resp.Code != http.StatusOK {
		t.Fatalf("fim on: got %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"teams":[]`) {
		t.Errorf("body missing \"teams\": %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"raids":[]`) {
		t.Errorf("body missing \"raids\": %s", resp.Body.String())
	}
}

func TestHumaStationAvailableRoute(t *testing.T) {
	prevSecret := config.Config.ApiSecret
	prevFim := config.Config.FortInMemory
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prevSecret; config.Config.FortInMemory = prevFim }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerFortScanRoutes(api)

	config.Config.FortInMemory = false
	if resp := api.Get("/api/station/available", "X-Golbat-Secret: topsecret"); resp.Code != http.StatusServiceUnavailable {
		t.Errorf("fim off: got %d, want 503", resp.Code)
	}
	config.Config.FortInMemory = true
	resp := api.Get("/api/station/available", "X-Golbat-Secret: topsecret")
	if resp.Code != http.StatusOK {
		t.Fatalf("fim on: got %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"battles":[]`) {
		t.Errorf("body missing empty battles array: %s", resp.Body.String())
	}
}
