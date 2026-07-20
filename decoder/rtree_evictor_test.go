package decoder

import (
	"sync"
	"testing"
)

func TestTreeEvictorFlushesAllEntriesInBatches(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]treeEvictionEntry[uint64]

	e := newTreeEvictor[uint64]("test", 64, 4, func(entries []treeEvictionEntry[uint64]) {
		mu.Lock()
		batch := make([]treeEvictionEntry[uint64], len(entries))
		copy(batch, entries)
		flushed = append(flushed, batch)
		mu.Unlock()
	})

	for i := range uint64(10) {
		e.Enqueue(i, float64(i), -float64(i))
	}
	e.Close() // drains remaining entries and stops the worker

	mu.Lock()
	defer mu.Unlock()

	total := 0
	seen := map[uint64]bool{}
	for _, batch := range flushed {
		if len(batch) == 0 || len(batch) > 4 {
			t.Errorf("batch size %d outside (0, 4]", len(batch))
		}
		for _, entry := range batch {
			total++
			seen[entry.id] = true
			if entry.lat != float64(entry.id) || entry.lon != -float64(entry.id) {
				t.Errorf("entry %d has wrong coords (%f, %f)", entry.id, entry.lat, entry.lon)
			}
		}
	}
	if total != 10 || len(seen) != 10 {
		t.Errorf("expected 10 unique entries flushed, got %d (%d unique)", total, len(seen))
	}
}

func TestTreeEvictorCloseIsIdempotentAndEnqueueAfterCloseIsSafe(t *testing.T) {
	e := newTreeEvictor[uint64]("test", 4, 2, func([]treeEvictionEntry[uint64]) {})
	e.Enqueue(1, 0, 0)
	e.Close()
	e.Close()          // must not panic
	e.Enqueue(2, 0, 0) // must not panic or block
}
