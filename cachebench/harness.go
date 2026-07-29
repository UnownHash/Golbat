// Package cachebench benchmarks TTL-cache candidates for Golbat's cache
// layer against the workload described in docs/cache-investigation-brief.md.
//
// It is a standalone module and is NOT part of the Golbat build.
package cachebench

import (
	"time"
)

// Entity models the values Golbat caches: pointers to ~1KB structs
// (Pokemon is the sizing reference). The cache stores *Entity.
type Entity struct {
	Id              uint64
	Lat             float64
	Lon             float64
	ExpireTimestamp int64
	Updated         int64
	Payload         [968]byte // pads the struct to ~1KB like production entities
}

// EvictReason mirrors the distinction Golbat needs: expiry vs explicit delete.
type EvictReason int

const (
	EvictExpired EvictReason = iota + 1
	EvictDeleted
)

// BenchCache is the API surface Golbat needs from a cache (brief: "What
// Golbat actually needs"). Keys are uint64 in the benchmarks (pokemon /
// spawnpoint hot paths); all candidates are generic over K in their real
// APIs.
type BenchCache interface {
	// Get returns the live value or (nil, false). Whether a hit extends the
	// entry's TTL ("touch") is fixed per cache instance via Config.TouchOnHit.
	Get(key uint64) (*Entity, bool)
	// Set stores the value with a per-entry TTL.
	Set(key uint64, v *Entity, ttl time.Duration)
	// GetOrSetFunc atomically returns the existing live value or creates one
	// via factory. found=true means an existing entry was returned. The
	// single-winner property (factory runs at most once per absent key even
	// under races) is load-bearing for Golbat's locking model.
	GetOrSetFunc(key uint64, factory func() *Entity, ttl time.Duration) (*Entity, bool)
	Delete(key uint64)
	Len() int
	Range(fn func(key uint64, v *Entity) bool)
	// Close stops background goroutines so the instance can be GC'd between
	// benchmark cases.
	Close()
}

// Config is the common construction config for all candidates.
type Config struct {
	// Shards is used by the ttlcache baseline (decoder/sharded_cache.go
	// replica). Other candidates ignore it (they scale internally).
	Shards int
	// TouchOnHit: Get extends the entry to its own TTL (spawnpoint/fort
	// pattern). false = pokemon pattern (WithDisableTouchOnHit).
	TouchOnHit bool
	// DefaultTTL is the cache-wide default (ttlcache WithTTL).
	DefaultTTL time.Duration
	// OnEvict, when non-nil, must be invoked for expiry and explicit deletes.
	OnEvict func(key uint64, v *Entity, reason EvictReason)
	// ExpectedEntries sizes bounded candidates (theine requires a max size;
	// adapters give 2x headroom so capacity eviction stays out of the way).
	ExpectedEntries int
	// SweepInterval is the proactive-eviction cadence for candidates where
	// it is configurable (prototype wheel tick). otter and theine hard-code
	// a 1s maintenance ticker; ttlcache sweeps at its own schedule.
	SweepInterval time.Duration
	// TouchRefreshBelow enables hysteresis touch on the ttlcache adapter
	// (decoder commit 75b3df0): underlying touch-on-hit is disabled and
	// Get/GetOrSetFunc re-Set the entry only when its remaining lifetime
	// drops below this threshold. This is the NEUTRALIZED production
	// baseline the decision rule judges candidates against.
	TouchRefreshBelow time.Duration
}

// Factory constructs a candidate cache.
type Factory func(cfg Config) BenchCache

// Candidates maps candidate name -> factory. Iterated in this fixed order
// by the benchmarks.
//
// "ttlcache-hyst" is the hysteresis-touch baseline (75b3df0), the
// spawnpoint shape: refresh threshold = DefaultTTL/4 (15 min of 60 min),
// re-jittered refresh TTL. It participates in benchmarks 1, 2 and 6; for
// expiry/callback/memory scenarios it is machinery-identical to "ttlcache".
var CandidateNames = []string{"ttlcache", "ttlcache-hyst", "otter", "theine", "proto"}

var Candidates = map[string]Factory{
	"ttlcache": NewTTLCacheAdapter,
	"ttlcache-hyst": func(cfg Config) BenchCache {
		cfg.TouchOnHit = false // hysteresis requires underlying touch off
		cfg.TouchRefreshBelow = cfg.DefaultTTL / 4
		return NewTTLCacheAdapter(cfg)
	},
	"otter":  NewOtterAdapter,
	"theine": NewTheineAdapter,
	"proto":  NewProtoAdapter,
}
