package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// The other tests here construct []GolbatPeer in Go, so they exercise
// validateGolbatPeers but never the koanf tags. A singular/plural mismatch
// between the TOML table name and the `koanf:"golbat_peer"` tag would bind
// nothing at all - GolbatPeers empty, the feature silently off - and every
// one of those tests would still pass. That is the one failure mode they
// structurally cannot see, so bind real TOML through the same parser and
// struct ReadConfig uses.
func TestGolbatPeerTomlBindsThroughKoanf(t *testing.T) {
	const raw = `
[[golbat_peer]]
address = "10.0.0.2:50051"
api_secret = "shared-secret"
timeout_ms = 1500

[[golbat_peer]]
address = "10.0.0.3:50051"
`
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write temp config: %s", err)
	}

	k := koanf.New(".")
	if err := k.Load(file.Provider(path), toml.Parser()); err != nil {
		t.Fatalf("load: %s", err)
	}

	var cfg configDefinition
	if err := k.Unmarshal("", &cfg); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}

	if len(cfg.GolbatPeers) != 2 {
		t.Fatalf("[[golbat_peer]] did not bind: got %d peers, want 2", len(cfg.GolbatPeers))
	}
	if got := cfg.GolbatPeers[0].Address; got != "10.0.0.2:50051" {
		t.Fatalf("address: got %q", got)
	}
	if got := cfg.GolbatPeers[0].ApiSecret; got != "shared-secret" {
		t.Fatalf("api_secret: got %q", got)
	}
	if got := cfg.GolbatPeers[0].TimeoutMs; got != 1500 {
		t.Fatalf("timeout_ms: got %d want 1500", got)
	}

	// And the default still lands on the entry that omitted it, through the
	// same call ReadConfig makes.
	if err := validateGolbatPeers(cfg.GolbatPeers); err != nil {
		t.Fatalf("unexpected validation error: %s", err)
	}
	if got := cfg.GolbatPeers[1].TimeoutMs; got != defaultPeerTimeoutMs {
		t.Fatalf("default timeout: got %d want %d", got, defaultPeerTimeoutMs)
	}
}

func TestValidateGolbatPeersAppliesDefaultTimeout(t *testing.T) {
	peers := []GolbatPeer{{Address: "127.0.0.1:50051", ApiSecret: "s"}}

	if err := validateGolbatPeers(peers); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if peers[0].TimeoutMs != defaultPeerTimeoutMs {
		t.Fatalf("timeout: got %d want %d", peers[0].TimeoutMs, defaultPeerTimeoutMs)
	}
}

func TestValidateGolbatPeersRejectsMissingAddress(t *testing.T) {
	peers := []GolbatPeer{{ApiSecret: "s"}}

	if err := validateGolbatPeers(peers); err == nil {
		t.Fatal("a peer without an address must be rejected")
	}
}

func TestValidateGolbatPeersKeepsExplicitTimeout(t *testing.T) {
	peers := []GolbatPeer{{Address: "127.0.0.1:50051", TimeoutMs: 2000}}

	if err := validateGolbatPeers(peers); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if peers[0].TimeoutMs != 2000 {
		t.Fatalf("explicit timeout must be preserved, got %d", peers[0].TimeoutMs)
	}
}
