package decoder

import (
	"runtime"
	"testing"

	"golbat/config"
)

func TestCacheShardCountDefaultsToNumCPU(t *testing.T) {
	old := config.Config.Tuning.CacheShards
	defer func() { config.Config.Tuning.CacheShards = old }()

	config.Config.Tuning.CacheShards = 0
	if got := cacheShardCount(); got != runtime.NumCPU() {
		t.Errorf("cacheShardCount() = %d, want NumCPU %d", got, runtime.NumCPU())
	}

	config.Config.Tuning.CacheShards = 64
	if got := cacheShardCount(); got != 64 {
		t.Errorf("cacheShardCount() = %d, want 64", got)
	}
}
