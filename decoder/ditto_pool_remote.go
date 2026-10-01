package decoder

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"golbat/config"
	"golbat/pogo"

	log "github.com/sirupsen/logrus"
)

// The disguise pool is published at dittoPoolURL and handled as the
// masterfile is (see remoteFile): fetched at startup, cached, refreshed on a
// timer, with a copy bundled in the binary for when neither the URL nor the
// cache can be read.
//
// Any ditto_pool entry in config overrides it — see InitDittoPool. The
// published list is still fetched and cached while an override is in force,
// and each refresh logs how it differs, so an operator holding a manual list
// can see when the published one has caught up and drop theirs.
const dittoPoolURL = "https://uxie.unownhash.com/"

const dittoPoolRefreshInterval = 6 * time.Hour

var dittoPoolFile = &remoteFile{
	name:      "Ditto pool",
	url:       dittoPoolURL,
	cachePath: "cache/ditto-pool.json",
	bundled:   pogo.BundledDittoPool,
	client:    &http.Client{Timeout: 15 * time.Second},
	apply:     applyPublishedDittoPool,
	onRefresh: onDittoPoolRefresh,
}

// dittoPoolPublished is the most recent published list golbat has read, from
// whichever source supplied it; dittoPoolPreviousPublished is the one it
// replaced, for the refresh's log line. InitDittoPool uses the published list
// when config does not override the pool.
var dittoPoolPublished, dittoPoolPreviousPublished atomic.Pointer[dittoPool]

type publishedDittoPool struct {
	Disguises struct {
		Species []struct {
			PokemonId int `json:"pokemon_id"`
			Form      int `json:"form"`
		} `json:"species"`
	} `json:"ditto_disguises"`
}

// parsePublishedDittoPool reads the published format. It refuses a payload
// with no entries at all rather than returning an empty pool: an empty pool
// switches the roll off without a word, and a truncated or mistaken deploy of
// the URL is a far likelier cause than every disguise being retired at once.
func parsePublishedDittoPool(data []byte) (*dittoPool, error) {
	var raw publishedDittoPool
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not a ditto pool: %w", err)
	}
	var entries []dittoPoolEntry
	for _, s := range raw.Disguises.Species {
		if s.PokemonId <= 0 || s.PokemonId > math.MaxInt16 || s.Form < 0 || s.Form > math.MaxInt16 {
			return nil, fmt.Errorf("entry %d:%d is out of range", s.PokemonId, s.Form)
		}
		entries = append(entries, dittoPoolEntry{pokemonId: int16(s.PokemonId), form: int16(s.Form)})
	}
	if len(entries) == 0 {
		return nil, errors.New("the published pool has no entries")
	}
	return newDittoPool(entries), nil
}

func applyPublishedDittoPool(data []byte) error {
	pool, err := parsePublishedDittoPool(data)
	if err != nil {
		return err
	}
	dittoPoolPreviousPublished.Store(dittoPoolPublished.Swap(pool))
	return nil
}

// EnsureDittoPool loads the published pool and builds the pool in force from
// it or from config. Must run after config.ReadConfig and before any sighting
// is decoded.
func EnsureDittoPool() {
	source, err := dittoPoolFile.ensure()
	if err != nil {
		log.Errorf("[DITTO] no published ditto pool could be loaded: %v", err)
	}
	InitDittoPool()
	logDittoPoolSource(source)
}

// WatchDittoPool refreshes the published pool every dittoPoolRefreshInterval.
func WatchDittoPool() error {
	return dittoPoolFile.watch(dittoPoolRefreshInterval)
}

// onDittoPoolRefresh runs once a refresh has installed a changed published
// pool: it comes into force unless config overrides it, in which case how the
// two differ is logged instead.
func onDittoPoolRefresh() {
	pool := dittoPoolPublished.Load()
	if dittoPoolOverridden() {
		logDittoPoolOverrideDiff(pool)
		return
	}
	InitDittoPool()
	if added, removed := diffDittoPools(pool, dittoPoolPreviousPublished.Load()); len(added) > 0 || len(removed) > 0 {
		log.Infof("[DITTO] ditto pool updated from %s: %d entries; added %v; removed %v",
			dittoPoolURL, pool.size(), added, removed)
	}
}

// dittoPoolOverridden reports whether config sets ditto_pool at all. Any
// entry counts, valid or not: an operator who configured the pool asked not
// to have the published one, and a mistake in their list is theirs to fix —
// InitDittoPool says so loudly — rather than a reason to quietly hand them
// the list they chose against.
func dittoPoolOverridden() bool {
	return len(config.Config.DittoPool) > 0
}

func logDittoPoolSource(published string) {
	live := dittoPoolLookup.Load()
	if dittoPoolOverridden() {
		log.Infof("[DITTO] ditto pool: %d entries from ditto_pool in config, which overrides the published pool", live.size())
		if pub := dittoPoolPublished.Load(); pub != nil {
			logDittoPoolOverrideDiff(pub)
		}
		return
	}
	if live.size() == 0 {
		log.Errorf("[DITTO] ditto pool: none loaded, so ditto_derivation will mark nothing until a refresh from %s succeeds", dittoPoolURL)
		return
	}
	log.Infof("[DITTO] ditto pool: %d entries from %s", live.size(), published)
}

// logDittoPoolOverrideDiff says how the published pool differs from the one
// config pins, and nothing when they agree.
func logDittoPoolOverrideDiff(published *dittoPool) {
	onlyPublished, onlyConfig := diffDittoPools(published, dittoPoolLookup.Load())
	if len(onlyPublished) == 0 && len(onlyConfig) == 0 {
		return
	}
	log.Infof("[DITTO] the published ditto pool differs from ditto_pool in config, which is in force: "+
		"published only %v; config only %v. Remove ditto_pool from config to use the published pool.",
		onlyPublished, onlyConfig)
}

// diffDittoPools returns the disguises only in a and only in b, readable and
// sorted. Either may be nil. Entries are compared exactly as they match
// sightings: a bare config entry already carries both of the published
// list's spellings (see InitDittoPool).
func diffDittoPools(a, b *dittoPool) (onlyA, onlyB []string) {
	ca, cb := a.all(), b.all()
	for e := range ca {
		if !cb[e] {
			onlyA = append(onlyA, e.String())
		}
	}
	for e := range cb {
		if !ca[e] {
			onlyB = append(onlyB, e.String())
		}
	}
	slices.Sort(onlyA)
	slices.Sort(onlyB)
	return onlyA, onlyB
}
