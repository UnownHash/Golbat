package main

import (
	"context"
	"testing"
)

// These exercise the routing switch only via DB-free branches (get-time is a
// no-op; the no-context case returns before any DB access). The get-state ->
// DB path is covered by Task C4's decoder test and manual validation.

func TestDecodeNebula_RoutesInvasion(t *testing.T) {
	nd := &NebulaData{
		Endpoint: "get-time",
		Invasion: &nebulaInvasionContext{FortId: "F", IncidentId: "-9"},
	}
	if got := decodeNebula(context.Background(), "get-time", nd); got != "ignored (not needed for lineup)" {
		t.Fatalf("expected invasion get-time to be ignored, got %q", got)
	}
}

func TestDecodeNebula_NoContextIsRejected(t *testing.T) {
	nd := &NebulaData{Endpoint: "get-time"} // no Invasion set
	if got := decodeNebula(context.Background(), "get-time", nd); got != "no context" {
		t.Errorf("got %q, want \"no context\"", got)
	}
}
