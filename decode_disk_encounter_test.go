package main

import (
	"context"
	"strings"
	"testing"

	"golbat/stats_collector"
)

// Request payloads are required for disk encounters (they carry the
// encounter id, fort id and fort location); without one the proto is
// counted and skipped.
func TestDecodeDiskEncounterRequiresRequest(t *testing.T) {
	statsCollector = stats_collector.NewNoopStatsCollector()
	res := decodeDiskEncounter(context.Background(), nil, []byte{}, "tester")
	if !strings.Contains(res, "without request") {
		t.Errorf("decodeDiskEncounter without request = %q, want ignored-without-request message", res)
	}
}
