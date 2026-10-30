package decoder

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/pogo"
)

// dittoPoolServer answers every request dittoPoolFile.client makes, whatever
// the URL, with the status and body the test sets, and counts the requests.
// The URL stays the production constant: the client's transport is what is
// swapped.
type dittoPoolServer struct {
	status int
	body   string
	hits   atomic.Int32
}

func (s *dittoPoolServer) RoundTrip(req *http.Request) (*http.Response, error) {
	s.hits.Add(1)
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// withDittoPoolSources isolates every input the Ditto pool loader reads:
// its HTTP client, its cache (in a temp dir) and bundled copy, ditto_pool in
// config, and the published and live pools.
func withDittoPoolSources(t *testing.T, status int, body string) (*dittoPoolServer, string) {
	t.Helper()
	srv := &dittoPoolServer{status: status, body: body}
	dir := t.TempDir()

	f := dittoPoolFile
	oldClient, oldCache, oldBundled, oldRaw := f.client, f.cachePath, f.bundled, f.current()
	oldConfig := config.Config.DittoPool
	oldPublished, oldPrevious, oldLive := dittoPoolPublished.Load(), dittoPoolPreviousPublished.Load(), dittoPoolLookup.Load()
	f.client = &http.Client{Transport: srv}
	f.cachePath = filepath.Join(dir, "cache", "ditto-pool.json")
	f.bundled = nil
	f.mu.Lock()
	f.raw = nil
	f.mu.Unlock()
	config.Config.DittoPool = nil
	dittoPoolPublished.Store(nil)
	dittoPoolPreviousPublished.Store(nil)
	t.Cleanup(func() {
		f.client, f.cachePath, f.bundled = oldClient, oldCache, oldBundled
		f.mu.Lock()
		f.raw = oldRaw
		f.mu.Unlock()
		config.Config.DittoPool = oldConfig
		dittoPoolPublished.Store(oldPublished)
		dittoPoolPreviousPublished.Store(oldPrevious)
		dittoPoolLookup.Store(oldLive)
	})
	return srv, dir
}

func loadDittoPoolFile(t *testing.T, path string) *dittoPool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	p, err := parsePublishedDittoPool(data)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return p
}

func poolJSON(entries ...[2]int) string {
	var b strings.Builder
	b.WriteString(`{"ditto_disguises":{"species":[`)
	for i, e := range entries {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"pokemon_id":` + strconv.Itoa(e[0]) + `,"form":` + strconv.Itoa(e[1]) + `}`)
	}
	b.WriteString(`]}}`)
	return b.String()
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The copy shipped in the repository must be one golbat can read: it is the
// last thing standing between a first start with no network and no pool.
func TestBundledDittoPoolParses(t *testing.T) {
	p, err := parsePublishedDittoPool(pogo.BundledDittoPool)
	if err != nil {
		t.Fatalf("the ditto pool embedded in the binary does not parse: %v", err)
	}
	if !p.has(dittoPoolEntry{pokemonId: 283, form: 1412}) {
		t.Error("the bundled pool does not contain SURSKIT_NORMAL, which every published version so far has listed")
	}
}

func TestParsePublishedDittoPoolRefusesWhatWouldSilentlyDisableTheRoll(t *testing.T) {
	for name, body := range map[string]string{
		"not json":     `<html>maintenance</html>`,
		"no entries":   poolJSON(),
		"wrong shape":  `{"species":[{"pokemon_id":56,"form":999}]}`,
		"out of range": poolJSON([2]int{70000, 0}),
	} {
		if p, err := parsePublishedDittoPool([]byte(body)); err == nil {
			t.Errorf("%s: parsed to %d entries, want an error — accepting it would replace the pool in force", name, p.size())
		}
	}
}

// Fetched, then cached, then bundled — the masterfile's order.
func TestEnsureDittoPoolFallsBackFromURLToCacheToBundled(t *testing.T) {
	surskit := dittoPoolEntry{pokemonId: 283, form: 1412}
	seedot := dittoPoolEntry{pokemonId: 273, form: 625}
	mankey := dittoPoolEntry{pokemonId: 56, form: 999}

	t.Run("the URL answers: it is used and cached", func(t *testing.T) {
		srv, _ := withDittoPoolSources(t, http.StatusOK, poolJSON([2]int{283, 1412}))
		EnsureDittoPool()
		if !inDittoPool(surskit.pokemonId, surskit.form) {
			t.Fatal("the fetched pool is not in force")
		}
		if srv.hits.Load() != 1 {
			t.Errorf("fetched %d times, want 1", srv.hits.Load())
		}
		if !loadDittoPoolFile(t, dittoPoolFile.cachePath).has(surskit) {
			t.Error("the fetched pool was not cached")
		}
	})

	t.Run("the URL fails: the cache is used over the bundled copy", func(t *testing.T) {
		withDittoPoolSources(t, http.StatusInternalServerError, "")
		writeFile(t, dittoPoolFile.cachePath, poolJSON([2]int{273, 625}))
		dittoPoolFile.bundled = []byte(poolJSON([2]int{56, 999}))
		EnsureDittoPool()
		if !inDittoPool(seedot.pokemonId, seedot.form) || inDittoPool(mankey.pokemonId, mankey.form) {
			t.Error("with the URL down the cache must be used, not the bundled copy")
		}
	})

	t.Run("the URL serves an empty pool: it is refused, not applied", func(t *testing.T) {
		withDittoPoolSources(t, http.StatusOK, poolJSON())
		writeFile(t, dittoPoolFile.cachePath, poolJSON([2]int{273, 625}))
		EnsureDittoPool()
		if !inDittoPool(seedot.pokemonId, seedot.form) {
			t.Error("an empty published pool replaced the cached one, which would switch the roll off")
		}
		if !loadDittoPoolFile(t, dittoPoolFile.cachePath).has(seedot) {
			t.Error("an empty published pool overwrote the cache")
		}
	})

	t.Run("URL and cache both fail: the bundled copy is used", func(t *testing.T) {
		withDittoPoolSources(t, http.StatusInternalServerError, "")
		dittoPoolFile.bundled = []byte(poolJSON([2]int{56, 999}))
		EnsureDittoPool()
		if !inDittoPool(mankey.pokemonId, mankey.form) {
			t.Error("the bundled pool is not in force when nothing else can be read")
		}
	})
}

// ditto_pool in config wins, but the published pool is still fetched and
// cached, and how it differs is logged, so the operator can see when it has
// caught up.
func TestConfigDittoPoolOverridesButThePublishedOneIsStillFetched(t *testing.T) {
	srv, _ := withDittoPoolSources(t, http.StatusOK, poolJSON([2]int{283, 1412}, [2]int{273, 625}))
	config.Config.DittoPool = []string{"56", "283"}
	logs := captureStandardLoggerHook(t)

	EnsureDittoPool()

	if !inDittoPool(56, 999) || inDittoPool(273, 625) {
		t.Fatal("config's ditto_pool is not the pool in force")
	}
	if srv.hits.Load() != 1 {
		t.Error("the published pool was not fetched while config overrides it")
	}
	if !loadDittoPoolFile(t, dittoPoolFile.cachePath).has(dittoPoolEntry{pokemonId: 273, form: 625}) {
		t.Error("the published pool was not cached while config overrides it")
	}
	var diff string
	for _, e := range logs.AllEntries() {
		if strings.Contains(e.Message, "differs from ditto_pool in config") {
			diff = e.Message
		}
	}
	if diff == "" {
		t.Fatal("no line saying how the published pool differs from the override")
	}
	for _, want := range []string{"SEEDOT/SEEDOT_NORMAL (273:625)", "MANKEY/MANKEY_NORMAL (56:999)"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the difference line does not name %s: %s", want, diff)
		}
	}
	if strings.Contains(diff, "283:1412") {
		t.Errorf("the difference line names SURSKIT, which both lists have: %s", diff)
	}

	// A refresh repeats the comparison and still does not replace the override.
	srv.body = poolJSON([2]int{56, 0}, [2]int{56, 999}, [2]int{283, 0}, [2]int{283, 1412})
	logs.Reset()
	dittoPoolFile.refresh()
	if inDittoPool(273, 625) || !inDittoPool(56, 999) {
		t.Error("a refresh replaced config's ditto_pool")
	}
	for _, e := range logs.AllEntries() {
		if strings.Contains(e.Message, "differs from ditto_pool in config") {
			t.Errorf("logged a difference once the published pool matched the override: %s", e.Message)
		}
	}
}

func TestRefreshDittoPoolSwapsThePublishedPoolInWithoutARestart(t *testing.T) {
	srv, _ := withDittoPoolSources(t, http.StatusOK, poolJSON([2]int{283, 1412}))
	EnsureDittoPool()

	srv.body = poolJSON([2]int{283, 1412}, [2]int{25, 598})
	dittoPoolFile.refresh()
	if !inDittoPool(25, 598) {
		t.Fatal("a refreshed published pool did not come into force")
	}

	// A failed or empty refresh keeps what is in force.
	for _, bad := range []struct {
		status int
		body   string
	}{{http.StatusBadGateway, ""}, {http.StatusOK, poolJSON()}} {
		srv.status, srv.body = bad.status, bad.body
		dittoPoolFile.refresh()
		if !inDittoPool(25, 598) {
			t.Errorf("a refresh answering %d %q dropped the pool in force", bad.status, bad.body)
		}
	}
}

// detectDitto's prior for its two unsettleable transitions asks whether a
// scan shows a known disguise — species AND form, as the roll does. The pool
// answers from startup; a disguise confirmed since start counts too.
func TestKnownDittoDisguiseIsAnsweredByThePool(t *testing.T) {
	withDittoPoolEntries(t, "58") // GROWLITHE_NORMAL
	normal := &pokemonScan{Pokemon: 58, Form: 280}
	hisuian := &pokemonScan{Pokemon: 58, Form: 2792}
	pikachu := &pokemonScan{Pokemon: 25, Form: 598}
	for _, s := range []*pokemonScan{normal, hisuian, pikachu} {
		dittoDisguises.Delete(dittoPoolEntry{pokemonId: int16(s.Pokemon), form: int16(s.Form)})
	}

	if !knownDittoDisguise(normal) {
		t.Error("a disguise in the pool is not known until one is confirmed")
	}
	if knownDittoDisguise(hisuian) {
		t.Error("a Hisuian Growlithe counts as a known disguise because an ordinary one is in the pool — " +
			"a Ditto only wears the NORMAL form, so the form has to match")
	}
	if knownDittoDisguise(pikachu) {
		t.Fatal("fixture assumption violated: PIKACHU_NORMAL is neither in the pool nor confirmed")
	}
	key := dittoPoolEntry{pokemonId: 25, form: 598}
	dittoDisguises.Store(key, time.Now())
	t.Cleanup(func() { dittoDisguises.Delete(key) })
	if !knownDittoDisguise(pikachu) {
		t.Error("a disguise confirmed since start is not known")
	}
	if knownDittoDisguise(&pokemonScan{Pokemon: 25, Form: 2330}) {
		t.Error("confirming one form of a species made another form of it known")
	}
}

// The first confirmation of each disguise since start is logged once, by
// name, and a disguise the pool does not list is a warning naming the entry.
func TestConfirmDittoSeparatesDisguisesInThePoolFromOnesNotInIt(t *testing.T) {
	withDittoPoolEntries(t, "283")
	for _, k := range []dittoPoolEntry{{pokemonId: 283, form: 1412}, {pokemonId: 25, form: 598}} {
		dittoDisguises.Delete(k)
	}
	logs := captureStandardLoggerHook(t)

	confirmDitto(&pokemonScan{Pokemon: 283, Form: 1412, Level: 20})
	confirmDitto(&pokemonScan{Pokemon: 283, Form: 1412, Level: 21}) // again: not logged again
	confirmDitto(&pokemonScan{Pokemon: int32(pogo.HoloPokemonId_PIKACHU), Form: 598, Level: 20})

	var infos, warns []string
	for _, e := range logs.AllEntries() {
		if !strings.HasPrefix(e.Message, "[DITTO] Confirmed a Ditto") {
			continue
		}
		switch e.Level {
		case log.InfoLevel:
			infos = append(infos, e.Message)
		case log.WarnLevel:
			warns = append(warns, e.Message)
		}
	}
	if len(infos) != 1 || !strings.Contains(infos[0], "SURSKIT/SURSKIT_NORMAL (283:1412)") || !strings.Contains(infos[0], "in the ditto pool") {
		t.Errorf("in-pool confirmations: %q, want exactly one Info line naming SURSKIT/SURSKIT_NORMAL", infos)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "PIKACHU/PIKACHU_NORMAL (25:598)") ||
		!strings.Contains(warns[0], "NOT in the ditto pool") || !strings.Contains(warns[0], `"25:598"`) {
		t.Errorf("not-in-pool confirmations: %q, want exactly one Warn naming PIKACHU and the entry to add", warns)
	}
}

// The prior at work, through detectDitto: the BN>[0P] transition, which scan
// evidence cannot settle. The record was seen boosted as one species and is
// now seen unboosted, at the same level and IVs, as another, with the cell
// partly cloudy — either the spawn rerolled, or a Ditto under a new disguise
// is showing its boosted stats. detectDitto calls it a Ditto only if the new
// species is a known disguise and the old one is not.
// The pool is what makes a species known from startup; confirmations alone
// would leave this guess wrong after every restart.
func TestDetectDittoPriorUsesThePoolFromStartup(t *testing.T) {
	const disguise, previous = int32(283), int32(19) // Surskit, Rattata
	for _, s := range []int32{disguise, previous} {
		dittoDisguises.Delete(dittoPoolEntry{pokemonId: int16(s)})
		dittoDisguises.Delete(dittoPoolEntry{pokemonId: int16(s), form: 1412})
	}
	newRecord := func(form int32) (*Pokemon, *pokemonScan) {
		p := &Pokemon{
			PokemonData: PokemonData{Id: Uint64Str(930401), PokemonId: int16(disguise)},
			scanHistory: []*pokemonScan{{Pokemon: previous, Weather: int32(pogo.GameplayWeatherProto_RAINY),
				Level: 20, Attack: 10, Defense: 11, Stamina: 12}},
		}
		scan := &pokemonScan{Pokemon: disguise, Form: form, Weather: int32(pogo.GameplayWeatherProto_NONE),
			CellWeather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY), Level: 20, Attack: 10, Defense: 11, Stamina: 12}
		return p, scan
	}

	t.Run("in the pool", func(t *testing.T) {
		withDittoPoolEntries(t, "283")
		p, scan := newRecord(1412) // SURSKIT_NORMAL
		if _, err := p.detectDitto(scan); err != nil {
			t.Fatal(err)
		}
		if !p.IsDitto {
			t.Error("BN>[0P] with the new species in the pool and nothing confirmed since start: " +
				"not called a Ditto, so the pool is not reaching detectDitto's prior")
		}
	})

	// The species is in the pool but this form is not: a bare entry covers
	// the NORMAL form and form 0, and a Ditto wears nothing else.
	t.Run("species in the pool, form not", func(t *testing.T) {
		withDittoPoolEntries(t, "283")
		p, scan := newRecord(1413)
		if _, err := p.detectDitto(scan); err != nil {
			t.Fatal(err)
		}
		if p.IsDitto {
			t.Error("called a Ditto on the species alone — the pool lists 283:1412 and 283:0, and this scan is form 1413")
		}
	})

	t.Run("in neither the pool nor the confirmed set", func(t *testing.T) {
		withDittoPoolEntries(t, "56")
		p, scan := newRecord(1412)
		if _, err := p.detectDitto(scan); err != nil {
			t.Fatal(err)
		}
		if p.IsDitto {
			t.Error("called a Ditto with nothing saying the species is a disguise")
		}
	})
}

// Both disguise warnings tell the operator how to cover a disguise before the
// published pool does. ditto_pool replaces the published pool, so with no
// override in force the advice must say the whole list goes in: adding the
// one entry alone would shrink the pool to it.
func TestDittoPoolAddAdviceNeverShrinksThePool(t *testing.T) {
	withDittoPoolSources(t, http.StatusOK, "")

	advice := dittoPoolAddAdvice("161:1145")
	if !strings.Contains(advice, "published pool's entries") || !strings.Contains(advice, "replaces the published pool") {
		t.Errorf("with the published pool in force the advice must say to copy the whole list, got: %s", advice)
	}

	config.Config.DittoPool = []string{"56"}
	if advice := dittoPoolAddAdvice("161:1145"); !strings.Contains(advice, `add "161:1145" to ditto_pool`) {
		t.Errorf("with ditto_pool already in force, adding the entry is right, got: %s", advice)
	}
}

// Any ditto_pool entry means the operator chose their own list, so an
// override whose entries are all unusable still overrides — and says loudly
// that the roll now marks nothing, rather than falling back to the published
// list they chose against.
func TestAnUnusableOverrideStillOverridesAndSaysSo(t *testing.T) {
	withDittoPoolSources(t, http.StatusOK, poolJSON([2]int{283, 1412}))
	config.Config.DittoPool = []string{"745", "mankey"} // Lycanroc has no NORMAL form; the other is not a number
	logs := captureStandardLoggerHook(t)

	EnsureDittoPool()

	if inDittoPool(283, 1412) {
		t.Fatal("an override whose entries were all refused fell back to the published pool")
	}
	found := false
	for _, e := range logs.AllEntries() {
		if e.Level == log.ErrorLevel && strings.Contains(e.Message, "none of its 2 entries could be used") {
			found = true
		}
	}
	if !found {
		t.Error("no error saying the override left the roll with nothing to mark")
	}
}

// The published list spells each species twice — form 0 and NORMAL — and a
// bare config entry means both, so a config naming exactly the published
// species matches the same sightings and reports no difference. A config
// written with explicit NORMAL forms does differ, by the form-0 sightings it
// would miss, and the diff says so.
func TestBareConfigEntriesMatchThePublishedSpellings(t *testing.T) {
	withDittoPoolSources(t, http.StatusOK, poolJSON([2]int{56, 0}, [2]int{56, 999}, [2]int{283, 0}, [2]int{283, 1412}))

	config.Config.DittoPool = []string{"56", "283"}
	logs := captureStandardLoggerHook(t)
	EnsureDittoPool()
	if !inDittoPool(283, 0) || !inDittoPool(283, 1412) {
		t.Error("a bare entry does not cover both spellings")
	}
	for _, e := range logs.AllEntries() {
		if strings.Contains(e.Message, "differs from ditto_pool in config") {
			t.Errorf("a config matching the published species reported a difference: %s", e.Message)
		}
	}

	config.Config.DittoPool = []string{"56:999", "283:1412"}
	logs.Reset()
	EnsureDittoPool()
	if inDittoPool(283, 0) {
		t.Error("an explicit NORMAL entry also matched form 0")
	}
	reported := false
	for _, e := range logs.AllEntries() {
		if strings.Contains(e.Message, "differs from ditto_pool in config") && strings.Contains(e.Message, "283:0") {
			reported = true
		}
	}
	if !reported {
		t.Error("the diff did not report the form-0 entries only the published pool has")
	}
}

// With every source failing there is no pool, and the log says so rather than
// naming a source it did not load from.
func TestNoPoolAtAllIsAnErrorNotASource(t *testing.T) {
	withDittoPoolSources(t, http.StatusInternalServerError, "")
	dittoPoolFile.bundled = []byte("not json")
	logs := captureStandardLoggerHook(t)

	EnsureDittoPool()

	if dittoPoolLookup.Load().size() != 0 {
		t.Fatal("fixture assumption violated: nothing could load, so the pool must be empty")
	}
	var sawError bool
	for _, e := range logs.AllEntries() {
		if strings.Contains(e.Message, "ditto pool: ") && e.Level == log.InfoLevel {
			t.Errorf("claimed a source for a pool that did not load: %s", e.Message)
		}
		if e.Level == log.ErrorLevel && strings.Contains(e.Message, "none loaded") {
			sawError = true
		}
	}
	if !sawError {
		t.Error("no error saying no pool loaded")
	}
}
