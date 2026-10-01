package decoder

// This file holds the production primitives behind Ditto marking: the
// configured disguise pool, this package's half of the PRNG candidate rule
// (the policy of what to do with a roll; the roll itself belongs to
// derive/encounter), the boost-branch comparison, and the unknown-disguise
// warning.
//
// They are deliberately separate from derive_validation.go's audit
// apparatus. That file is instrumentation whose [DERIVE] log is off by
// default, but maybeMarkDittoFromRoll calls inDittoPool and dittoSuspectRoll
// on every sighting to decide whether to mark a record at all. Keeping these
// apart means turning off validation logging cannot also turn off Ditto
// marking, and the unknown-disguise warning — which stays useful for as long
// as the pool can go stale — keeps firing regardless.

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"golbat/config"
	"golbat/derive/encounter"
	"golbat/pogo"
	"golbat/util"

	log "github.com/sirupsen/logrus"
)

// dittoPoolEntry is a species and the exact form of it that can be a disguise.
//
// The form matters: a Ditto only ever wears a species' NORMAL form, so a
// Hisuian Growlithe is not a disguise even though an ordinary Growlithe is.
// Keyed on the species alone, the roll would fire on 2% of the variants too,
// and nothing downstream could tell those marks from real ones.
type dittoPoolEntry struct {
	pokemonId int16
	form      int16
}

// String names the entry for a log line: species and form enum names, and
// the "species:form" spelling ditto_pool takes.
func (e dittoPoolEntry) String() string {
	return disguiseName(int32(e.pokemonId), int32(e.form))
}

// disguiseName is how every [DITTO] line names a disguise, e.g.
// "SURSKIT/SURSKIT_NORMAL (283:1412)".
func disguiseName(species, form int32) string {
	return fmt.Sprintf("%s/%s (%d:%d)", pogo.HoloPokemonId(species).String(),
		pogo.PokemonDisplayProto_Form(form).String(), species, form)
}

// dittoPool is a set of disguises, each a species AND form. Methods are
// nil-safe: a nil pool is an empty one.
type dittoPool struct {
	entries map[dittoPoolEntry]bool
}

func newDittoPool(entries []dittoPoolEntry) *dittoPool {
	p := &dittoPool{
		entries: make(map[dittoPoolEntry]bool, len(entries)),
	}
	for _, e := range entries {
		p.entries[e] = true
	}
	return p
}

func (p *dittoPool) has(e dittoPoolEntry) bool { return p != nil && p.entries[e] }

func (p *dittoPool) size() int {
	if p == nil {
		return 0
	}
	return len(p.entries)
}

func (p *dittoPool) all() map[dittoPoolEntry]bool {
	if p == nil {
		return nil
	}
	return p.entries
}

// dittoPoolLookup is the pool in force. It is read on every sighting, so it
// is a pointer swapped whole: while InitDittoPool replaces it after startup,
// every read sees either the old pool or the fully built new one, with no
// lock on the read path.
var dittoPoolLookup atomic.Pointer[dittoPool]

// InitDittoPool (re)builds the pool in force: ditto_pool from config when it
// has any entries, otherwise the published pool (see ditto_pool_remote.go).
//
// A config entry is "<species>" or "<species>:<form>", both as numbers. A
// bare species means what the published pool lists for a species: its NORMAL
// form, and form 0, which the game also reports for an ordinary disguise. It
// never means the species' other forms — an ordinary Growlithe is a
// disguise, a Hisuian Growlithe is not, and they differ only by form. See
// parseDittoPoolEntry.
func InitDittoPool() {
	if !dittoPoolOverridden() {
		dittoPoolLookup.Store(dittoPoolPublished.Load())
		return
	}
	entries := make([]dittoPoolEntry, 0, len(config.Config.DittoPool))
	for _, raw := range config.Config.DittoPool {
		entry, err := parseDittoPoolEntry(raw)
		if err != nil {
			log.Errorf("[DITTO] ignoring ditto_pool entry %q: %s", raw, err)
			continue
		}
		entries = append(entries, entry)
		if !strings.Contains(raw, ":") && entry.form != 0 {
			entries = append(entries, dittoPoolEntry{pokemonId: entry.pokemonId, form: 0})
		}
	}
	if len(entries) == 0 {
		log.Errorf("[DITTO] ditto_pool is set in config but none of its %d entries could be used. It still "+
			"overrides the published pool, so ditto_derivation will mark nothing: fix the entries, or remove "+
			"ditto_pool to use the published pool.", len(config.Config.DittoPool))
	}
	dittoPoolLookup.Store(newDittoPool(entries))
}

// parseDittoPoolEntry turns one configured entry into the species and form it
// names. Errors rather than guessing: a pool entry nobody can parse should be
// reported and dropped, not silently widened to every form of that species.
func parseDittoPoolEntry(raw string) (dittoPoolEntry, error) {
	species, formPart, hasForm := strings.Cut(strings.TrimSpace(raw), ":")
	id, err := strconv.ParseInt(strings.TrimSpace(species), 10, 16)
	if err != nil || id <= 0 {
		return dittoPoolEntry{}, fmt.Errorf("species must be a positive number, got %q", species)
	}
	if hasForm {
		form, err := strconv.ParseInt(strings.TrimSpace(formPart), 10, 16)
		if err != nil || form < 0 {
			return dittoPoolEntry{}, fmt.Errorf("form must be a number, got %q", formPart)
		}
		return dittoPoolEntry{pokemonId: int16(id), form: int16(form)}, nil
	}
	name, known := pogo.HoloPokemonId_name[int32(id)]
	if !known {
		return dittoPoolEntry{}, fmt.Errorf("no species has id %d", id)
	}
	form, ok := normalFormOf(int16(id))
	if !ok {
		return dittoPoolEntry{}, fmt.Errorf("%s has no NORMAL form, so a bare entry names no form at all: "+
			"write %d:<form> with the form seen in game", name, id)
	}
	return dittoPoolEntry{pokemonId: int16(id), form: form}, nil
}

// normalFormOf returns the form a bare pool entry means for a species, and
// whether there is one to mean, from the proto form enum.
//
// Three cases, and the third is the reason this reports ok rather than just
// falling back to 0:
//
//   - <SPECIES>_NORMAL exists: that form, which is what the game sends.
//   - The species has no forms at all: 0, which is what the game sends for
//     one. Only Nidoran♀ and Nidoran♂ are in this case.
//   - The species has forms but no NORMAL — 48 of them, Lycanroc and
//     Squawkabilly among them: ok is false. Defaulting to 0 here would build
//     a pool entry that can never match anything, because the game always
//     sends one of the real forms, and it would do so silently.
func normalFormOf(pokemonId int16) (int16, bool) {
	name, known := pogo.HoloPokemonId_name[int32(pokemonId)]
	if !known {
		return 0, false
	}
	if form, ok := pogo.PokemonDisplayProto_Form_value[name+"_NORMAL"]; ok {
		return int16(form), true
	}
	if speciesHasForms(name) {
		return 0, false
	}
	return 0, true
}

// speciesHasForms reports whether any form in the enum is named for this
// species. Forms are named <SPECIES>_<FORM>, so the test is a prefix match.
//
// A prefix match would lend one species' forms to another whenever one name
// prefixes another, and exactly one pair does: PORYGON_Z_NORMAL begins with
// "PORYGON_". It cannot reach here, because this is only consulted for a
// species with no NORMAL form of its own and PORYGON has one. Should a future
// enum add a pair where the shorter name has no NORMAL, the cost is that a
// bare entry for it is refused and has to be written with its form — an error
// the operator sees, not a pool entry that quietly matches nothing.
func speciesHasForms(name string) bool {
	prefix := name + "_"
	for key := range pogo.PokemonDisplayProto_Form_value {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func inDittoPool(pokemonId, form int16) bool {
	return dittoPoolLookup.Load().has(dittoPoolEntry{pokemonId: pokemonId, form: form})
}

// dittoCandidateThreshold is the roll threshold: a sighting is a candidate
// when the encounter's nextFloat draw comes out below 0.02, matching the
// game's `nextFloat() < 0.02f`.
//
// The comparison is float32 on both sides, which fixes an exact integer
// bound:
//
//	0.02 as float32 = 0.01999999955296516418
//	        × 2^24  = 335544.3125
//	=> candidate when next(24) < 335545
//
// nextFloat is next(24)/2^24, monotonic in the integer, so the float compare
// and that bound are equivalent. The bound depends only on nextFloat being
// 24-bit, not on whether the literal was written 0.02f or 0.02.
const dittoCandidateThreshold float32 = 0.02

// dittoSuspectRoll is the roll half of the candidate rule. It takes only the
// roll — no scan, no pool — so the roll and pool membership stay independent
// signals. roll may be encounter.DittoCandidateRoll(id) or a Draw's DittoRoll
// field; they are the same value.
func dittoSuspectRoll(roll float32) bool {
	return roll < dittoCandidateThreshold
}

// dittoCaughtBoosted reports whether the Ditto on a record whose reported
// weather_boosted_condition is this one would be CAUGHT boosted.
//
// Only in PP: partly cloudy weather AND a disguise that partly cloudy also
// boosts. The game reports exactly that pair as one value, so the reported
// condition answers it on its own and no cell lookup is involved.
//
// The cell's weather is a different question and gives a different answer. In
// 0P the cell IS partly cloudy, so the Normal-typed Ditto under a disguise
// partly cloudy does not boost is REPORTED with boosted stats — and is still
// caught unboosted. Reported and caught come apart there, and only the caught
// set belongs on the record.
func dittoCaughtBoosted(reportedWeather int64) bool {
	return reportedWeather == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
}

// dittoBranch is the boost-branch comparison channel: whether a real
// encounter's raw observed IV/level/size draw (encounter.Values, everything
// checkDerivation's own mismatch check compares) matches the branch
// scan.Weather implies ("expected", the ordinary case), the OTHER branch
// ("opposite"), neither, or the comparison could not be made at all
// ("unknown").
//
// The disguise's types cannot answer this question, which is why the
// comparison is against the Pokemon's own derivations rather than against
// its typing. A Ditto is Normal-type whatever it wears, and Normal is
// boosted only by PARTLY_CLOUDY, which invites comparing
// weather_boosted_condition against the displayed species' types and
// treating a contradiction as a Ditto. The game reports
// weather_boosted_condition for the DISPLAYED species, so a Ditto's
// reported boost already agrees with its disguise's typing and that
// contradiction does not arise.
//
// "opposite" arises because the IVs an encounter REPORTS follow the Ditto's
// own boost state: in PARTLY_CLOUDY the Ditto is boosted while a disguise
// whose typing that weather does not cover is not, so the proto reports
// unboosted and the reported IVs are boosted. That is the 0P state, and it is
// about what is REPORTED — the Ditto you catch there is unboosted, because a
// caught Ditto is boosted only in PP.
//
// Because the comparison never consults ditto_pool, it is the only channel
// that can discover a disguise the pool does not yet know about (see
// logUnknownDittoDisguise).
type dittoBranch string

const (
	// dittoBranchExpected: observed matches the branch scan.Weather implies.
	// The ordinary case.
	dittoBranchExpected dittoBranch = "expected"
	// dittoBranchOpposite: observed matches the OTHER branch — a Ditto
	// indicator.
	dittoBranchOpposite dittoBranch = "opposite"
	// dittoBranchNeither: observed matches neither branch. A genuine
	// derivation failure — checkDerivation counts only this state (never
	// "opposite") toward derivation_disagreed_total.
	dittoBranchNeither dittoBranch = "neither"
	// dittoBranchUnknown: not comparable — a strong Pokemon, whose IVs are
	// unaffected by weather, so neither branch predicts anything for it.
	dittoBranchUnknown dittoBranch = "unknown"
)

// dittoBranchFor compares observed against both of an encounter's derived
// boost branches — expected (the branch scan's own weather_boosted_condition
// implies) and opposite (the other one) — and reports which, if either, it
// matches. A strong Pokemon reports dittoBranchUnknown without comparing.
//
// expected is checked before opposite, so the rare encounter id whose two
// branches coincide (both derive identical Values) reads as "expected," not
// "opposite" — the ordinary, uninteresting case, not a Ditto indicator.
func dittoBranchFor(observed, expected, opposite encounter.Values, strong bool) dittoBranch {
	if strong {
		return dittoBranchUnknown
	}
	switch {
	case boostBranchValuesMatch(observed, expected):
		return dittoBranchExpected
	case boostBranchValuesMatch(observed, opposite):
		return dittoBranchOpposite
	default:
		return dittoBranchNeither
	}
}

// boostBranchValuesMatch compares observed against one derived branch,
// field by field. An observed size of 0 is POKEMON_SIZE_UNSET, "not
// reported" rather than "disagrees", so it matches any branch.
func boostBranchValuesMatch(observed, branch encounter.Values) bool {
	return observed.Attack == branch.Attack &&
		observed.Defense == branch.Defense &&
		observed.Stamina == branch.Stamina &&
		observed.Level == branch.Level &&
		(observed.Size == 0 || observed.Size == branch.Size)
}

// logUnknownDittoDisguise warns that a real encounter's observed IVs matched
// the opposite boost branch for a species not in ditto_pool — the one case
// the branch comparison can discover and pool membership cannot.
//
// Emitted directly by checkDerivation, independent of the [DERIVE] log and
// its shouldLog machinery, because it must keep firing with that log off.
//
// WARN, not INFO: worth attention, but nothing is broken when it fires. It
// logs and never marks; pool expansion stays human-in-the-loop.
//
// species, form and the encounter id are enough to reproduce the finding
// offline, since both the branch comparison and the roll are pure functions
// of the id.
func logUnknownDittoDisguise(encounterId uint64, species, form int32) {
	unknownDisguiseDrops.Report(func(dropped int64) {
		// The exact entry to paste, because ditto_pool is keyed on species AND
		// form: adding the species alone resolves to its normal form, which is
		// the wrong thing to do if what was seen is a variant.
		entry := fmt.Sprintf("%d:%d", species, form)
		// And say when it IS a variant, since that is the surprising case. A
		// Ditto wears a species' normal form, so a variant here is either a
		// change in the game or a false positive, and either is worth knowing
		// before the entry is added.
		variant := ""
		if normal, ok := normalFormOf(int16(species)); !ok {
			variant = " NOTE: this species has no NORMAL form, so there is no bare entry for it — the form above has to be named, as it is above."
		} else if int32(normal) != form {
			variant = fmt.Sprintf(" NOTE: this is NOT the normal form (%d) — a Ditto is not expected to wear a variant, so confirm carefully.", normal)
		}
		log.Warnf("[DITTO] Unknown disguise: %s, encounter %d — not in the ditto pool, but observed IVs "+
			"matched the OPPOSITE weather-boost branch (a Ditto indicator). Confirm in game; if genuine, "+
			"report it for the published pool. %s%s (%d such encounters since the last line.)",
			disguiseName(species, form), encounterId, dittoPoolAddAdvice(entry), variant, dropped)
	})
}

// dittoPoolAddAdvice is how both disguise warnings tell an operator to cover
// a disguise before the published pool does. ditto_pool REPLACES the published
// pool rather than adding to it, so the advice has to say the whole list goes
// in — adding the one entry alone would shrink the pool to it.
func dittoPoolAddAdvice(entry string) string {
	if dittoPoolOverridden() {
		return fmt.Sprintf("To cover it now, add %q to ditto_pool in config.", entry)
	}
	return fmt.Sprintf("To cover it now, set ditto_pool in config to the published pool's entries "+
		"plus %q — ditto_pool replaces the published pool, so listing %q alone would shrink it to that one entry.",
		entry, entry)
}

// unknownDisguiseDrops rate-limits logUnknownDittoDisguise. An unknown disguise
// is rare, but the one situation this instrumentation exists to catch — the
// derivation drifting away from the game, or a change in how weather boost is
// classified — is exactly the one that makes "opposite" the common outcome.
// At that point every encounter would emit this, on a path already saturated.
// One line still names a species to act on, and the count says how widespread
// it is.
var unknownDisguiseDrops util.DropReporter
