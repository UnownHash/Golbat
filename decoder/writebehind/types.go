package writebehind

import (
	"time"

	"golbat/db"
)

// Writeable is implemented by any entity that can be written through the queue
type Writeable interface {
	// WriteKey returns a unique key for this entity (for squashing)
	WriteKey() string

	// WriteType returns the entity type name (for metrics)
	WriteType() string

	// WriteToDB performs the actual database write
	// isNewRecord determines INSERT vs UPDATE
	WriteToDB(db db.DbDetails, isNewRecord bool) error
}

// QueueEntry represents a pending write in the queue
type QueueEntry struct {
	Key         string
	Entity      Writeable
	QueuedAt    time.Time
	UpdatedAt   time.Time
	IsNewRecord bool          // Track if this needs INSERT (preserved across updates)
	Delay       time.Duration // Minimum delay before writing (0 = immediate)
}

// QueueConfig holds configuration for the write-behind queue
type QueueConfig struct {
	StartupDelaySeconds int // Delay before processing starts (warmup period)
	RateLimit           int // Writes per second, 0 = unlimited
	BurstCapacity       int // Token bucket burst capacity
}
