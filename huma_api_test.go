package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"golbat/config"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/gin-gonic/gin"
	gojson "github.com/goccy/go-json"
)

func TestHumaConfigUsesGoccy(t *testing.T) {
	cfg := newHumaConfig("test")
	f, ok := cfg.Formats["application/json"]
	if !ok || f.Marshal == nil {
		t.Fatal("application/json format not configured")
	}
	var buf bytes.Buffer
	if err := f.Marshal(&buf, map[string]int{"a": 1}); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want, _ := gojson.Marshal(map[string]int{"a": 1})
	if got := bytes.TrimSpace(buf.Bytes()); !bytes.Equal(got, want) {
		t.Errorf("configured marshaler output = %s, want %s", got, want)
	}
}

func TestHumaConfigDeclaresSecurityScheme(t *testing.T) {
	cfg := newHumaConfig("test")
	if cfg.Components == nil || cfg.Components.SecuritySchemes == nil {
		t.Fatal("no security schemes configured")
	}
	scheme, ok := cfg.Components.SecuritySchemes["golbatSecret"]
	if !ok {
		t.Fatal("golbatSecret scheme missing")
	}
	if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != "X-Golbat-Secret" {
		t.Errorf("unexpected scheme: %+v", scheme)
	}
}

// registerSecretTestOps registers a secured /secure operation (declaring the
// golbatSecret security requirement) and an unsecured /open operation on api.
func registerSecretTestOps(api huma.API) {
	type emptyOut struct {
		Body struct{}
	}
	handler := func(ctx context.Context, _ *struct{}) (*emptyOut, error) {
		return &emptyOut{}, nil
	}
	huma.Register(api, huma.Operation{
		OperationID: "secure",
		Method:      http.MethodGet,
		Path:        "/secure",
		Security:    []map[string][]string{{securitySchemeName: {}}},
	}, handler)
	huma.Register(api, huma.Operation{
		OperationID: "open",
		Method:      http.MethodGet,
		Path:        "/open",
	}, handler)
}

func TestHumaSecretMiddleware(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerSecretTestOps(api)

	t.Run("secure without header is 401", func(t *testing.T) {
		resp := api.Get("/secure")
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.Code)
		}
	})

	t.Run("secure with wrong header is 401", func(t *testing.T) {
		resp := api.Get("/secure", "X-Golbat-Secret: wrong")
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.Code)
		}
	})

	t.Run("secure with correct header is 200", func(t *testing.T) {
		resp := api.Get("/secure", "X-Golbat-Secret: topsecret")
		if resp.Code != http.StatusOK {
			t.Errorf("got %d, want 200", resp.Code)
		}
	})

	t.Run("open without header is 200", func(t *testing.T) {
		resp := api.Get("/open")
		if resp.Code != http.StatusOK {
			t.Errorf("got %d, want 200", resp.Code)
		}
	})
}

func TestHumaSecretMiddlewareDisabledWhenSecretEmpty(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = ""
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerSecretTestOps(api)

	resp := api.Get("/secure")
	if resp.Code != http.StatusOK {
		t.Errorf("auth-disabled: got %d, want 200", resp.Code)
	}
}

func TestOpenAPISpecIsDiscoverable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := humagin.New(r, newHumaConfig("test"))
	registerHumaRoutes(api)

	spec, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	s := string(spec)

	for _, want := range []string{
		"scan-pokemon-v2",
		"scan-pokemon-v3",
		"PvpRankings",
		"PvpEntry",
		"PokemonResult",
		"golbatSecret",
		"X-Golbat-Secret",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("OpenAPI spec missing %q", want)
		}
	}
}
