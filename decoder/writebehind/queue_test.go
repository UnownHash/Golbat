package writebehind

import (
	"bytes"
	"cmp"
	"context"
	"testing"
	"time"

	"golbat/db"
	"golbat/stats_collector"
)

// testData is the data type for testing
type testData struct {
	key     string
	quality int
}

func TestTypedQueueEnqueue(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	q := NewTypedQueue(TypedQueueConfig[string, testData]{
		Name:                "test",
		BatchSize:           50,
		BatchTimeout:        100 * time.Millisecond,
		StartupDelaySeconds: 0, // No delay for tests
		Db:                  db.DbDetails{},
		Stats:               stats,
		FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []testData) error { return nil },
		KeyFunc:             func(d testData) string { return d.key },
		KeyCompare:          cmp.Compare[string],
	})

	data := testData{key: "test:1", quality: 1}
	q.Enqueue(data, true, 0)

	if q.Size() != 1 {
		t.Errorf("Expected queue size 1, got %d", q.Size())
	}
}

func TestTypedQueueSquashing(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	q := NewTypedQueue(TypedQueueConfig[string, testData]{
		Name:                "test",
		BatchSize:           50,
		BatchTimeout:        100 * time.Millisecond,
		StartupDelaySeconds: 0,
		Db:                  db.DbDetails{},
		Stats:               stats,
		FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []testData) error { return nil },
		KeyFunc:             func(d testData) string { return d.key },
		KeyCompare:          cmp.Compare[string],
	})

	// Enqueue first entity
	data1 := testData{key: "test:1", quality: 1}
	q.Enqueue(data1, true, 0)

	// Enqueue second entity with same key
	data2 := testData{key: "test:1", quality: 2}
	q.Enqueue(data2, false, 0)

	// Should still only have 1 entry (squashed)
	if q.Size() != 1 {
		t.Errorf("Expected queue size 1 after squash, got %d", q.Size())
	}

	// The entry should use the newer data (replaces old)
	q.mu.Lock()
	entry := q.pending["test:1"]
	q.mu.Unlock()

	if entry.Data.quality != 2 {
		t.Errorf("Expected quality 2 (newer), got %d", entry.Data.quality)
	}

	// IsNewRecord should be preserved (true || false = true)
	if !entry.IsNewRecord {
		t.Error("Expected IsNewRecord to be preserved as true after squash")
	}
}

func TestTypedQueueNewRecordPreservation(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	q := NewTypedQueue(TypedQueueConfig[string, testData]{
		Name:                "test",
		BatchSize:           50,
		BatchTimeout:        100 * time.Millisecond,
		StartupDelaySeconds: 0,
		Db:                  db.DbDetails{},
		Stats:               stats,
		FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []testData) error { return nil },
		KeyFunc:             func(d testData) string { return d.key },
		KeyCompare:          cmp.Compare[string],
	})

	// Enqueue as new record
	data1 := testData{key: "test:1", quality: 1}
	q.Enqueue(data1, true, 0)

	// Enqueue update (not new)
	data2 := testData{key: "test:1", quality: 2}
	q.Enqueue(data2, false, 0)

	q.mu.Lock()
	entry := q.pending["test:1"]
	q.mu.Unlock()

	if !entry.IsNewRecord {
		t.Error("IsNewRecord should be preserved as true when first entry was new")
	}
}

func TestTypedQueueDelayHandling(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	q := NewTypedQueue(TypedQueueConfig[string, testData]{
		Name:                "test",
		BatchSize:           50,
		BatchTimeout:        100 * time.Millisecond,
		StartupDelaySeconds: 0,
		Db:                  db.DbDetails{},
		Stats:               stats,
		FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []testData) error { return nil },
		KeyFunc:             func(d testData) string { return d.key },
		KeyCompare:          cmp.Compare[string],
	})

	// Enqueue with 1 second delay
	data1 := testData{key: "test:1", quality: 1}
	q.Enqueue(data1, true, 1*time.Second)

	q.mu.Lock()
	entry := q.pending["test:1"]
	q.mu.Unlock()

	if entry.Delay != 1*time.Second {
		t.Errorf("Expected delay of 1s, got %v", entry.Delay)
	}

	// Enqueue same key with 0 delay (should reduce delay)
	data2 := testData{key: "test:1", quality: 2}
	q.Enqueue(data2, false, 0)

	q.mu.Lock()
	entry = q.pending["test:1"]
	q.mu.Unlock()

	if entry.Delay != 0 {
		t.Errorf("Expected delay reduced to 0, got %v", entry.Delay)
	}
}

func TestTypedQueueWarmup(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	q := NewTypedQueue(TypedQueueConfig[string, testData]{
		Name:                "test",
		BatchSize:           50,
		BatchTimeout:        100 * time.Millisecond,
		StartupDelaySeconds: 1, // 1 second delay
		Db:                  db.DbDetails{},
		Stats:               stats,
		FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []testData) error { return nil },
		KeyFunc:             func(d testData) string { return d.key },
		KeyCompare:          cmp.Compare[string],
	})

	if q.IsWarmupComplete() {
		t.Error("Warmup should not be complete immediately")
	}

	// Wait for warmup
	time.Sleep(1100 * time.Millisecond)

	// Trigger warmup check
	q.checkWarmup()

	if !q.IsWarmupComplete() {
		t.Error("Warmup should be complete after delay")
	}
}

func TestTypedQueueIntegerKey(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()

	type intKeyData struct {
		id      uint64
		quality int
	}

	q := NewTypedQueue(TypedQueueConfig[uint64, intKeyData]{
		Name:                "test",
		BatchSize:           50,
		BatchTimeout:        100 * time.Millisecond,
		StartupDelaySeconds: 0,
		Db:                  db.DbDetails{},
		Stats:               stats,
		FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []intKeyData) error { return nil },
		KeyFunc:             func(d intKeyData) uint64 { return d.id },
		KeyCompare:          cmp.Compare[uint64],
	})

	// Enqueue with integer key
	data1 := intKeyData{id: 12345678901234, quality: 1}
	q.Enqueue(data1, true, 0)

	if q.Size() != 1 {
		t.Errorf("Expected queue size 1, got %d", q.Size())
	}

	// Enqueue same key, should squash
	data2 := intKeyData{id: 12345678901234, quality: 2}
	q.Enqueue(data2, false, 0)

	if q.Size() != 1 {
		t.Errorf("Expected queue size 1 after squash, got %d", q.Size())
	}

	q.mu.Lock()
	entry := q.pending[uint64(12345678901234)]
	q.mu.Unlock()

	if entry.Data.quality != 2 {
		t.Errorf("Expected quality 2 (newer), got %d", entry.Data.quality)
	}
}

// arrayKey is a comparable key that is NOT cmp.Ordered — the shape FortId
// has (a fixed-width array-backed struct, not a string or number). Before
// the queue's key constraint was relaxed from cmp.Ordered to comparable,
// no NewTypedQueue instantiation with this key type could compile.
type arrayKey [3]byte

func TestTypedQueueAcceptsNonOrderedKey(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()

	type arrayKeyData struct {
		id      arrayKey
		quality int
	}

	q := NewTypedQueue(TypedQueueConfig[arrayKey, arrayKeyData]{
		Name:                "test",
		BatchSize:           50,
		BatchTimeout:        100 * time.Millisecond,
		StartupDelaySeconds: 0,
		Db:                  db.DbDetails{},
		Stats:               stats,
		FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []arrayKeyData) error { return nil },
		KeyFunc:             func(d arrayKeyData) arrayKey { return d.id },
		KeyCompare:          func(a, b arrayKey) int { return bytes.Compare(a[:], b[:]) },
	})

	data1 := arrayKeyData{id: arrayKey{1, 2, 3}, quality: 1}
	q.Enqueue(data1, true, 0)

	if q.Size() != 1 {
		t.Errorf("Expected queue size 1, got %d", q.Size())
	}

	// Enqueue same key, should squash
	data2 := arrayKeyData{id: arrayKey{1, 2, 3}, quality: 2}
	q.Enqueue(data2, false, 0)

	if q.Size() != 1 {
		t.Errorf("Expected queue size 1 after squash, got %d", q.Size())
	}

	q.mu.Lock()
	entry := q.pending[arrayKey{1, 2, 3}]
	q.mu.Unlock()

	if entry.Data.quality != 2 {
		t.Errorf("Expected quality 2 (newer), got %d", entry.Data.quality)
	}
}

// TestSortEntriesForLockOrder drives sortEntriesForLockOrder directly with
// out-of-order entries, proving the resulting order is whatever KeyCompare
// says it should be — for a scalar key (cmp.Compare) and for an
// array-backed key (bytes.Compare), which cmp.Compare cannot handle.
func TestSortEntriesForLockOrder(t *testing.T) {
	t.Run("scalar key via cmp.Compare", func(t *testing.T) {
		stats := stats_collector.NewNoopStatsCollector()
		q := NewTypedQueue(TypedQueueConfig[string, testData]{
			Name:                "test",
			StartupDelaySeconds: 0,
			Db:                  db.DbDetails{},
			Stats:               stats,
			FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []testData) error { return nil },
			KeyFunc:             func(d testData) string { return d.key },
			KeyCompare:          cmp.Compare[string],
		})

		entries := []*Entry[string, testData]{
			{Key: "c"},
			{Key: "a"},
			{Key: "b"},
		}
		q.sortEntriesForLockOrder(entries)

		want := []string{"a", "b", "c"}
		for i, e := range entries {
			if e.Key != want[i] {
				t.Fatalf("order[%d] = %q, want %q", i, e.Key, want[i])
			}
		}
	})

	t.Run("array key via bytes.Compare", func(t *testing.T) {
		stats := stats_collector.NewNoopStatsCollector()

		type arrayKeyData struct {
			id arrayKey
		}

		q := NewTypedQueue(TypedQueueConfig[arrayKey, arrayKeyData]{
			Name:                "test",
			StartupDelaySeconds: 0,
			Db:                  db.DbDetails{},
			Stats:               stats,
			FlushFunc:           func(ctx context.Context, db db.DbDetails, entries []arrayKeyData) error { return nil },
			KeyFunc:             func(d arrayKeyData) arrayKey { return d.id },
			KeyCompare:          func(a, b arrayKey) int { return bytes.Compare(a[:], b[:]) },
		})

		entries := []*Entry[arrayKey, arrayKeyData]{
			{Key: arrayKey{3, 0, 0}},
			{Key: arrayKey{1, 0, 0}},
			{Key: arrayKey{2, 0, 0}},
		}
		q.sortEntriesForLockOrder(entries)

		want := []arrayKey{{1, 0, 0}, {2, 0, 0}, {3, 0, 0}}
		for i, e := range entries {
			if e.Key != want[i] {
				t.Fatalf("order[%d] = %v, want %v", i, e.Key, want[i])
			}
		}
	})
}
