package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jmoiron/sqlx"

	"golbat/config"
	db2 "golbat/db"
)

const squareFenceBody = `{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[10,20],[12,20],[12,24],[10,24],[10,20]]]}}`

func newFenceTestApi(t *testing.T) humatest.TestAPI {
	t.Helper()
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = ""
	t.Cleanup(func() { config.Config.ApiSecret = prev })

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerTier3Routes(api)
	registerTier4Routes(api)
	return api
}

// TestClearQuestsReportsBackendFailure: when the id query fails, clear-quests
// must answer with an error, not 202 {status: ok}. The DSN points at a port
// nothing listens on, so the first query fails with connection refused.
func TestClearQuestsReportsBackendFailure(t *testing.T) {
	sdb, err := sqlx.Open("mysql", "u:p@tcp(127.0.0.1:1)/x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	prev := dbDetails
	dbDetails = db2.DbDetails{GeneralDb: sdb, PokemonDb: sdb}
	t.Cleanup(func() { dbDetails = prev })

	api := newFenceTestApi(t)
	resp := api.Post("/api/clear-quests", strings.NewReader(squareFenceBody))
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500; body=%s", resp.Code, resp.Body.String())
	}
}

// TestFenceEndpointsRejectNilGeometry: a Feature with a null geometry must be
// a 400 at the parse boundary on every fence endpoint, never a panic.
func TestFenceEndpointsRejectNilGeometry(t *testing.T) {
	api := newFenceTestApi(t)
	const body = `{"type":"Feature","geometry":null,"properties":{}}`
	for _, path := range []string{"/api/pokestop-positions", "/api/quest-status", "/api/clear-quests"} {
		t.Run(path, func(t *testing.T) {
			resp := api.Post(path, strings.NewReader(body))
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400; body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

// TestFenceEndpointsRejectNonAreaGeometry: a Point body is not a fence and
// must be a 400 on every fence endpoint rather than an empty success.
func TestFenceEndpointsRejectNonAreaGeometry(t *testing.T) {
	api := newFenceTestApi(t)
	const body = `{"type":"Point","coordinates":[11,22]}`
	for _, path := range []string{"/api/pokestop-positions", "/api/quest-status", "/api/clear-quests"} {
		t.Run(path, func(t *testing.T) {
			resp := api.Post(path, strings.NewReader(body))
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400; body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}
