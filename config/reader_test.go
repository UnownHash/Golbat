package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadConfigDecodesProtoEngineOverridesTable is the task brief's
// required check: koanf's TOML provider must decode a
// [proto_engine.overrides] table into ProtoEngine.Overrides
// (map[string]string), not just scalar/slice/struct fields like every
// other koanf-tagged field in this package. ReadConfig() hardcodes
// "config.toml" as the file name (file.Provider("config.toml")), so this
// chdirs into a temp directory containing one to exercise the real
// load path end to end rather than only unit-testing struct decoding.
func TestReadConfigDecodesProtoEngineOverridesTable(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[proto_engine]
default = "hyperpb"

[proto_engine.overrides]
fort_details = "std"
quest = "hyperpb"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(origWd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	cfg, err := ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}

	if cfg.ProtoEngine.Default != "hyperpb" {
		t.Fatalf("Default = %q, want %q", cfg.ProtoEngine.Default, "hyperpb")
	}
	want := map[string]string{
		"fort_details": "std",
		"quest":        "hyperpb",
	}
	if len(cfg.ProtoEngine.Overrides) != len(want) {
		t.Fatalf("Overrides = %#v, want %#v", cfg.ProtoEngine.Overrides, want)
	}
	for k, v := range want {
		if got := cfg.ProtoEngine.Overrides[k]; got != v {
			t.Fatalf("Overrides[%q] = %q, want %q", k, got, v)
		}
	}

	// Legacy gmo/encounter/disk_encounter keys are absent from this
	// config.toml entirely, so they must resolve to "" (inherit), matching
	// the default struct literal in ReadConfig -- not "hyperpb" (the old
	// pre-Wave-3 default), which would silently defeat Overrides/Default
	// for anyone relying on the legacy keys being unset.
	if cfg.ProtoEngine.Gmo != "" || cfg.ProtoEngine.Encounter != "" || cfg.ProtoEngine.DiskEncounter != "" {
		t.Fatalf("expected legacy proto_engine keys to default to \"\", got gmo=%q encounter=%q disk_encounter=%q",
			cfg.ProtoEngine.Gmo, cfg.ProtoEngine.Encounter, cfg.ProtoEngine.DiskEncounter)
	}
}
