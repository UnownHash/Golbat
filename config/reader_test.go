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
