package decoder

import (
	"context"
	"errors"
	"testing"

	"golbat/db"
)

// TestClearQuestsForPokestopsStopsOnCancelledContext: once the context is
// done, the per-id loop must stop and report the error instead of logging
// each failed load, leaving a zeroed placeholder in the cache per id, and
// returning a partial count as success.
func TestClearQuestsForPokestopsStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	const id = "pr400-clear-quests-cancelled"

	n, err := clearQuestsForPokestops(ctx, db.DbDetails{}, []string{id})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n != 0 {
		t.Fatalf("cleared %d, want 0", n)
	}
	if _, ok := pokestopCache.Get(id); ok {
		t.Fatal("cancelled clear left a placeholder pokestop in the cache")
	}
}
