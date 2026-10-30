package decoder

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"golbat/config"
	"golbat/db"
	"golbat/derive/encounter"
	"golbat/pogo"
	"golbat/stats_collector"

	"github.com/guregu/null/v6"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// withDittoPool rebuilds dittoPoolLookup from ids for the duration of the
// test, restoring the pre-test config.Config.DittoPool (and the lookup built
// from it) on cleanup. Each id means that species' NORMAL form — the same
// thing a bare entry means in the config file. withDittoPoolEntries takes the
// raw "<species>[:<form>]" strings for tests that need a specific form.
//
// Decoder package tests never call config.ReadConfig() or EnsureDittoPool, so
// the pool starts empty in every test binary unless a test sets it. Setting
// the config field is not enough on its own: inDittoPool reads a cache that
// InitDittoPool must be told to rebuild.
func withDittoPool(t *testing.T, ids ...int16) {
	t.Helper()
	entries := make([]string, len(ids))
	for i, id := range ids {
		entries[i] = strconv.Itoa(int(id))
	}
	withDittoPoolEntries(t, entries...)
}

func withDittoPoolEntries(t *testing.T, entries ...string) {
	t.Helper()
	old := config.Config.DittoPool
	config.Config.DittoPool = entries
	InitDittoPool()
	t.Cleanup(func() {
		config.Config.DittoPool = old
		InitDittoPool()
	})
}

// The pool is the set of disguises the roll is allowed to mark.
func TestDittoPoolMembership(t *testing.T) {
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)

	for _, id := range []int16{56, 58, 81, 100, 273, 283, 355} {
		if !inDittoPool(id, normalForm(id)) {
			t.Errorf("species %d should be in the pool", id)
		}
	}
	if inDittoPool(25, normalForm(25)) {
		t.Error("Pikachu is not a Ditto disguise")
	}
}

// derivationCountingCollector embeds the noop collector so every other
// method keeps its normal no-op behavior, and counts the derivation
// counters, which the noop collector discards and so could not be asserted
// against directly.
type derivationCountingCollector struct {
	stats_collector.StatsCollector
	checked               map[string]int
	disagreed             map[string]int
	moveMismatch          map[string]int
	boostBranchDivergence map[string]int
	phaseDiverged         int
}

func newDerivationCountingCollector() *derivationCountingCollector {
	return &derivationCountingCollector{
		StatsCollector:        stats_collector.NewNoopStatsCollector(),
		checked:               map[string]int{},
		disagreed:             map[string]int{},
		moveMismatch:          map[string]int{},
		boostBranchDivergence: map[string]int{},
	}
}

// agreedCount is checked minus disagreed. There is no agreement counter to
// read: every checked encounter either disagrees or does not, so an explicit
// one would carry nothing the other two do not already say.
func (c *derivationCountingCollector) agreedCount(path string) int {
	return c.checked[path] - c.disagreed[path]
}

func (c *derivationCountingCollector) IncDerivationChecked(path string)      { c.checked[path]++ }
func (c *derivationCountingCollector) IncDerivationDisagreed(path string)    { c.disagreed[path]++ }
func (c *derivationCountingCollector) IncDerivationMoveMismatch(path string) { c.moveMismatch[path]++ }
func (c *derivationCountingCollector) IncDerivationBoostBranchDivergence(path string) {
	c.boostBranchDivergence[path]++
}
func (c *derivationCountingCollector) IncDespawnPhaseDiverged() { c.phaseDiverged++ }

func withDerivationCountingCollector(t *testing.T) *derivationCountingCollector {
	t.Helper()
	fake := newDerivationCountingCollector()
	setStatsCollectorForTest(t, fake)
	return fake
}

// derivationLogPrefix is the literal prefix logDerivationOutcome's
// log.Infof call writes ahead of the JSON payload.
const derivationLogPrefix = "[DERIVE] "

// derivationLogFields parses the JSON payload out of a captured log entry's
// Message field.
//
// It reads entry.Message because that is what production renders: Golbat's
// only installed formatter, main.PlainFormatter, renders exactly
// `level + timestamp + entry.Message` and never reads entry.Data.
// logrus/hooks/test populates entry.Data regardless of which logrus call
// produced the entry, so asserting on it cannot distinguish log.WithFields
// (dropped in production) from log.Infof (rendered). PlainFormatter lives in
// package main and cannot be imported here without an import cycle; since its
// body is that one concatenation, entry.Message is equivalent to its output
// minus the level/timestamp prefix.
func derivationLogFields(t *testing.T, entry *logrus.Entry) map[string]any {
	t.Helper()
	msg := entry.Message
	if !strings.HasPrefix(msg, derivationLogPrefix) {
		t.Fatalf("log message missing %q prefix: %s", derivationLogPrefix, msg)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(msg[len(derivationLogPrefix):]), &fields); err != nil {
		t.Fatalf("log message is not valid JSON: %s (message: %q)", err, msg)
	}
	return fields
}

// soleDerivationLogEntry finds exactly one entry carrying the "[DERIVE] "
// prefix among a hook's captured entries, ignoring unrelated log lines
// (e.g. confirmDitto's "[DITTO] Confirmed a Ditto" line, or a "Failed to
// obtain weather" warning) that a real call through addEncounterPokemon can
// also emit. Fails the test if there is not exactly one.
func soleDerivationLogEntry(t *testing.T, entries []logrus.Entry) *logrus.Entry {
	t.Helper()
	var found []*logrus.Entry
	for i := range entries {
		if strings.HasPrefix(entries[i].Message, derivationLogPrefix) {
			found = append(found, &entries[i])
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one [DERIVE] log entry, got %d (of %d total entries)", len(found), len(entries))
	}
	return found[0]
}

// callLogDerivationOutcome computes the derived/oppositeValues/boosted/
// dittoPoolMember/branch parameters of logDerivationOutcome the same way
// checkDerivation does, and forwards to it. Tests use it to log
// unconditionally, bypassing checkDerivation's gate that writes a row only
// for a disagreement (see derivationLogReasons).
// observedMove1/observedMove2 are forwarded unexamined; callers not testing
// moves pass 0, 0.
func callLogDerivationOutcome(pokemon *Pokemon, scan *pokemonScan, observed encounter.Values, path string, wasRollMarked bool, observedMove1, observedMove2 int32) {
	callLogDerivationOutcomeWithEvidence(pokemon, scan, observed, path, wasRollMarked, false, observedMove1, observedMove2)
}

// callLogDerivationOutcomeWithEvidence is callLogDerivationOutcome plus
// explicit control over hadDetectEvidence, for the tests that need to pin
// ditto_detect. callLogDerivationOutcome forwards false.
func callLogDerivationOutcomeWithEvidence(pokemon *Pokemon, scan *pokemonScan, observed encounter.Values, path string, wasRollMarked bool, hadDetectEvidence bool, observedMove1, observedMove2 int32) {
	boosted := scan.Weather != int32(pogo.GameplayWeatherProto_NONE)
	derived := encounter.For(uint64(pokemon.Id), boosted, scan.Form)
	oppositeValues := encounter.For(uint64(pokemon.Id), !boosted, scan.Form).Values
	dittoPoolMember := inDittoPool(int16(scan.Pokemon), int16(scan.Form))
	branch := dittoBranchFor(observed, derived.Values, oppositeValues, scan.Strong)

	var derivedMove1, derivedMove2 int32
	if masterFileData := getMasterFileData(); masterFileData.Initialized {
		if quick, charged, ok := movePoolsFor(masterFileData, int16(scan.Pokemon), int16(scan.Form)); ok {
			quickIndex, chargedIndex := derived.MoveIndices(int32(len(quick)), int32(len(charged)))
			derivedMove1 = int32(quick[quickIndex])
			derivedMove2 = int32(charged[chargedIndex])
		}
	}

	logDerivationOutcome(pokemon, scan, derivationOutcomeParams{
		Observed:          observed,
		Path:              path,
		WasRollMarked:     wasRollMarked,
		HadDetectEvidence: hadDetectEvidence,
		Derived:           derived,
		OppositeValues:    oppositeValues,
		Boosted:           boosted,
		DittoPoolMember:   dittoPoolMember,
		Branch:            branch,
		DerivedMove1:      derivedMove1,
		DerivedMove2:      derivedMove2,
		ObservedMove1:     observedMove1,
		ObservedMove2:     observedMove2,
	})
}

// A correctly-derived, non-Ditto encounter must count as checked+agreed and
// must not be logged: agreements are counted, not logged.
func TestCheckDerivationAgreementCountsAndDoesNotLog(t *testing.T) {
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(111222333444)
	// The species' NORMAL form: what the game reports for it, and what a bare
	// ditto_pool entry resolves to. Pool membership is species AND form.
	const form = int32(dittoRollTestPoolForm16)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: 1, // Bulbasaur: not in the Ditto pool
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, false)

	if fake.checked["encounter"] != 1 {
		t.Fatalf("checked = %d, want 1", fake.checked["encounter"])
	}
	if fake.agreedCount("encounter") != 1 {
		t.Fatalf("agreed = %d, want 1", fake.agreedCount("encounter"))
	}
	if fake.disagreed["encounter"] != 0 {
		t.Fatalf("disagreed = %d, want 0", fake.disagreed["encounter"])
	}
	if len(hook.Entries) != 0 {
		t.Fatalf("expected no log entry on a plain agreement, got %d", len(hook.Entries))
	}
}

// A mismatched, non-Ditto encounter must count as checked+disagreed and must
// be logged, carrying the observed values from the raw scan — asserted on
// the rendered log message, not entry.Data.
func TestCheckDerivationMismatchCountsDisagreedAndLogs(t *testing.T) {
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(222333444555)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  (v.Attack + 1) % 16, // deliberately wrong
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: 1,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, false)

	if fake.checked["encounter"] != 1 {
		t.Fatalf("checked = %d, want 1", fake.checked["encounter"])
	}
	if fake.agreedCount("encounter") != 0 {
		t.Fatalf("agreed = %d, want 0", fake.agreedCount("encounter"))
	}
	if fake.disagreed["encounter"] != 1 {
		t.Fatalf("disagreed = %d, want 1", fake.disagreed["encounter"])
	}
	if len(hook.Entries) != 1 {
		t.Fatalf("expected exactly one log entry on a mismatch, got %d", len(hook.Entries))
	}
	fields := derivationLogFields(t, &hook.Entries[0])
	if got := fields["observed_attack"]; got != float64(scan.Attack) {
		t.Errorf("observed_attack = %v, want %v", got, scan.Attack)
	}
	if got := fields["path"]; got != "encounter" {
		t.Errorf("path = %v, want encounter", got)
	}
}

// A pool disguise whose derivation agrees and whose Ditto signals agree is
// counted, and not logged: only disagreements are written, and a row where
// everything concurs says nothing about which signal is wrong.
func TestCheckDerivationDittoPoolAgreementIsCountedNotLogged(t *testing.T) {
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(333444555666)
	// The species' NORMAL form: pool membership is species AND form, and a
	// bare ditto_pool entry resolves to the normal one.
	const form = int32(dittoRollTestPoolForm16)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: 56, // Ditto disguise pool member
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, false)

	if fake.agreedCount("encounter") != 1 {
		t.Fatalf("agreed = %d, want 1 (a correctly-derived Ditto still agrees)", fake.agreedCount("encounter"))
	}
	if fake.disagreed["encounter"] != 0 {
		t.Fatalf("disagreed = %d, want 0", fake.disagreed["encounter"])
	}
	if len(hook.Entries) != 0 {
		t.Fatalf("a pool disguise with everything agreeing was logged (%d entries); only disagreements are", len(hook.Entries))
	}
}

// A strong Pokemon must not pollute derivation_checked/agreed/disagreed_total,
// but must still be logged with is_strong true.
func TestCheckDerivationStrongPokemonExcludedFromCountersButLogged(t *testing.T) {
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(444555666777)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  (v.Attack + 1) % 16, // strong Pokemon routinely mismatch
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: 1,
		Form:    form,
		Strong:  true,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, false)

	if fake.checked["encounter"] != 0 || fake.agreedCount("encounter") != 0 || fake.disagreed["encounter"] != 0 {
		t.Fatalf("strong Pokemon must not touch the counters: checked=%d agreed=%d disagreed=%d",
			fake.checked["encounter"], fake.agreedCount("encounter"), fake.disagreed["encounter"])
	}
	if len(hook.Entries) != 1 {
		t.Fatalf("expected exactly one log entry for the strong mismatch, got %d", len(hook.Entries))
	}
	fields := derivationLogFields(t, &hook.Entries[0])
	if got := fields["is_strong"]; got != true {
		t.Errorf("is_strong = %v, want true", got)
	}
}

// is_ditto and display_pokemon_id must reflect pokemon's post-detectDitto
// ground truth, not the raw scan — that is the whole reason
// logDerivationOutcome takes both pokemon and scan. This pins that split
// directly: pokemon and scan deliberately disagree, and each logged field
// must come from the side the doc comment says it does.
func TestLogDerivationOutcomeReadsDittoFieldsFromPokemonAndDisplayFieldsFromScan(t *testing.T) {
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(555666777888)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Pokemon: 133, // Eevee: the raw display this round
		Form:    7,
		Costume: 2,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.SetIsDitto(true)
	p.SetDisplayPokemonId(null.IntFrom(133))

	callLogDerivationOutcome(p, scan, encounter.Values{}, "encounter", false, 0, 0)

	if len(hook.Entries) != 1 {
		t.Fatalf("expected exactly one log entry, got %d", len(hook.Entries))
	}
	fields := derivationLogFields(t, &hook.Entries[0])
	if got := fields["is_ditto"]; got != true {
		t.Errorf("is_ditto = %v, want true (from pokemon, post-detectDitto)", got)
	}
	if got := fields["displayed_species"]; got != float64(133) {
		t.Errorf("displayed_species = %v, want 133 (from scan, the raw display)", got)
	}
	if got := fields["form"]; got != float64(7) {
		t.Errorf("form = %v, want 7 (from scan)", got)
	}
	if got := fields["costume"]; got != float64(2) {
		t.Errorf("costume = %v, want 2 (from scan)", got)
	}
}

// The corpus-critical fields — the Ditto roll, both derived
// branches, and cell_weather sourced specifically from scan.CellWeather
// rather than scan.Weather — are pinned individually here, each against an
// independently computed expectation, with scan.Weather and scan.CellWeather
// deliberately set to different values so a source swap is visible.
func TestLogDerivationOutcomePayloadFields(t *testing.T) {
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(888999111222)
	const form = int32(9)
	wantRoll := encounter.DittoCandidateRoll(eid)
	wantUnboosted := encounter.For(eid, false, form)
	wantBoosted := encounter.For(eid, true, form)

	scan := &pokemonScan{
		Weather:     int32(pogo.GameplayWeatherProto_NONE),  // this round's boost condition
		CellWeather: int32(pogo.GameplayWeatherProto_RAINY), // deliberately different from Weather
		Pokemon:     133,
		Form:        form,
		Costume:     4,
		Attack:      1, Defense: 2, Stamina: 3, Level: 4,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	observed := encounter.Values{Attack: 1, Defense: 2, Stamina: 3, Level: 4, Size: 7}
	callLogDerivationOutcome(p, scan, observed, "encounter", false, 0, 0)

	if len(hook.Entries) != 1 {
		t.Fatalf("expected exactly one log entry, got %d", len(hook.Entries))
	}
	fields := derivationLogFields(t, &hook.Entries[0])

	// ditto_roll is logged as a float32, but json.Unmarshal into map[string]any
	// always decodes a JSON number as float64 — the shortest decimal that
	// round-trips the float32 does not, itself, equal float64(wantRoll) bit
	// for bit (float64 has far more precision than the float32 the decimal
	// was chosen for), so the comparison must convert the decoded value back
	// down to float32, which does recover the original exactly.
	gotRoll, ok := fields["ditto_roll"].(float64)
	if !ok || float32(gotRoll) != wantRoll {
		t.Errorf("ditto_roll = %v, want %v", fields["ditto_roll"], wantRoll)
	}

	if got := fields["cell_weather"]; got != float64(pogo.GameplayWeatherProto_RAINY) {
		t.Errorf("cell_weather = %v, want %v (scan.CellWeather, not scan.Weather=%v)",
			got, int32(pogo.GameplayWeatherProto_RAINY), int32(pogo.GameplayWeatherProto_NONE))
	}

	unboosted, ok := fields["derived_unboosted"].(map[string]any)
	if !ok {
		t.Fatalf("derived_unboosted missing or wrong shape: %v", fields["derived_unboosted"])
	}
	if got := unboosted["attack"]; got != float64(wantUnboosted.Attack) {
		t.Errorf("derived_unboosted.attack = %v, want %v", got, wantUnboosted.Attack)
	}
	if got := unboosted["level"]; got != float64(wantUnboosted.Level) {
		t.Errorf("derived_unboosted.level = %v, want %v", got, wantUnboosted.Level)
	}

	boosted, ok := fields["derived_boosted"].(map[string]any)
	if !ok {
		t.Fatalf("derived_boosted missing or wrong shape: %v", fields["derived_boosted"])
	}
	if got := boosted["attack"]; got != float64(wantBoosted.Attack) {
		t.Errorf("derived_boosted.attack = %v, want %v", got, wantBoosted.Attack)
	}
	if got := boosted["level"]; got != float64(wantBoosted.Level) {
		t.Errorf("derived_boosted.level = %v, want %v", got, wantBoosted.Level)
	}

	// observed_size must be logged so a size-only
	// disagreement is explainable from the row alone.
	if got := fields["observed_size"]; got != float64(7) {
		t.Errorf("observed_size = %v, want 7", got)
	}
}

// proto.Size 0 means POKEMON_SIZE_UNSET — "not reported," not "disagrees."
// An otherwise perfect agreement with size left at 0 must still count as an
// agreement, not poison derivation_disagreed_total with a permanent floor from
// every proto that omits size.
func TestCheckDerivationIgnoresUnsetSize(t *testing.T) {
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(999111222333)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: 1,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, 0, 0, 0, "encounter", false, false) // size unset

	if fake.agreedCount("encounter") != 1 {
		t.Fatalf("agreed = %d, want 1 (an unset size must not cause a disagreement)", fake.agreedCount("encounter"))
	}
	if fake.disagreed["encounter"] != 0 {
		t.Fatalf("disagreed = %d, want 0", fake.disagreed["encounter"])
	}
	if len(hook.Entries) != 0 {
		t.Fatalf("expected no log entry, got %d", len(hook.Entries))
	}
}

// The flip side of the above: a genuinely wrong REPORTED size (not merely
// unset) must still count as a disagreement: the unset exclusion narrows the
// size check, it does not disable it.
func TestCheckDerivationCountsAGenuineSizeMismatch(t *testing.T) {
	fake := withDerivationCountingCollector(t)

	const eid = uint64(111222333999)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: 1,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	wrongSize := v.Size + 1
	if wrongSize > encounter.SizeXXL {
		wrongSize = v.Size - 1
	}

	p.checkDerivation(scan, wrongSize, 0, 0, "encounter", false, false)

	if fake.disagreed["encounter"] != 1 {
		t.Fatalf("disagreed = %d, want 1 (a genuinely wrong non-zero reported size must still count as a disagreement)",
			fake.disagreed["encounter"])
	}
}

// checkDerivation is a method, so `unused` cannot flag a deleted call to it,
// and the tests above call it directly rather than through its only
// production entry point. This drives an encounter through
// addEncounterPokemon (a fresh Pokemon with no scan history, so detectDitto
// returns the scan unchanged and takes no error path) and asserts the counter
// moved, so it fails if addEncounterPokemon stops calling checkDerivation.
func TestAddEncounterPokemonReachesDerivationCounters(t *testing.T) {
	fake := withDerivationCountingCollector(t)

	const eid = uint64(777888999111)
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

	proto := &pogo.PokemonProto{
		PokemonId:         1, // Bulbasaur: not in the Ditto pool
		IndividualAttack:  10,
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      0.5,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if fake.checked["encounter"] != 1 {
		t.Fatalf("IncDerivationChecked(\"encounter\") count = %d, want 1 — "+
			"the call site in addEncounterPokemon may be missing or misrouted", fake.checked["encounter"])
	}
}

// cpMultiplierToLevel mirrors addEncounterPokemon's CpMultiplier -> Level
// formula exactly, so a test fixture can pick a target Level without
// inverting it. It is fixture construction only and does not test the
// formula itself.
func cpMultiplierToLevel(cpMultiplier float32) int32 {
	if cpMultiplier < 0.734 {
		return int32((58.215688455154954*cpMultiplier-2.7012478057856497)*cpMultiplier + 1.3220677708486794)
	} else if cpMultiplier < .795 {
		return int32(171.34093607855277*cpMultiplier - 94.95626666368578)
	}
	return int32(199.99995231630976*cpMultiplier - 117.55996066890287)
}

// Encounter ids exceed 2^53 (the golden vector used throughout this file,
// 8429844873227918432, is one), so a bare JSON number is silently truncated by float64 parsers — every common
// consumer (jq before 1.7, JavaScript, Go's own map[string]any) decodes
// JSON numbers as float64. encounter_id is the corpus's join key, so it
// must render as an exact, quoted JSON string.
//
// This asserts on the RENDERED BYTES specifically, via strings.Contains on
// entry.Message, rather than by unmarshaling into a struct or map carrying
// the same ",string" tag/behavior — that would pass whether or not the tag
// on the production struct does anything, because it would round-trip
// through the very mechanism under test either way.
func TestLogDerivationOutcomeEncodesEncounterIdAsExactJSONString(t *testing.T) {
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(8429844873227918432) // > 2^53
	scan := &pokemonScan{Weather: int32(pogo.GameplayWeatherProto_NONE), Pokemon: 56, Form: int32(dittoRollTestPoolForm16)}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	callLogDerivationOutcome(p, scan, encounter.Values{}, "encounter", false, 0, 0)

	entry := soleDerivationLogEntry(t, hook.Entries)
	const want = `"encounter_id":"8429844873227918432"`
	if !strings.Contains(entry.Message, want) {
		t.Fatalf("rendered log message does not contain %s (an exact, quoted encounter_id): %s", want, entry.Message)
	}
}

// Pins the snapshot ordering — observedScan := scan must be
// taken BEFORE pokemon.detectDitto(&scan) runs — against detectDitto's
// riskiest failure mode for it: the ">0P" branch (inside detectDitto,
// reached when a Ditto is confirmed for the first time from a stored
// boosted scan whose Level happens to equal this round's unboosted Level)
// mutates scan.Weather in place from NONE to PARTLY_CLOUDY, to record that
// the Ditto's TRUE weather category differed from what its disguise
// displayed. If the snapshot captured that mutated value instead of the
// raw display, both the boost branch checkDerivation compares against and
// the logged weather_boosted_condition would silently flip for exactly the
// Ditto population — the corpus's most important rows.
//
// This constructs that exact scenario directly (scanHistory seeded so
// locateAllScans finds both an unboostedScan and a boostedScan, with
// boostedScan.Level equal to what this round's CpMultiplier will derive)
// and asserts the logged weather_boosted_condition is NONE — what this
// round's disguise actually displayed — not the Ditto's reclassified
// PARTLY_CLOUDY.
func TestAddEncounterPokemonSnapshotsScanBeforeDetectDittoMutatesWeather(t *testing.T) {
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(666777888999)
	const cpMultiplier = float32(0.5) // first branch of cpMultiplierToLevel
	wantLevel := cpMultiplierToLevel(cpMultiplier)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.PokemonId = 56 // a Ditto confirmed on an encounter whose roll did not fire logs as roll_missed_confirmed
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))) // this round: disguise NOT boosted
	p.scanHistory = []*pokemonScan{
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY), Level: wantLevel, Attack: 5, Defense: 5, Stamina: 5, Pokemon: 56, Form: int32(dittoRollTestPoolForm16)},
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_NONE), Level: wantLevel - 1, Attack: 5, Defense: 5, Stamina: 5, Pokemon: 56, Form: int32(dittoRollTestPoolForm16)},
	}

	proto := &pogo.PokemonProto{
		PokemonId:         56,
		IndividualAttack:  5,
		IndividualDefense: 5,
		IndividualStamina: 5,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if !p.IsDitto {
		t.Fatal("setup did not confirm a Ditto (detectDitto's \">0P\" branch did not fire) — " +
			"the test fixture is wrong, not necessarily the code under test")
	}

	fields := derivationLogFields(t, soleDerivationLogEntry(t, hook.Entries))
	want := float64(pogo.GameplayWeatherProto_NONE)
	if got := fields["weather_boosted_condition"]; got != want {
		t.Fatalf("weather_boosted_condition = %v, want %v (NONE, what this round actually displayed) — "+
			"got the Ditto's reclassified PARTLY_CLOUDY (%v) instead, meaning the pre-detectDitto snapshot was taken "+
			"AFTER detectDitto mutated scan.Weather",
			got, want, float64(pogo.GameplayWeatherProto_PARTLY_CLOUDY))
	}
}

// TestAddEncounterPokemonConfirmedDittoOutsidePoolIsLogged drives a Ditto
// confirmation (detectDitto's ">0P" branch — the same branch
// TestAddEncounterPokemonSnapshotsScanBeforeDetectDittoMutatesWeather
// constructs) through addEncounterPokemon end to end. The disguise is outside
// the pool, so the roll cannot fire for it, and detectDitto confirming it from
// evidence is a Ditto the roll missed: the row is logged as
// roll_missed_confirmed.
//
// The encounter id is chosen so this round's values agree with encounter.For
// in every field checkDerivation compares, so the row cannot be logged as a
// mismatch instead. Asserted on the RENDERED log message, not entry.Data: the
// app installs a formatter that renders only entry.Message, so an assertion
// on entry.Data passes while production emits nothing.
func TestAddEncounterPokemonConfirmedDittoOutsidePoolIsLogged(t *testing.T) {
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const disguise = 25 // Pikachu
	if inDittoPool(int16(disguise), normalForm(int16(disguise))) {
		t.Fatal("test fixture assumption violated: species 25 is now a dittoPool member; " +
			"this test needs a disguise outside the pool")
	}

	const cpMultiplier = float32(0.5) // first (quadratic) branch of cpMultiplierToLevel
	wantLevel := cpMultiplierToLevel(cpMultiplier)

	// Find an encounter id whose UNBOOSTED derivation lands on exactly
	// wantLevel, so this round's raw display can be made to agree with
	// encounter.For in every field checkDerivation compares, so the row is not
	// a mismatch.
	var eid uint64
	var derived encounter.Values
	found := false
	for candidate := uint64(1); candidate < 200000; candidate++ {
		v := encounter.For(candidate, false, 0)
		if v.Level == wantLevel {
			eid, derived, found = candidate, v.Values, true
			break
		}
	}
	if !found {
		t.Fatal("could not find an encounter id whose unboosted derivation matches wantLevel in the search range")
	}

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.PokemonId = disguise
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))) // this round: disguise NOT boosted
	p.scanHistory = []*pokemonScan{
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY), Level: wantLevel, Attack: 5, Defense: 5, Stamina: 5, Pokemon: disguise},
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_NONE), Level: wantLevel - 1, Attack: 5, Defense: 5, Stamina: 5, Pokemon: disguise},
	}

	proto := &pogo.PokemonProto{
		PokemonId:         disguise,
		IndividualAttack:  derived.Attack,
		IndividualDefense: derived.Defense,
		IndividualStamina: derived.Stamina,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if !p.IsDitto {
		t.Fatal("setup did not confirm a Ditto (detectDitto's \">0P\" branch did not fire) — " +
			"the test fixture is wrong, not necessarily the code under test")
	}

	entry := soleDerivationLogEntry(t, hook.Entries)
	const want = `"is_ditto":true`
	if !strings.Contains(entry.Message, want) {
		t.Fatalf("rendered log message does not contain %s (production's PlainFormatter renders only "+
			"entry.Message): %s", want, entry.Message)
	}

	// This Ditto was confirmed by detectDitto's OWN ">0P" branch from real
	// scan history — p.dittoRollMarked was never set — so ditto_roll_marked
	// must be false even though is_ditto is true. is_ditto alone cannot make
	// this distinction; this field must.
	const wantNotRollMarked = `"ditto_roll_marked":false`
	if !strings.Contains(entry.Message, wantNotRollMarked) {
		t.Fatalf("rendered log message does not contain %s — a genuine detectDitto confirmation "+
			"must not be recorded as roll-marked: %s", wantNotRollMarked, entry.Message)
	}
}

// TestLogDerivationOutcomeRecordsRollMarkingSurvivingMissingHistory covers
// auditability for a roll-marked record's FIRST real encounter, driven
// through the real addEncounterPokemon call site: a roll-marked record meets
// its first encounter with no scan history for detectDitto to work from, and
// discarding the mark there would be silent and unrecoverable. detectDitto's
// missing-history branch KEEPS a roll-marked record (see its doc comment);
// this test pins that outcome — both p.IsDitto and the logged is_ditto stay
// true — through the real call site, so a placement or ordering fault in
// addEncounterPokemon's wiring, not just in detectDitto, is caught.
//
// checkDerivation's wasRollMarked parameter is a snapshot taken BEFORE
// detectDitto runs (see its doc comment). dittoRollMarked is true both before
// and after detectDitto here, so this test alone would not catch a live read;
// TestAddEncounterPokemonSnapshotsScanBeforeDetectDittoMutatesWeather pins
// the ordering (via scan.Weather, mutated by a DIFFERENT detectDitto branch),
// and TestDetectDittoRollMarkedRecordWithHistoryUsesNormalDittoLogic covers a
// roll-marked record that IS reset by real cross-boost evidence, where the
// snapshot matters.
func TestLogDerivationOutcomeRecordsRollMarkingSurvivingMissingHistory(t *testing.T) {
	const disguise = int16(56)
	withDittoPool(t, disguise)

	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(555666777888)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(eid),
			IsDitto:          true,
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
		},
		dittoRollMarked: true,
	}
	p.SetSeenType(SeenTypeCodeEncounter)

	proto := &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(disguise),
		IndividualAttack:  10,
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      0.5,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if !p.IsDitto {
		t.Fatal("expected IsDitto to remain true — a first encounter with no usable prior scan " +
			"cannot verify a Ditto, and inability to verify is not evidence of refutation")
	}
	if !p.dittoRollMarked {
		t.Fatal("expected dittoRollMarked to remain true — this record's origin is still the roll")
	}
	if p.PokemonId != int16(pogo.HoloPokemonId_DITTO) {
		t.Fatalf("PokemonId = %d, want %d (DITTO) — must not have been reverted to the disguise",
			p.PokemonId, int16(pogo.HoloPokemonId_DITTO))
	}

	entry := soleDerivationLogEntry(t, hook.Entries)
	fields := derivationLogFields(t, entry)
	if got := fields["ditto_roll_marked"]; got != true {
		t.Errorf("ditto_roll_marked = %v, want true", got)
	}
	if got := fields["is_ditto"]; got != true {
		t.Errorf("is_ditto = %v, want true — must match the kept (not reset) record", got)
	}
}

// dittoDetectFixtureSpecies is an arbitrary, stable disguise id for the
// ditto_detect fixtures below. Those fixtures read ditto_detect off the
// logged row, and their rows are logged as mismatches: each round's values
// are chosen for detectDitto, not to agree with encounter.For. A fixture
// whose values happened to agree would log nothing and fail at
// soleDerivationLogEntry, not pass vacuously.
const dittoDetectFixtureSpecies = int16(1)

// TestLogDerivationOutcomeDittoDetectRejectedWithBothBoostStatesInHistory
// covers ditto_detect's "rejected" state end to end through
// addEncounterPokemon: prior scan history already holds both an unboosted
// and a boosted (non-strong) scan — exactly hadCrossBoostScanHistory's
// condition — and this round's own observation is consistent with the
// unboosted one, so detectDitto's "both IVs present" branch takes the plain
// checkScans path, never calling
// setDittoAttributes. pokemon.IsDitto must end up false and ditto_detect
// must read "rejected": a genuine, evidence-backed negative, not
// "no_evidence".
func TestLogDerivationOutcomeDittoDetectRejectedWithBothBoostStatesInHistory(t *testing.T) {
	withDittoPool(t, dittoDetectFixtureSpecies)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(212223242526)
	const cpMultiplier = float32(0.5)
	wantLevel := cpMultiplierToLevel(cpMultiplier) // this round's own (unboosted) level

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.PokemonId = int16(dittoDetectFixtureSpecies)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))) // this round: unboosted
	p.scanHistory = []*pokemonScan{
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY), Level: wantLevel + 5,
			Attack: 10, Defense: 10, Stamina: 10, Pokemon: int32(dittoDetectFixtureSpecies)},
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_NONE), Level: wantLevel,
			Attack: 10, Defense: 10, Stamina: 10, Pokemon: int32(dittoDetectFixtureSpecies)},
	}

	proto := &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(dittoDetectFixtureSpecies),
		IndividualAttack:  10, // matches the unboosted history scan: checkScans must not error
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if p.IsDitto {
		t.Fatal("test fixture is wrong, not necessarily the code under test: this round's level " +
			"matches the UNBOOSTED history scan while itself being unboosted, which detectDitto's " +
			"\"both IVs present\" branch must read as a plain (non-Ditto) re-sighting")
	}

	fields := derivationLogFields(t, soleDerivationLogEntry(t, hook.Entries))
	if got := fields["ditto_detect"]; got != "rejected" {
		t.Fatalf("ditto_detect = %v, want rejected — both an unboosted and a boosted scan were "+
			"already in history when detectDitto ran, which is exactly the cross-boost evidence "+
			"that makes a \"not a Ditto\" conclusion authoritative", got)
	}
}

// TestLogDerivationOutcomeDittoDetectNoEvidenceWithNoScanHistory covers
// ditto_detect's "no_evidence" state for the simplest case: a record with no
// scan history at all (a genuine first sighting). detectDitto's own
// no-history, non-Ditto path never calls setDittoAttributes, so
// pokemon.IsDitto stays false, and ditto_detect must read "no_evidence" —
// not "rejected", since detectDitto had nothing whatsoever to compare
// against.
func TestLogDerivationOutcomeDittoDetectNoEvidenceWithNoScanHistory(t *testing.T) {
	withDittoPool(t, dittoDetectFixtureSpecies)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(313233343536)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.PokemonId = int16(dittoDetectFixtureSpecies)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

	proto := &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(dittoDetectFixtureSpecies),
		IndividualAttack:  10,
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      0.5,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if p.IsDitto {
		t.Fatal("test fixture is wrong, not necessarily the code under test: a first-ever " +
			"sighting has no history for detectDitto to confirm a Ditto from")
	}
	if len(p.scanHistory) == 0 {
		t.Fatal("test fixture is wrong: addEncounterPokemon should have appended this round's own scan")
	}

	fields := derivationLogFields(t, soleDerivationLogEntry(t, hook.Entries))
	if got := fields["ditto_detect"]; got != "no_evidence" {
		t.Fatalf("ditto_detect = %v, want no_evidence — there was no prior scan history at all, "+
			"so detectDitto's \"not a Ditto\" outcome carries no information", got)
	}
}

// TestLogDerivationOutcomeDittoDetectNoEvidenceWithSingleBoostStateHistory is
// THE case ditto_detect exists for: prior history holds a scan in only ONE
// boost state, and this round's encounter lands in the OTHER boost state —
// the normal weather-boost transition (level shifts by exactly 5), which
// detectDitto's "switch scan.Level - (matchingScan.Level + levelAdjustment)
// { case 0: }" arm treats as an ordinary re-sighting, not a Ditto.
//
// It also guards hadCrossBoostScanHistory's timing requirement:
// addEncounterPokemon folds this round's scan into pokemon.scanHistory AFTER
// detectDitto returns, so by the time the assertions run, scanHistory holds
// BOTH boost states. A ditto_detect computed from a live post-encounter
// scan-history check, instead of the pre-detectDitto snapshot
// hadCrossBoostScanHistory takes, would misread this fixture as "rejected".
// See hadCrossBoostScanHistory's doc comment.
func TestLogDerivationOutcomeDittoDetectNoEvidenceWithSingleBoostStateHistory(t *testing.T) {
	withDittoPool(t, dittoDetectFixtureSpecies)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(414243444546)
	const cpMultiplier = float32(0.5)
	wantLevel := cpMultiplierToLevel(cpMultiplier) // this round's own (boosted) level

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.PokemonId = int16(dittoDetectFixtureSpecies)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY))) // this round: boosted
	p.scanHistory = []*pokemonScan{
		// Only ONE boost state on file: unboosted, exactly 5 levels below
		// what this round (boosted) will report — the ordinary weather-boost
		// relationship, not a Ditto tell.
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_NONE), Level: wantLevel - 5,
			Attack: 10, Defense: 10, Stamina: 10, Pokemon: int32(dittoDetectFixtureSpecies)},
	}

	proto := &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(dittoDetectFixtureSpecies),
		IndividualAttack:  10,
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if p.IsDitto {
		t.Fatal("test fixture is wrong, not necessarily the code under test: a level shift of " +
			"exactly +5 into the boosted state is the ordinary, expected transition, which " +
			"detectDitto's case-0 arm must not read as a Ditto")
	}
	if len(p.scanHistory) < 2 {
		t.Fatal("test fixture is wrong: addEncounterPokemon should have appended this round's own " +
			"(boosted) scan alongside the pre-existing unboosted one, giving scanHistory both boost " +
			"states AFTER this call — which is exactly the trap this test exists to catch")
	}

	fields := derivationLogFields(t, soleDerivationLogEntry(t, hook.Entries))
	if got := fields["ditto_detect"]; got != "no_evidence" {
		t.Fatalf("ditto_detect = %v, want no_evidence — prior history held only ONE boost state "+
			"when detectDitto ran; it must not be read as \"rejected\" merely because THIS round's "+
			"own scan happens to supply the other one", got)
	}
}

// TestLogDerivationOutcomeDittoDetectConfirmedRegardlessOfEvidence covers
// ditto_detect's "confirmed" state via a roll-marked record on its first
// real encounter — pokemon.IsDitto and dittoRollMarked already true, no
// scan history at all (see dittoRollMarked, and detectDitto's
// missing-history KEEP branch). It has NO cross-boost evidence, so it also
// pins the priority order documented on logDerivationOutcome:
// pokemon.IsDitto must win outright over hadDetectEvidence, not merely agree
// with it.
func TestLogDerivationOutcomeDittoDetectConfirmedRegardlessOfEvidence(t *testing.T) {
	const disguise = int16(56)

	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(515253545556)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(eid),
			IsDitto:          true,
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
		},
		dittoRollMarked: true,
	}
	p.SetSeenType(SeenTypeCodeEncounter)

	proto := &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(disguise),
		IndividualAttack:  10,
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      0.5,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if !p.IsDitto {
		t.Fatal("test fixture is wrong, not necessarily the code under test: a roll-marked record's " +
			"first encounter, with no prior history to contradict it, must keep IsDitto set (see " +
			"detectDitto's missing-history KEEP branch)")
	}

	fields := derivationLogFields(t, soleDerivationLogEntry(t, hook.Entries))
	if got := fields["ditto_detect"]; got != "confirmed" {
		t.Fatalf("ditto_detect = %v, want confirmed — pokemon.IsDitto is set, which must decide "+
			"ditto_detect on its own regardless of there being no cross-boost history at all", got)
	}
}

// TestHadCrossBoostScanHistoryHydratesFromGolbatInternal pins
// hadCrossBoostScanHistory's hydration requirement: a record loaded fresh
// from the database carries its scan history as still-unparsed
// GolbatInternal bytes, not yet decoded into pokemon.scanHistory. An
// implementation that inspected pokemon.scanHistory directly (or skipped
// populateInternal) would see a nil slice and wrongly report false — the
// same hydration trap
// TestMaybeMarkDittoFromRollScanHistoryGateHydratesFromGolbatInternal pins
// for the roll's scan-history gate. legacyGolbatInternalHex is not usable
// here: it carries a single non-strong (unboosted) scan, which reports false
// hydrated or not (see wantScanHistory), so this builds a two-boost-state
// fixture instead.
func TestHadCrossBoostScanHistoryHydratesFromGolbatInternal(t *testing.T) {
	history := []*pokemonScan{
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_NONE), Level: 20, Attack: 10, Defense: 10, Stamina: 10, Pokemon: 1},
		{Strong: false, Weather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY), Level: 25, Attack: 10, Defense: 10, Stamina: 10, Pokemon: 1},
	}
	marshaled, err := proto.Marshal(scanHistoryToProto(history, false))
	if err != nil {
		t.Fatalf("failed to marshal fixture history: %s", err)
	}

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(1)}}
	p.GolbatInternal = marshaled // persisted history; scanHistory not yet hydrated in memory

	if got := p.hadCrossBoostScanHistory(); !got {
		t.Fatal("hadCrossBoostScanHistory() = false, want true — GolbatInternal carried both an " +
			"unboosted and a boosted scan; this must hydrate (via locateAllScans/populateInternal) " +
			"before deciding, not read pokemon.scanHistory as it stood before this call")
	}
}

// TestDittoSuspectRollTrueAndFalse pins ditto_suspect_roll for two encounter
// ids whose advance 3 (encounter.DittoCandidateRoll) was computed
// independently of this package, the same way
// encounter.TestDittoCandidateRollMatchesReferenceVector's reference vector
// was: a from-scratch Python port of java.util.Random (seed scramble, LCG
// step, next(24) = seed >> 24), salted with encounter.EncounterXor
// (0x18ADE5CD737B), reduced to a float32 exactly as nextFloat does.
//
//   - 8429844873227918432 (the reference-vector encounter id):
//     advance 3 = next(24) 0x09523c -> float32 0.036411..., ABOVE
//     dittoCandidateThreshold (0.02) -> ditto_suspect_roll must be false.
//   - 8429844873227918442: advance 3 = next(24) 0x04e6aa -> float32
//     0.019145..., BELOW threshold -> ditto_suspect_roll must be true. This
//     id's advance 2 is NOT below threshold, so the assertion below fails
//     if the candidate rule reads advance 2 instead of 3.
//
// Both cases are asserted on the RENDERED log message, not entry.Data.
func TestDittoSuspectRollTrueAndFalse(t *testing.T) {
	for _, tc := range []struct {
		name string
		eid  uint64
		want bool
	}{
		{"advance 3 above threshold", 8429844873227918432, false},
		{"advance 3 below threshold", 8429844873227918442, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDerivationLog(t)
			hook := captureStandardLoggerHook(t)

			scan := &pokemonScan{
				Weather: int32(pogo.GameplayWeatherProto_NONE),
				Pokemon: 1, // Bulbasaur: not a dittoPool member
			}
			p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(tc.eid)}}

			callLogDerivationOutcome(p, scan, encounter.Values{}, "encounter", false, 0, 0)

			if len(hook.Entries) != 1 {
				t.Fatalf("expected exactly one log entry, got %d", len(hook.Entries))
			}
			fields := derivationLogFields(t, &hook.Entries[0])
			if got := fields["ditto_suspect_roll"]; got != tc.want {
				t.Errorf("ditto_suspect_roll = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDittoSuspectRollDoesNotDependOnPoolMembership drives the SAME
// encounter id (so the same roll, deterministically) through two
// displayed species — one a dittoPool member (56), one not (1) — and
// requires ditto_suspect_roll to come out identical either way.
// dittoPoolMember is reported separately, so this field does not repeat that
// gate. Adding "&& dittoPoolMember" to
// dittoSuspectRoll's computation would make the non-pool-member case log
// false here instead of true.
func TestDittoSuspectRollDoesNotDependOnPoolMembership(t *testing.T) {
	const eid = uint64(8429844873227918442) // advance 3 below threshold (see above)

	for _, tc := range []struct {
		name    string
		species int32
	}{
		{"pool member", 56},
		{"not a pool member", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDerivationLog(t)
			hook := captureStandardLoggerHook(t)

			scan := &pokemonScan{
				Weather: int32(pogo.GameplayWeatherProto_NONE),
				Pokemon: tc.species,
			}
			p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

			callLogDerivationOutcome(p, scan, encounter.Values{}, "encounter", false, 0, 0)

			if len(hook.Entries) != 1 {
				t.Fatalf("expected exactly one log entry, got %d", len(hook.Entries))
			}
			fields := derivationLogFields(t, &hook.Entries[0])
			if got := fields["ditto_suspect_roll"]; got != true {
				t.Errorf("%s: ditto_suspect_roll = %v, want true regardless of pool membership", tc.name, got)
			}
		})
	}
}

// TestDittoSuspectRollExactBoundary pins dittoSuspectRoll's decision boundary
// to the exact integer next(24) it reduces to — 335544 accepted, 335545
// not — rather than to the float32 literal 0.02 that expresses it in code.
// See dittoCandidateThreshold's own doc comment for the derivation:
// float32(0.02) = 0.01999999955296516418, × 2^24 = 335544.3125, so the
// comparison accepts next(24) <= 335544 and rejects next(24) >= 335545.
//
// The two roll values here are constructed directly as next(24)/2^24 in
// float32 arithmetic — independent of encounter.DittoCandidateRoll and of
// javarandom.Random entirely — so this test does not depend on the PRNG
// producing either boundary value for some encounter id; it pins the
// boundary of dittoSuspectRoll itself, in isolation.
//
// An exact integer bound is what makes this assertion expressible at all.
// A float64 threshold (1.0/50.0 against next(32)/2^32) has no exact integer
// boundary to pin: its nearest analogous values line up only with next(32)
// values 175 wide near the crossing. An effective bound off by one (335544 or
// 335546 instead of 335545) flips one of the two assertions below.
func TestDittoSuspectRollExactBoundary(t *testing.T) {
	const (
		lastCandidateNext24     = 335544
		firstNonCandidateNext24 = 335545
	)
	candidateRoll := float32(lastCandidateNext24) / float32(1<<24)
	nonCandidateRoll := float32(firstNonCandidateNext24) / float32(1<<24)

	if !dittoSuspectRoll(candidateRoll) {
		t.Errorf("next(24) = %d (roll %v): dittoSuspectRoll = false, want true (candidate)",
			lastCandidateNext24, candidateRoll)
	}
	if dittoSuspectRoll(nonCandidateRoll) {
		t.Errorf("next(24) = %d (roll %v): dittoSuspectRoll = true, want false (not a candidate)",
			firstNonCandidateNext24, nonCandidateRoll)
	}
}

// withDerivationLog turns the [DERIVE] validation log on for one test and
// restores the previous setting afterwards. It is off by default in
// production (config.Config.DerivationLog), so a test asserting on log
// output — or asserting that a particular row does NOT log — has to enable
// it explicitly, or it would pass merely because logging was off.
func withDerivationLog(t *testing.T) {
	t.Helper()
	prev := config.Config.DerivationLog
	config.Config.DerivationLog = true
	t.Cleanup(func() { config.Config.DerivationLog = prev })
}

// withMasterFileData replaces the package-level masterFileData for the
// duration of the test, restoring the previous value via t.Cleanup.
// disguiseBoostedWeathers and derivedMoves/movePoolsFor read this global
// directly, so a test that needs a specific mask, the uninitialized sentinel,
// or specific move pools must control it rather than rely on ambient
// test-run state. Safe only because no test in this package uses t.Parallel.
func withMasterFileData(t *testing.T, data MasterFileData) {
	t.Helper()
	masterFileMu.Lock()
	prev := masterFileData
	masterFileData = data
	masterFileMu.Unlock()
	t.Cleanup(func() {
		masterFileMu.Lock()
		masterFileData = prev
		masterFileMu.Unlock()
	})
}

// TestDittoPoolMemberTrueAndFalse pins ditto_pool_member against
// inDittoPool's own species list: a pool member (56) must log true, and a
// non-member (1, Bulbasaur) must log false — the false case uses a real
// non-member rather than merely asserting the true case, so a stub that
// always returns true cannot pass. Both asserted on the RENDERED log
// message, not entry.Data.
func TestDittoPoolMemberTrueAndFalse(t *testing.T) {
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)

	// Membership is species AND form, so each case names both. The third is
	// the one the form exists for: the same species in a variant form is not a
	// disguise, because a Ditto only ever wears the normal one.
	for _, tc := range []struct {
		name    string
		species int32
		form    int32
		want    bool
	}{
		{"pool member, normal form", 56, int32(dittoRollTestPoolForm16), true},
		{"pool member, variant form", 56, 2792, false},
		{"not a pool member", 1, int32(normalForm(1)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDerivationLog(t)
			hook := captureStandardLoggerHook(t)

			scan := &pokemonScan{
				Weather: int32(pogo.GameplayWeatherProto_NONE),
				Pokemon: tc.species,
				Form:    tc.form,
			}
			p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(uint64(444555666777))}}

			callLogDerivationOutcome(p, scan, encounter.Values{}, "encounter", false, 0, 0)

			if len(hook.Entries) != 1 {
				t.Fatalf("expected exactly one log entry, got %d", len(hook.Entries))
			}
			fields := derivationLogFields(t, &hook.Entries[0])
			if got := fields["ditto_pool_member"]; got != tc.want {
				t.Errorf("%s: ditto_pool_member = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// dittoBranchValues returns the expected/opposite pair for eid/form:
// expected is the branch scan.Weather (boosted) implies, opposite is the
// other one. Shared by the ditto_branch tests below so each only has
// to say which branch it wants observed to match.
func dittoBranchValues(eid uint64, form int32, boosted bool) (expected, opposite encounter.Values) {
	if boosted {
		return encounter.For(eid, true, form).Values, encounter.For(eid, false, form).Values
	}
	return encounter.For(eid, false, form).Values, encounter.For(eid, true, form).Values
}

// The standalone discovery channel, called directly: a species outside the
// pool whose observed IVs match the OPPOSITE boost branch emits the "[DITTO]
// Unknown disguise" WARN even with derivation_log off — it must not depend on
// the validation log — and, with the log on, a [DERIVE] row that says why:
// the values show a Ditto the roll could not fire for.
func TestCheckDerivationOppositeBranchNonPoolWarnsAndLogsTheConflict(t *testing.T) {
	// The reporter is package-level and shared by every test in the
	// binary; reopen its window so this test's own line is not
	// suppressed by one an earlier test already made this second.
	unknownDisguiseDrops.Reset()
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355) // species 25 below is not one of them
	fake := withDerivationCountingCollector(t)
	hook := captureStandardLoggerHook(t) // derivation_log is off: the WARN must not need it

	const disguise = 25 // Pikachu: not in the pool above
	if inDittoPool(int16(disguise), normalForm(int16(disguise))) {
		t.Fatal("test fixture assumption violated: species 25 is a dittoPool member")
	}

	const eid = uint64(246813579246813)
	const form = int32(0)
	const boosted = false
	_, opposite := dittoBranchValues(eid, form, boosted)

	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE), // boosted=false -> expected is the unboosted branch
		Level:   opposite.Level,
		Attack:  opposite.Attack,
		Defense: opposite.Defense,
		Stamina: opposite.Stamina,
		Pokemon: disguise,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, opposite.Size, 0, 0, "encounter", false, false)

	// Not a mismatch, not a disagreement: an "opposite" row counts as agreed,
	// not disagreed.
	if fake.agreedCount("encounter") != 1 || fake.disagreed["encounter"] != 0 {
		t.Fatalf("expected an agreement (agreed=%d disagreed=%d) — an opposite-branch row must not count "+
			"as a disagreement", fake.agreedCount("encounter"), fake.disagreed["encounter"])
	}
	if fake.boostBranchDivergence["encounter"] != 1 {
		t.Errorf("boostBranchDivergence = %d, want 1", fake.boostBranchDivergence["encounter"])
	}

	// The property under test: the standalone WARN fired anyway, and names
	// the species (not just its dex number) and carries the encounter id.
	var warn *logrus.Entry
	for i := range hook.Entries {
		if strings.HasPrefix(hook.Entries[i].Message, "[DITTO] Unknown disguise") {
			warn = &hook.Entries[i]
		}
	}
	if warn == nil {
		t.Fatalf("expected a \"[DITTO] Unknown disguise\" WARN entry, got entries: %v", hook.Entries)
	}
	if warn.Level != logrus.WarnLevel {
		t.Errorf("WARN entry level = %v, want %v", warn.Level, logrus.WarnLevel)
	}
	if !strings.Contains(warn.Message, "PIKACHU") {
		t.Errorf("WARN message does not contain the species name PIKACHU: %s", warn.Message)
	}
	if !strings.Contains(warn.Message, "25") {
		t.Errorf("WARN message does not contain the species number 25: %s", warn.Message)
	}
	if !strings.Contains(warn.Message, "246813579246813") {
		t.Errorf("WARN message does not contain the encounter id: %s", warn.Message)
	}

	// Logging only — must never mark the Pokemon.
	if p.IsDitto {
		t.Error("checkDerivation must never mark the Pokemon as Ditto from the branch signal alone")
	}

	// With the log on, the same row is written as the conflict it is.
	withDerivationLog(t)
	hook.Reset()
	(&Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}).checkDerivation(scan, opposite.Size, 0, 0, "encounter", false, false)
	fields := derivationLogFields(t, soleDerivationLogEntry(t, hook.Entries))
	if reasons, _ := fields["logged_because"].([]any); len(reasons) != 1 || reasons[0] != "roll_missed_opposite" {
		t.Errorf("logged_because = %v, want [roll_missed_opposite]", fields["logged_because"])
	}
}

// TestCheckDerivationExpectedBranchNonPoolDoesNotWarn is the negative
// counterpart: a non-pool species whose observed IVs match the EXPECTED
// (ordinary) branch must not trigger the discovery WARN — only "opposite"
// does. A WARN fired unconditionally (or on any non-pool species) would still
// pass the positive test above but fails this one.
func TestCheckDerivationExpectedBranchNonPoolDoesNotWarn(t *testing.T) {
	// The reporter is package-level and shared by every test in the binary.
	// Without reopening its window, a line this test wants to prove is NOT
	// emitted could simply have been suppressed by an earlier test's, and the
	// assertion below would hold whatever this code did.
	unknownDisguiseDrops.Reset()
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const disguise = 25 // Pikachu: not in the pool above
	const eid = uint64(246813579246814)
	const form = int32(0)
	expected, _ := dittoBranchValues(eid, form, false)

	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   expected.Level,
		Attack:  expected.Attack,
		Defense: expected.Defense,
		Stamina: expected.Stamina,
		Pokemon: disguise,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, expected.Size, 0, 0, "encounter", false, false)

	for _, e := range hook.Entries {
		if strings.HasPrefix(e.Message, "[DITTO] Unknown disguise") {
			t.Fatalf("expected no discovery WARN for an expected-branch row, got: %s", e.Message)
		}
	}
}

// TestCheckDerivationOppositeBranchPoolMemberLogsDeriveNotWarn is the
// in-pool counterpart of the discovery test above: an "opposite" row for a
// species ALREADY in the pool, on an encounter whose roll did not fire, is a
// Ditto indicator the roll missed and appears in [DERIVE] as
// roll_missed_opposite — but must NOT also fire the standalone WARN, which
// exists to find a disguise the pool does not yet know about.
func TestCheckDerivationOppositeBranchPoolMemberLogsDeriveNotWarn(t *testing.T) {
	// The reporter is package-level and shared by every test in the binary.
	// Without reopening its window, a line this test wants to prove is NOT
	// emitted could simply have been suppressed by an earlier test's, and the
	// assertion below would hold whatever this code did.
	unknownDisguiseDrops.Reset()
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const disguise = 56 // a pool member (see withDittoPool above)
	const eid = uint64(246813579246815)
	// The species' NORMAL form: pool membership is species AND form, and a
	// bare ditto_pool entry resolves to the normal one.
	const form = int32(dittoRollTestPoolForm16)
	_, opposite := dittoBranchValues(eid, form, false)

	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   opposite.Level,
		Attack:  opposite.Attack,
		Defense: opposite.Defense,
		Stamina: opposite.Stamina,
		Pokemon: disguise,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, opposite.Size, 0, 0, "encounter", false, false)

	if fake.boostBranchDivergence["encounter"] != 1 {
		t.Errorf("boostBranchDivergence = %d, want 1 (the counter is not gated on pool membership)",
			fake.boostBranchDivergence["encounter"])
	}

	entry := soleDerivationLogEntry(t, hook.Entries)
	fields := derivationLogFields(t, entry)
	if got := fields["ditto_branch"]; got != "opposite" {
		t.Fatalf("test setup check failed: ditto_branch = %v, want \"opposite\"", got)
	}
	if got := fields["ditto_pool_member"]; got != true {
		t.Errorf("ditto_pool_member = %v, want true", got)
	}
	// soleDerivationLogEntry already proved there is exactly one [DERIVE]
	// entry; this additionally proves no [DITTO] WARN accompanied it.
	for _, e := range hook.Entries {
		if strings.HasPrefix(e.Message, "[DITTO] Unknown disguise") {
			t.Fatalf("expected no discovery WARN for an in-pool species, got: %s", e.Message)
		}
	}
}

// TestAddEncounterPokemonOppositeBranchNonPoolWarnsWithoutTheValidationLog
// drives the discovery signal through the REAL entry point. Tests that call
// checkDerivation directly with hand-built arguments would pass even if
// production could never produce those arguments. dittoBranchFor's own
// behaviour is covered by TestDittoBranchForAllFourStates; this asserts that
// a realistic encounter through addEncounterPokemon, exactly as production
// calls it, produces the standalone WARN with derivation_log off, so the
// channel that finds a disguise the pool lacks does not depend on the
// validation log.
//
// Uses TestAddEncounterPokemonConfirmedDittoOutsidePoolIsLogged's eid-search
// technique: find an encounter id whose BOOSTED derivation lands
// on exactly the level cpMultiplier implies, then report that branch's
// Attack/Defense/Stamina as this round's OBSERVED proto values while
// scan.Weather stays NONE (unboosted) — so the disguise's own boost state
// says "unboosted" (expected = unboosted branch) while the observed IVs
// come from the OTHER (boosted/opposite) branch.
func TestAddEncounterPokemonOppositeBranchNonPoolWarnsWithoutTheValidationLog(t *testing.T) {
	// derive_iv off: the disguise tripwire must not depend on derivation
	// being enabled. It is the only channel that can find a disguise
	// ditto_pool does not know about, and an operator running stock stats
	// still needs to hear when the pool goes stale.
	withDeriveIv(t, false)
	// The reporter is package-level and shared by every test in the
	// binary; reopen its window so this test's own line is not
	// suppressed by one an earlier test already made this second.
	unknownDisguiseDrops.Reset()
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)
	hook := captureStandardLoggerHook(t) // derivation_log is off

	const disguise = 25 // Pikachu: not in the pool above
	if inDittoPool(int16(disguise), normalForm(int16(disguise))) {
		t.Fatal("test fixture assumption violated: species 25 is now a dittoPool member; " +
			"this test needs a disguise ditto_pool does NOT already cover")
	}

	const cpMultiplier = float32(0.5) // first (quadratic) branch of cpMultiplierToLevel
	wantLevel := cpMultiplierToLevel(cpMultiplier)

	// Find an encounter id whose BOOSTED derivation lands on exactly
	// wantLevel, so this round's raw display can be made to match the
	// OPPOSITE branch (boosted) while scan.Weather reports unboosted.
	var eid uint64
	var opposite encounter.Values
	found := false
	for candidate := uint64(1); candidate < 200000; candidate++ {
		v := encounter.For(candidate, true, 0)
		if v.Level == wantLevel {
			eid, opposite, found = candidate, v.Values, true
			break
		}
	}
	if !found {
		t.Fatal("could not find an encounter id whose boosted derivation matches wantLevel in the search range")
	}

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
	p.PokemonId = disguise
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))) // this round: disguise NOT boosted

	proto := &pogo.PokemonProto{
		PokemonId:         disguise,
		IndividualAttack:  opposite.Attack,
		IndividualDefense: opposite.Defense,
		IndividualStamina: opposite.Stamina,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "")

	if p.IsDitto {
		t.Fatal("test fixture assumption violated: this fresh record (no scan history) must not have " +
			"been confirmed as a Ditto by detectDitto")
	}

	// The property under test: the standalone WARN fired anyway, through
	// the real addEncounterPokemon call site, and names the species.
	var warn *logrus.Entry
	for i := range hook.Entries {
		if strings.HasPrefix(hook.Entries[i].Message, "[DITTO] Unknown disguise") {
			warn = &hook.Entries[i]
		}
	}
	if warn == nil {
		t.Fatalf("expected a \"[DITTO] Unknown disguise\" WARN entry driven through addEncounterPokemon, "+
			"got entries: %v", hook.Entries)
	}
	if warn.Level != logrus.WarnLevel {
		t.Errorf("WARN entry level = %v, want %v", warn.Level, logrus.WarnLevel)
	}
	if !strings.Contains(warn.Message, "PIKACHU") {
		t.Errorf("WARN message does not contain the species name PIKACHU: %s", warn.Message)
	}
}

// derivation_disagreed_total must increment for "neither" and must NOT
// increment for "opposite". That distinction carries the counter's whole
// meaning: as raw mismatches the two are indistinguishable, and "opposite"
// rows are correctly-explained Dittos, so counting them would put a floor
// under the metric and make it read as constant derivation failure.
func TestCheckDerivationDisagreedCounterOnlyCountsNeitherBranch(t *testing.T) {
	const disguise = 25 // Pikachu: not a dittoPool member and not asserted on here either way
	const form = int32(0)

	for _, tc := range []struct {
		name           string
		eid            uint64
		wantAgreed     int
		wantDisagreed  int
		wantDivergence int
	}{
		{"opposite branch counts as agreed, not disagreed", 246813579246900, 1, 0, 1},
		{"neither branch counts as disagreed", 246813579246901, 0, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := withDerivationCountingCollector(t)
			withDerivationLog(t)
			hook := captureStandardLoggerHook(t)

			unboosted, boosted := dittoBranchValues(tc.eid, form, false)
			var observed encounter.Values
			if tc.wantDivergence == 1 {
				observed = boosted // matches the OPPOSITE branch exactly
			} else {
				// Matches neither: same Attack/Defense/Stamina/Size as the
				// expected (unboosted) branch, but a deliberately wrong
				// Level that also cannot coincide with the boosted branch's
				// Level (level differs from the boosted branch by the fixed
				// +5 weather bonus, so a +1 offset from unboosted can only
				// coincide with boosted if the bonus were 1, which it never
				// is).
				observed = unboosted
				observed.Level++
			}

			// Check the fixture produces the intended branch before trusting
			// the counter assertions below. wantDivergence encodes "opposite"
			// (1) vs "neither" (0); dittoBranchFor is the pure function
			// checkDerivation itself calls.
			wantBranch := dittoBranchNeither
			if tc.wantDivergence == 1 {
				wantBranch = dittoBranchOpposite
			}
			if got := dittoBranchFor(observed, unboosted, boosted, false); got != wantBranch {
				t.Fatalf("test fixture is worthless: dittoBranchFor(...) = %q, want %q — pick a different eid",
					got, wantBranch)
			}

			scan := &pokemonScan{
				Weather: int32(pogo.GameplayWeatherProto_NONE),
				Level:   observed.Level,
				Attack:  observed.Attack,
				Defense: observed.Defense,
				Stamina: observed.Stamina,
				Pokemon: disguise,
				Form:    form,
			}
			p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(tc.eid)}}

			p.checkDerivation(scan, observed.Size, 0, 0, "encounter", false, false)

			if fake.agreedCount("encounter") != tc.wantAgreed {
				t.Errorf("agreed = %d, want %d", fake.agreedCount("encounter"), tc.wantAgreed)
			}
			if fake.disagreed["encounter"] != tc.wantDisagreed {
				t.Errorf("disagreed = %d, want %d", fake.disagreed["encounter"], tc.wantDisagreed)
			}
			if fake.boostBranchDivergence["encounter"] != tc.wantDivergence {
				t.Errorf("boostBranchDivergence = %d, want %d", fake.boostBranchDivergence["encounter"], tc.wantDivergence)
			}
			_ = hook // logging behavior is covered by the tests above; this test is counters-only
		})
	}
}

// TestCheckDerivationStrongPokemonYieldsUnknownBranchNotNeither pins the
// fourth ditto_branch state's real-world trigger: a strong Pokemon's
// observed IVs are excluded from dittoBranchFor's comparison entirely (its
// IVs are unaffected by weather, so neither derived branch is a meaningful
// prediction), so ditto_branch must read "unknown", never "neither", even
// though the deliberately-wrong observed Attack below
// would read as "neither" for a non-strong Pokemon (see
// TestCheckDerivationDisagreedCounterOnlyCountsNeitherBranch's "neither"
// case, which uses an off-by-one fixture of the same kind). This is also
// TestCheckDerivationStrongPokemonExcludedFromCountersButLogged's fixture,
// extended with the ditto_branch assertion that test does not make.
func TestCheckDerivationStrongPokemonYieldsUnknownBranchNotNeither(t *testing.T) {
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(444555666778)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  (v.Attack + 1) % 16, // strong Pokemon routinely mismatch — would read "neither" if not strong
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: 1,
		Form:    form,
		Strong:  true,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, false)

	// Strong Pokemon never touch these counters, including the boost-branch
	// divergence counter.
	if fake.checked["encounter"] != 0 || fake.agreedCount("encounter") != 0 || fake.disagreed["encounter"] != 0 ||
		fake.boostBranchDivergence["encounter"] != 0 {
		t.Fatalf("strong Pokemon must not touch any derivation counter: checked=%d agreed=%d disagreed=%d divergence=%d",
			fake.checked["encounter"], fake.agreedCount("encounter"), fake.disagreed["encounter"], fake.boostBranchDivergence["encounter"])
	}

	entry := soleDerivationLogEntry(t, hook.Entries)
	fields := derivationLogFields(t, entry)
	if got := fields["ditto_branch"]; got != "unknown" {
		t.Errorf("ditto_branch = %v, want \"unknown\" (a strong Pokemon must never read \"neither\", "+
			"even on a genuine IV mismatch)", got)
	}
}

// A mismatch on either move (here, move_1) must fold into the mismatch
// condition — counting as a derivation disagreement, not only in the
// move-specific counter — and must log the derived/observed move fields.
// moveMismatchSpecies is a placeholder id outside the pool, so the move
// mismatch is the only reason this row can be logged.
func TestCheckDerivationMoveMismatchLogsCountsAndSetsMoveMismatchCounter(t *testing.T) {
	const moveMismatchSpecies = 9101
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			moveMismatchSpecies: {QuickMoves: []int{300, 301}, ChargedMoves: []int{400, 401, 402}},
		},
	})
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(741852963741852)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	quickIdx, chargedIdx := v.MoveIndices(2, 3)
	derivedMove1 := int32([]int{300, 301}[quickIdx])
	derivedMove2 := int32([]int{400, 401, 402}[chargedIdx])
	// Deliberately wrong: the pool only contains 300/301, so whichever one
	// derivedMove1 is not is guaranteed to disagree with it.
	wrongMove1 := int32(300)
	if derivedMove1 == 300 {
		wrongMove1 = 301
	}

	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: moveMismatchSpecies,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	// Every stat/level/size input above agrees with the derivation; only the
	// move disagrees, isolating moveMismatch as the sole cause of the
	// resulting disagreement.
	p.checkDerivation(scan, v.Size, wrongMove1, derivedMove2, "encounter", false, false)

	if fake.disagreed["encounter"] != 1 {
		t.Fatalf("disagreed = %d, want 1 (a move-only mismatch must still count as a derivation disagreement)",
			fake.disagreed["encounter"])
	}
	if fake.agreedCount("encounter") != 0 {
		t.Fatalf("agreed = %d, want 0", fake.agreedCount("encounter"))
	}
	if fake.moveMismatch["encounter"] != 1 {
		t.Fatalf("moveMismatch = %d, want 1", fake.moveMismatch["encounter"])
	}
	if len(hook.Entries) != 1 {
		t.Fatalf("expected exactly one log entry for a move mismatch, got %d", len(hook.Entries))
	}
	fields := derivationLogFields(t, &hook.Entries[0])
	if got := fields["derived_move_1"]; got != float64(derivedMove1) {
		t.Errorf("derived_move_1 = %v, want %v", got, derivedMove1)
	}
	if got := fields["derived_move_2"]; got != float64(derivedMove2) {
		t.Errorf("derived_move_2 = %v, want %v", got, derivedMove2)
	}
	if got := fields["observed_move_1"]; got != float64(wrongMove1) {
		t.Errorf("observed_move_1 = %v, want %v", got, wrongMove1)
	}
	if got := fields["observed_move_2"]; got != float64(derivedMove2) {
		t.Errorf("observed_move_2 = %v, want %v", got, derivedMove2)
	}
}

// An unresolved move pool is "no opinion," not a mismatch. This species is present in the masterfile fixture (so
// disguiseBoostedWeathers' own findBoostedWeathers lookup does not itself
// warn or otherwise interfere) but carries no move pools at all, so
// movePoolsFor must report ok=false — and the passed-in "observed" moves
// (999/888) are deliberately values that would disagree with any real pool,
// to prove the absence of a mismatch signal comes from the unresolved guard
// and not from an accidental agreement.
func TestCheckDerivationUnresolvedMovePoolTriggersNeitherLogNorCounter(t *testing.T) {
	const unresolvedSpecies = 9102
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			unresolvedSpecies: {Types: []int{1}}, // present, but no move pools
		},
	})
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(963258741963258)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: unresolvedSpecies,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, v.Size, 999, 888, "encounter", false, false)

	if fake.agreedCount("encounter") != 1 || fake.disagreed["encounter"] != 0 {
		t.Fatalf("an unresolved move pool must not cause a disagreement: agreed=%d disagreed=%d",
			fake.agreedCount("encounter"), fake.disagreed["encounter"])
	}
	if fake.moveMismatch["encounter"] != 0 {
		t.Fatalf("moveMismatch = %d, want 0 (no opinion, not a mismatch)", fake.moveMismatch["encounter"])
	}
	if len(hook.Entries) != 0 {
		t.Fatalf("expected no log entry when everything else agrees and the move pool is unresolved, got %d",
			len(hook.Entries))
	}
}

// The third "no opinion, not a disagreement" case (after POKEMON_SIZE_UNSET
// and an unresolved move pool): an observed move that does not
// appear ANYWHERE in the pool the derivation drew its index from cannot have
// been produced by any index into that pool, so it must not count as a
// mismatch. Unlike the unresolved-pool case above, though, this row is NOT
// silent — it is logged as move_out_of_pool, its own reason rather than a
// mismatch. The positive assertion on hook.Entries is
// the important one: counter checks alone would pass even if the row stopped
// being logged.
func TestCheckDerivationMoveOutOfPoolLogsButNotAsMismatch(t *testing.T) {
	const outOfPoolSpecies = 9103
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			outOfPoolSpecies: {QuickMoves: []int{700, 701}, ChargedMoves: []int{800, 801, 802}},
		},
	})
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(159357456159357)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	quickIdx, chargedIdx := v.MoveIndices(2, 3)
	derivedMove1 := int32([]int{700, 701}[quickIdx])
	derivedMove2 := int32([]int{800, 801, 802}[chargedIdx])
	// Not merely a different index — 999 is not in the quick pool at all,
	// so no index into it could ever have produced this observation.
	const observedMove1OutOfPool = int32(999)

	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: outOfPoolSpecies,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	// The charged slot (move2) matches the derivation exactly, isolating the
	// quick slot's out-of-pool observation as the sole unusual input.
	p.checkDerivation(scan, v.Size, observedMove1OutOfPool, derivedMove2, "encounter", false, false)

	if fake.agreedCount("encounter") != 1 {
		t.Fatalf("agreed = %d, want 1 (an out-of-pool move is no opinion, not a disagreement)", fake.agreedCount("encounter"))
	}
	if fake.disagreed["encounter"] != 0 {
		t.Fatalf("disagreed = %d, want 0 (must not feed derivation_disagreed_total)", fake.disagreed["encounter"])
	}
	if fake.moveMismatch["encounter"] != 0 {
		t.Fatalf("moveMismatch = %d, want 0 (must not increment derivation_move_mismatch_total)", fake.moveMismatch["encounter"])
	}
	if len(hook.Entries) != 1 {
		t.Fatalf("expected exactly one log entry for the out-of-pool move, got %d — "+
			"an out-of-pool observation must still be logged even though it is not a mismatch", len(hook.Entries))
	}
	fields := derivationLogFields(t, &hook.Entries[0])
	if got := fields["observed_move_1"]; got != float64(observedMove1OutOfPool) {
		t.Errorf("observed_move_1 = %v, want %v", got, observedMove1OutOfPool)
	}
	if got := fields["derived_move_1"]; got != float64(derivedMove1) {
		t.Errorf("derived_move_1 = %v, want %v", got, derivedMove1)
	}
}

// A shape seen in production: a single encounter where the quick move matches the derivation exactly and
// the charged move is out of pool, at once. Each slot must be judged
// independently — a single row-level flag conflating the two would misreport
// this exact case, which is why checkDerivation computes move1Mismatch/
// move2Mismatch (and move1OutOfPool/move2OutOfPool) separately rather than
// as one combined signal.
//
// Uses Lickitung's real masterfile pools (species 108: quick [212, 234],
// charged [14, 127, 118, 13]) and its real observed anomaly (charged move
// 131, Body Slam — absent from the charged pool in every masterfile
// variant), the population this whole exclusion exists for.
func TestCheckDerivationLickitungQuickMatchesChargedOutOfPool(t *testing.T) {
	const lickitung = 108
	quickPool := []int{212, 234}
	chargedPool := []int{14, 127, 118, 13}
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			lickitung: {QuickMoves: quickPool, ChargedMoves: chargedPool},
		},
	})
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(741963852741963)
	const form = int32(0)
	v := encounter.For(eid, false, form)
	quickIdx, chargedIdx := v.MoveIndices(int32(len(quickPool)), int32(len(chargedPool)))
	derivedMove1 := int32(quickPool[quickIdx])
	derivedMove2 := int32(chargedPool[chargedIdx])
	const observedChargedOutOfPool = int32(131) // Body Slam: not in Lickitung's charged pool

	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level,
		Attack:  v.Attack,
		Defense: v.Defense,
		Stamina: v.Stamina,
		Pokemon: lickitung,
		Form:    form,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	// move1 (quick) is set to exactly what the derivation predicts; move2
	// (charged) is the out-of-pool observation. If both slots were judged
	// together instead of independently, this combination is exactly what
	// would misreport.
	p.checkDerivation(scan, v.Size, derivedMove1, observedChargedOutOfPool, "encounter", false, false)

	if fake.agreedCount("encounter") != 1 {
		t.Fatalf("agreed = %d, want 1 (quick matches; charged is no opinion, not a disagreement)", fake.agreedCount("encounter"))
	}
	if fake.disagreed["encounter"] != 0 {
		t.Fatalf("disagreed = %d, want 0", fake.disagreed["encounter"])
	}
	if fake.moveMismatch["encounter"] != 0 {
		t.Fatalf("moveMismatch = %d, want 0", fake.moveMismatch["encounter"])
	}
	if len(hook.Entries) != 1 {
		t.Fatalf("expected exactly one log entry for the quick-matches/charged-out-of-pool encounter, got %d",
			len(hook.Entries))
	}
	fields := derivationLogFields(t, &hook.Entries[0])
	// The quick slot is judged clean: observed equals derived.
	if got := fields["observed_move_1"]; got != float64(derivedMove1) {
		t.Errorf("observed_move_1 = %v, want %v (quick slot matches the derivation)", got, derivedMove1)
	}
	if got := fields["derived_move_1"]; got != float64(derivedMove1) {
		t.Errorf("derived_move_1 = %v, want %v", got, derivedMove1)
	}
	// The charged slot is out of pool, independently of the quick slot's
	// agreement, and is not itself equal to the derivation.
	if got := fields["observed_move_2"]; got != float64(observedChargedOutOfPool) {
		t.Errorf("observed_move_2 = %v, want %v", got, observedChargedOutOfPool)
	}
	if got := fields["derived_move_2"]; got != float64(derivedMove2) {
		t.Errorf("derived_move_2 = %v, want %v", got, derivedMove2)
	}
	if fields["observed_move_2"] == fields["derived_move_2"] {
		t.Errorf("observed_move_2 (%v) coincidentally equals derived_move_2 (%v); this fixture must exercise a genuine out-of-pool mismatch",
			fields["observed_move_2"], fields["derived_move_2"])
	}
}

// TestCheckDerivationLogSuppressedWhenDisabled pins the default: with
// derivation_log off, a row that would otherwise log emits nothing. The
// fixture's attack disagrees with the derivation, so it logs as a mismatch
// when the gate is on — the control below proves it — and the gate is the
// only thing that can suppress it.
func TestCheckDerivationLogSuppressedWhenDisabled(t *testing.T) {
	const eid = uint64(135792468135792)
	v := encounter.For(eid, false, 0)

	for _, enabled := range []bool{true, false} {
		prev := config.Config.DerivationLog
		config.Config.DerivationLog = enabled
		hook := captureStandardLoggerHook(t)

		scan := &pokemonScan{
			Weather: int32(pogo.GameplayWeatherProto_NONE),
			Level:   v.Level, Attack: (v.Attack + 1) % 16, Defense: v.Defense, Stamina: v.Stamina,
			Pokemon: 1, Form: 0,
		}
		p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}
		p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, false)
		config.Config.DerivationLog = prev

		logged := 0
		for i := range hook.Entries {
			if strings.HasPrefix(hook.Entries[i].Message, derivationLogPrefix) {
				logged++
			}
		}
		if enabled && logged != 1 {
			t.Fatalf("control: with derivation_log on, the mismatch must log once; got %d entries", logged)
		}
		if !enabled && logged != 0 {
			t.Fatalf("derivation_log is off, so no [DERIVE] entry may be emitted; got %d", logged)
		}
	}
}

// TestCheckDerivationCountersAndWarningIgnoreTheLogGate is the separation the
// gate exists to preserve: turning the analysis corpus off must not blind an
// operator. The counters still move, and an unknown Ditto disguise is still
// reported, with derivation_log off.
func TestCheckDerivationCountersAndWarningIgnoreTheLogGate(t *testing.T) {
	// The reporter is package-level and shared by every test in the
	// binary; reopen its window so this test's own line is not
	// suppressed by one an earlier test already made this second.
	unknownDisguiseDrops.Reset()
	prev := config.Config.DerivationLog
	config.Config.DerivationLog = false
	t.Cleanup(func() { config.Config.DerivationLog = prev })

	fake := withDerivationCountingCollector(t)
	hook := captureStandardLoggerHook(t)

	// A non-pool species whose observed IVs match the OPPOSITE branch: the
	// unknown-disguise case, which must warn even with logging off.
	const eid = uint64(246813579246900)
	const nonPoolSpecies = 25
	unboosted, boosted := dittoBranchValues(eid, 0, false)
	if dittoBranchFor(boosted, unboosted, boosted, false) != dittoBranchOpposite {
		t.Fatalf("test fixture is worthless: this eid does not produce an opposite-branch row")
	}
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   boosted.Level, Attack: boosted.Attack, Defense: boosted.Defense,
		Stamina: boosted.Stamina, Pokemon: nonPoolSpecies, Form: 0,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, boosted.Size, 0, 0, "encounter", false, false)

	if fake.checked["encounter"] != 1 {
		t.Errorf("derivation_checked_total = %d, want 1 — counters must not depend on the log gate",
			fake.checked["encounter"])
	}
	if fake.boostBranchDivergence["encounter"] != 1 {
		t.Errorf("boostBranchDivergence = %d, want 1 — counters must not depend on the log gate",
			fake.boostBranchDivergence["encounter"])
	}
	var warned bool
	for i := range hook.Entries {
		if strings.HasPrefix(hook.Entries[i].Message, "[DITTO] Unknown disguise") {
			warned = true
		}
		if strings.HasPrefix(hook.Entries[i].Message, derivationLogPrefix) {
			t.Errorf("derivation_log is off; no [DERIVE] entry may be emitted")
		}
	}
	if !warned {
		t.Errorf("the [DITTO] unknown-disguise warning must fire with derivation_log off; got %v",
			hook.Entries)
	}
}

// detectDitto can refute a roll-mark from a single prior scan, through its 0N
// and BN branches. Those carry no cross-boost history, so without treating an
// active clear as evidence they would log as "no_evidence" — and derivecheck
// would drop a genuine false positive, under-counting in the one direction
// that flatters the roll rule.
func TestCheckDerivationReportsAnActiveRefutationAsRejected(t *testing.T) {
	withDerivationLog(t)
	withDittoPool(t, 56)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(246813579246913)
	v := encounter.For(eid, false, 0)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level, Attack: v.Attack, Defense: v.Defense, Stamina: v.Stamina,
		Pokemon: 56, Form: int32(dittoRollTestPoolForm16),
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	// wasRollMarked true, no cross-boost history, but detectDitto cleared the
	// flag during this decode — the shape the 0N/BN branches produce.
	p.checkDerivation(scan, v.Size, 0, 0, "encounter", true, true)

	entry := soleDerivationLogEntry(t, hook.Entries)
	if reasons, _ := derivationLogFields(t, entry)["logged_because"].([]any); len(reasons) != 1 || reasons[0] != "roll_refuted" {
		t.Errorf("logged_because = %v, want [roll_refuted]: a roll-mark detectDitto took back is the "+
			"disagreement this row exists to record", reasons)
	}
	if got := derivationLogFields(t, entry)["ditto_detect"]; got != "rejected" {
		t.Errorf("ditto_detect = %v, want \"rejected\": a refutation without cross-boost "+
			"history is still a refutation, and counting it as no_evidence hides a false positive", got)
	}
}

// Every combination of the four Ditto signals, and which ones are logged. The
// three conflicts are the only rows written for Ditto reasons; everything
// else is agreement, or the absence of evidence either way.
func TestDittoSignalConflict(t *testing.T) {
	for _, c := range []struct {
		rollFired, confirmed, rejected, opposite bool
		want                                     string
	}{
		{true, false, true, false, "roll_refuted"},
		{true, false, true, true, "roll_refuted"},
		{false, true, false, false, "roll_missed_confirmed"},
		{false, true, false, true, "roll_missed_confirmed"},
		{false, false, false, true, "roll_missed_opposite"},
		{false, false, true, true, "roll_missed_opposite"},
		// Agreement.
		{true, true, false, false, ""},
		{true, true, false, true, ""},
		{true, false, false, true, ""},
		{false, false, true, false, ""},
		// No evidence either way.
		{true, false, false, false, ""},
		{false, false, false, false, ""},
	} {
		if got := dittoSignalConflict(c.rollFired, c.confirmed, c.rejected, c.opposite); got != c.want {
			t.Errorf("roll=%v confirmed=%v rejected=%v opposite=%v: got %q, want %q",
				c.rollFired, c.confirmed, c.rejected, c.opposite, got, c.want)
		}
	}
}

// A Ditto detectDitto confirmed from evidence, on an encounter whose roll did
// not fire, is the roll's miss, and is logged as one. The same record marked
// by the roll is not: that confirmation is the roll's own.
func TestCheckDerivationLogsADittoTheRollMissed(t *testing.T) {
	withDittoPool(t, 56)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	eid := eidRollDoesNotFire
	v := encounter.For(eid, false, int32(dittoRollTestPoolForm16))
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level, Attack: v.Attack, Defense: v.Defense, Stamina: v.Stamina,
		Pokemon: 56, Form: int32(dittoRollTestPoolForm16),
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid), IsDitto: true}}
	p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, true)

	fields := derivationLogFields(t, soleDerivationLogEntry(t, hook.Entries))
	if reasons, _ := fields["logged_because"].([]any); len(reasons) != 1 || reasons[0] != "roll_missed_confirmed" {
		t.Errorf("logged_because = %v, want [roll_missed_confirmed]", fields["logged_because"])
	}

	hook.Reset()
	marked := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid), IsDitto: true}, dittoRollMarked: true}
	marked.checkDerivation(scan, v.Size, 0, 0, "encounter", true, false)
	if len(hook.Entries) != 0 {
		t.Errorf("a roll-marked Ditto was logged as a disagreement: %s", hook.Entries[0].Message)
	}
}

// An encounter that omits its moves reports 0 for both, which is "not
// reported", not a move. It must be neither a mismatch nor out of pool —
// otherwise every such encounter is logged as a disagreement and counted as
// one, on a log that is meant to hold disagreements only.
func TestCheckDerivationUnreportedMovesAreNotADisagreement(t *testing.T) {
	const species = 9104
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon:     map[int]MasterFilePokemon{species: {QuickMoves: []int{500, 501}, ChargedMoves: []int{600, 601}}},
	})
	fake := withDerivationCountingCollector(t)
	withDerivationLog(t)
	hook := captureStandardLoggerHook(t)

	const eid = uint64(333444555777)
	v := encounter.For(eid, false, 0)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   v.Level, Attack: v.Attack, Defense: v.Defense, Stamina: v.Stamina,
		Pokemon: species,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	p.checkDerivation(scan, v.Size, 0, 0, "encounter", false, false)

	if fake.disagreed["encounter"] != 0 {
		t.Errorf("disagreed = %d, want 0: unreported moves are not a disagreement", fake.disagreed["encounter"])
	}
	for _, e := range hook.Entries {
		if strings.HasPrefix(e.Message, derivationLogPrefix) {
			t.Errorf("an encounter agreeing on everything it reported was logged: %s", e.Message)
		}
	}
}
