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
		"ApiPvpRankings",
		"ApiPvpEntry",
		"ApiPokemonResult",
		"golbatSecret",
		"X-Golbat-Secret",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("OpenAPI spec missing %q", want)
		}
	}
}

// TestScanRequestRequiredFields pins the per-field required/optional contract for
// the scan request schemas: only the bounding box (min/max) is required at the top
// level; filter attributes are all optional; but a range object, when present,
// requires both min and max.
func TestScanRequestRequiredFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := humagin.New(r, newHumaConfig("test"))
	registerHumaRoutes(api)

	raw, err := gojson.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Required []string `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := gojson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal openapi: %v", err)
	}

	required := func(name string) []string {
		s, ok := doc.Components.Schemas[name]
		if !ok {
			names := make([]string, 0, len(doc.Components.Schemas))
			for n := range doc.Components.Schemas {
				names = append(names, n)
			}
			t.Fatalf("schema %q not found; available: %v", name, names)
		}
		return s.Required
	}
	asSet := func(ss []string) map[string]bool {
		m := map[string]bool{}
		for _, s := range ss {
			m[s] = true
		}
		return m
	}
	wantExactly := func(name string, want ...string) {
		got := asSet(required(name))
		wantSet := asSet(want)
		if len(got) != len(wantSet) {
			t.Errorf("%s required = %v, want exactly %v", name, required(name), want)
			return
		}
		for w := range wantSet {
			if !got[w] {
				t.Errorf("%s required = %v, missing %q", name, required(name), w)
			}
		}
	}

	// Bounding box required; limit/filters optional.
	wantExactly("ApiPokemonScan2", "min", "max")
	wantExactly("ApiPokemonScan3", "min", "max")
	// A range object requires both bounds when present.
	wantExactly("ApiPokemonDnfMinMax", "min", "max")
	wantExactly("ApiPokemonDnfMinMax8", "min", "max")
	// Filter attributes are all optional.
	wantExactly("ApiPokemonDnfFilter")
	wantExactly("ApiPokemonDnfFilter3")
	// Within a pokemon selector, id is required (a form without an id can never
	// match); form stays optional.
	wantExactly("ApiPokemonDnfId", "id")
}

// TestLatLonSchemaDocumentsOnlyLatLon asserts the OpenAPI advertises only the
// canonical lat/lon spelling, even though latitude/longitude is still accepted at
// runtime (see decoder.ApiLatLon.UnmarshalJSON and TestHumaScanAcceptsLatLonSpellings).
func TestLatLonSchemaDocumentsOnlyLatLon(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := humagin.New(r, newHumaConfig("test"))
	registerHumaRoutes(api)

	raw, err := gojson.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	// ApiLatLon is a SchemaProvider returning an unregistered schema, so Huma
	// inlines it into the min/max properties rather than as a named component.
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Properties map[string]any `json:"properties"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := gojson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal openapi: %v", err)
	}
	coord := doc.Components.Schemas["ApiPokemonScan2"].Properties["min"].Properties
	if coord == nil {
		t.Fatalf("ApiPokemonScan2.min has no inlined coordinate properties")
	}
	for _, want := range []string{"lat", "lon"} {
		if _, ok := coord[want]; !ok {
			t.Errorf("coordinate schema should document %q; properties=%v", want, coord)
		}
	}
	for _, notWant := range []string{"latitude", "longitude"} {
		if _, ok := coord[notWant]; ok {
			t.Errorf("coordinate schema must NOT advertise %q; properties=%v", notWant, coord)
		}
	}
}
