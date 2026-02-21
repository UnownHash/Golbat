package writebehind

import (
	"time"
)

// QueueConfig holds configuration for write-behind queues
type QueueConfig struct {
	StartupDelaySeconds int           // Delay before processing starts (warmup period)
	WorkerCount         int           // Number of concurrent write workers (default 50)
	BatchSize           int           // Number of entries per batch (default 50)
	BatchTimeout        time.Duration // Max time to wait for a full batch (default 100ms)
}
