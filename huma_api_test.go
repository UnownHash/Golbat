package main

import (
	"bytes"
	"testing"

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
