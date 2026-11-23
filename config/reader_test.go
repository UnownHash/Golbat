package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ReadConfig layers onto a package-level koanf instance, so these cases share
// state. Each one asserts only on what it sets itself.

func TestReadConfigMissingDefaultPathIsNotAnError(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := ReadConfig(DefaultConfigPath); err != nil {
		t.Fatalf("missing %s should fall back to defaults and env vars, got: %v", DefaultConfigPath, err)
	}
}

func TestReadConfigMissingExplicitPathIsAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.toml")

	_, err := ReadConfig(missing)
	if err == nil {
		t.Fatalf("explicitly requested config %s is missing, expected an error", missing)
	}
}

func TestReadConfigLoadsExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.toml")
	if err := os.WriteFile(path, []byte("port = 12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig(%s): %v", path, err)
	}
	if cfg.Port != 12345 {
		t.Errorf("Port = %d, want 12345 from %s", cfg.Port, path)
	}
}

func TestDerivationEventsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig(%s): %v", path, err)
	}
	de := cfg.DerivationEvents
	if de.Enabled || de.Dir != "logs" || de.BucketMinutes != 15 || de.RetainDays != 14 {
		t.Errorf("DerivationEvents = %+v, want disabled, logs, 15, 14", de)
	}
}

func TestDerivationModifiersConfigDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig(%s): %v", path, err)
	}
	m := cfg.DerivationModifiers
	if m.Enabled || m.Start != 3 || m.Confirm != 10 || m.End != 8 ||
		m.WindowSeconds != 1800 || m.UnseenSeconds != 3600 || m.ZoneWideCells != 3 {
		t.Errorf("DerivationModifiers = %+v, want disabled, 3/10/8/1800/3600/3", m)
	}
}
