package writebehind

import (
	"testing"
	"time"

	"golbat/db"
	"golbat/stats_collector"
)

// mockWriteable implements Writeable for testing
type mockWriteable struct {
	key       string
	writeType string
	quality   int
	written   bool
}

func (m *mockWriteable) WriteKey() string  { return m.key }
func (m *mockWriteable) WriteType() string { return m.writeType }
func (m *mockWriteable) WriteToDB(db db.DbDetails, isNewRecord bool) error {
	m.written = true
	return nil
}

func TestQueueEnqueue(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	cfg := QueueConfig{
		StartupDelaySeconds: 0,  // No delay for tests
		WorkerCount:         10, // 10 workers for tests
	}
	q := NewQueue(cfg, db.DbDetails{}, stats)

	entity := &mockWriteable{key: "test:1", writeType: "test", quality: 1}
	q.Enqueue(entity, true, 0)

	if q.Size() != 1 {
		t.Errorf("Expected queue size 1, got %d", q.Size())
	}
}

func TestQueueSquashing(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	cfg := QueueConfig{
		StartupDelaySeconds: 0,
		WorkerCount:         10,
	}
	q := NewQueue(cfg, db.DbDetails{}, stats)

	// Enqueue first entity
	entity1 := &mockWriteable{key: "test:1", writeType: "test", quality: 1}
	q.Enqueue(entity1, true, 0)

	// Enqueue second entity with same key
	entity2 := &mockWriteable{key: "test:1", writeType: "test", quality: 2}
	q.Enqueue(entity2, false, 0)

	// Should still only have 1 entry (squashed)
	if q.Size() != 1 {
		t.Errorf("Expected queue size 1 after squash, got %d", q.Size())
	}

	// The entry should use the newer entity (replaces old)
	q.mu.Lock()
	entry := q.pending["test:1"]
	q.mu.Unlock()

	if entry.Entity.(*mockWriteable).quality != 2 {
		t.Errorf("Expected entity quality 2 (newer), got %d", entry.Entity.(*mockWriteable).quality)
	}

	// IsNewRecord should be preserved (true || false = true)
	if !entry.IsNewRecord {
		t.Error("Expected IsNewRecord to be preserved as true after squash")
	}
}

func TestQueueNewRecordPreservation(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	cfg := QueueConfig{
		StartupDelaySeconds: 0,
		WorkerCount:         10,
	}
	q := NewQueue(cfg, db.DbDetails{}, stats)

	// Enqueue as new record
	entity1 := &mockWriteable{key: "test:1", writeType: "test", quality: 1}
	q.Enqueue(entity1, true, 0)

	// Enqueue update (not new)
	entity2 := &mockWriteable{key: "test:1", writeType: "test", quality: 2}
	q.Enqueue(entity2, false, 0)

	q.mu.Lock()
	entry := q.pending["test:1"]
	q.mu.Unlock()

	if !entry.IsNewRecord {
		t.Error("IsNewRecord should be preserved as true when first entry was new")
	}
}

func TestQueueDelayHandling(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	cfg := QueueConfig{
		StartupDelaySeconds: 0,
		WorkerCount:         10,
	}
	q := NewQueue(cfg, db.DbDetails{}, stats)

	// Enqueue with 1 second delay
	entity1 := &mockWriteable{key: "test:1", writeType: "test", quality: 1}
	q.Enqueue(entity1, true, 1*time.Second)

	q.mu.Lock()
	entry := q.pending["test:1"]
	q.mu.Unlock()

	if entry.Delay != 1*time.Second {
		t.Errorf("Expected delay of 1s, got %v", entry.Delay)
	}

	// Enqueue same key with 0 delay (should reduce delay)
	entity2 := &mockWriteable{key: "test:1", writeType: "test", quality: 2}
	q.Enqueue(entity2, false, 0)

	q.mu.Lock()
	entry = q.pending["test:1"]
	q.mu.Unlock()

	if entry.Delay != 0 {
		t.Errorf("Expected delay reduced to 0, got %v", entry.Delay)
	}
}

func TestQueueWarmup(t *testing.T) {
	stats := stats_collector.NewNoopStatsCollector()
	cfg := QueueConfig{
		StartupDelaySeconds: 1, // 1 second delay
		WorkerCount:         10,
	}
	q := NewQueue(cfg, db.DbDetails{}, stats)

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
