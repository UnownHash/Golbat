package config

import "testing"

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
