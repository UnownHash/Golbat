package decoder

import (
	"sync"
	"testing"

	"golbat/config"
)

// TestReloadOhbemFromMasterFile_ConcurrentQuery reproduces the production
// fatal "concurrent map read and map write": the masterfile watcher calls
// reloadOhbemFromMasterFile while API and decode goroutines are inside
// QueryPvPRank reading the live masterfile maps. Run under -race this fails
// unless reload publishes a fresh instance instead of mutating the live one.
func TestReloadOhbemFromMasterFile_ConcurrentQuery(t *testing.T) {
	oldPath := masterFileCachePath
	oldPvp := config.Config.Pvp
	defer func() {
		masterFileCachePath = oldPath
		config.Config.Pvp = oldPvp
		ohbem.Store(nil) // other tests rely on ohbem being nil
	}()

	masterFileCachePath = "../pogo/master-latest-basics.json"
	config.Config.Pvp.Enabled = true
	config.Config.Pvp.LevelCaps = []int{50, 51}

	InitialiseOhbem()
	if ohbem.Load() == nil {
		t.Fatal("InitialiseOhbem did not initialise ohbem")
	}

	// Sanity check: the query must reach the masterfile maps, otherwise the
	// concurrent phase below would be vacuous.
	if pvp, err := ohbem.Load().QueryPvPRank(1, 0, 0, 1, 15, 15, 15, 20); err != nil || len(pvp) == 0 {
		t.Fatalf("QueryPvPRank sanity check failed: pvp=%v err=%v", pvp, err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Load once per query, like the production read sites.
				if _, err := ohbem.Load().QueryPvPRank(1, 0, 0, 1, 15, 15, 15, 20); err != nil {
					t.Errorf("QueryPvPRank failed during reload: %v", err)
					return
				}
			}
		}()
	}

	for i := 0; i < 5; i++ {
		reloadOhbemFromMasterFile()
	}
	close(stop)
	wg.Wait()
}
