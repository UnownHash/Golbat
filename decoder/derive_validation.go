package decoder

import (
	"encoding/json"
	"slices"

	"golbat/config"
	"golbat/derive/encounter"
	"golbat/pogo"

	log "github.com/sirupsen/logrus"
)

// checkDerivation compares one real encounter's observed stats against a
// fresh derivation for the same encounter id and boost state, updates the
// accuracy counters, and logs the outcome when the derivation disagreed, an
// observed move is absent from the pool the derivation drew from, or the Ditto
// signals conflict (see dittoSignalConflict).
//
// scan must be the snapshot addEncounterPokemon takes BEFORE detectDitto
// runs. detectDitto rewrites the record afterwards and mutates scan.Weather
// in place to record a Ditto's reclassified boost state, so a late snapshot
// would compare against the wrong branch for exactly the Ditto population.
// wasRollMarked and hadDetectEvidence must come from that same
// pre-detectDitto point, for the same reason: detectDitto clears
// dittoRollMarked when it resets a roll-marked record, so a live read here
// would report false for precisely the sightings these distinguish.
//
// size is proto.Size, and move1/move2 are proto.Move1/Move2, passed
// alongside scan because pokemonScan carries neither. A size of 0 is
// POKEMON_SIZE_UNSET — not reported, not a disagreement — and is excluded
// from the comparison; counting it would give derivation_disagreed_total a
// permanent floor from every proto that omits size.
//
// A strong Pokemon is scored against the strong walk (encounter.ForStrong).
// That walk rests on a small sample, so every strong encounter is logged,
// agreeing or not ("strong" in logged_because): the corpus then holds the
// strong agreement rate, not only the failures.
//
// The record's state before this encounter (oldValues, snapshotted when the
// record was locked) says how it was last seen. When that sighting's strong
// flag differs from the encounter's, what was derived from it used the wrong
// walk, and the row is logged as "strong_changed" with the prior seen type,
// so a source that does not carry the flag (nearby, say) shows up by name.
func (pokemon *Pokemon) checkDerivation(scan *pokemonScan, size int32, move1 int32, move2 int32, path string, wasRollMarked bool, hadDetectEvidence bool) {
	encounterID := uint64(pokemon.Id)
	boosted := scan.Weather != int32(pogo.GameplayWeatherProto_NONE)
	derived := encounter.Derive(encounterID, boosted, scan.Strong, scan.Form)

	// oppositeValues is the other boost branch. It is computed for every
	// encounter, not only logged ones, because dittoBranchFor needs it to
	// drive derivation_disagreed_total and the unknown-disguise check below.
	// It is PRNG arithmetic only; the masterfile lookup a logged row needs
	// (disguiseBoostedWeathers) stays deferred to logDerivationOutcome.
	oppositeValues := encounter.Derive(encounterID, !boosted, scan.Strong, scan.Form).Values

	observed := encounter.Values{
		Attack:  scan.Attack,
		Defense: scan.Defense,
		Stamina: scan.Stamina,
		Level:   scan.Level,
		Size:    size,
	}

	// Moves are drawn from the DISPLAYED species/form's pools, the pair the
	// game drew from. When the pools do not resolve there is nothing to
	// compare against, so every move signal below stays false.
	//
	// move1OutOfPool/move2OutOfPool, per slot: the observed move appears
	// nowhere in the pool the derivation drew that slot's index from.
	moves := derivedMoves(&derived, int16(scan.Pokemon), int16(scan.Form))
	derivedMove1, derivedMove2 := moves.Move1, moves.Move2
	movePoolsResolved := moves.Resolved
	// A move of 0 is "not reported": an encounter that omits its moves says
	// nothing about them, so the slot is neither a mismatch nor out of pool.
	move1Reported, move2Reported := move1 != 0, move2 != 0
	var move1OutOfPool, move2OutOfPool bool
	if movePoolsResolved {
		move1OutOfPool = move1Reported && !moveInPool(moves.QuickPool, move1)
		move2OutOfPool = move2Reported && !moveInPool(moves.ChargedPool, move2)
	}
	// A slot is a mismatch only when its pool resolved and the observed move
	// is reported and in it. An unresolved pool, an unreported move, or one no
	// index into the pool could produce (a granted event or legacy move, not a
	// rolled one) means the derivation has no opinion. Counting "no opinion"
	// as a disagreement would give derivation_disagreed_total a permanent
	// floor and mask the failures it exists to catch.
	move1Mismatch := movePoolsResolved && move1Reported && !move1OutOfPool && move1 != derivedMove1
	move2Mismatch := movePoolsResolved && move2Reported && !move2OutOfPool && move2 != derivedMove2
	moveMismatch := move1Mismatch || move2Mismatch
	// moveOutOfPool is a reason to log, never part of mismatch. It must be a
	// logging trigger of its own: it is not a mismatch, so nothing else would
	// log the row, and its rate and species breakdown show whether it spreads.
	moveOutOfPool := move1OutOfPool || move2OutOfPool

	// See dittoBranch for the four states and why "opposite" indicates a Ditto.
	branch := dittoBranchFor(observed, derived.Values, oppositeValues, scan.Strong)

	// For a non-strong Pokemon only dittoBranchNeither is a derivation
	// failure. dittoBranchOpposite is a Ditto whose IVs follow its own
	// typing rather than its disguise's; a raw observed-vs-derived compare
	// would count it and put a floor under derivation_disagreed_total.
	//
	// dittoBranchFor reports dittoBranchUnknown for a strong Pokemon, which
	// the Ditto comparison does not cover, so a strong one is compared
	// against the expected branch alone.
	var ivMismatch bool
	if scan.Strong {
		ivMismatch = !boostBranchValuesMatch(observed, derived.Values)
	} else {
		ivMismatch = branch == dittoBranchNeither
	}
	mismatch := ivMismatch || moveMismatch
	dittoPoolMember := inDittoPool(int16(scan.Pokemon), int16(scan.Form))

	getStatsCollector().IncDerivationChecked(path)
	if mismatch {
		getStatsCollector().IncDerivationDisagreed(path)
	}
	// Counted separately as well, so a move-only disagreement can be told
	// apart from an IV/level/size one without reading logs.
	if moveMismatch {
		getStatsCollector().IncDerivationMoveMismatch(path)
	}
	// The "opposite" population ivMismatch excludes: evidence of a Ditto,
	// not a derivation failure, so it has its own counter. Never a strong
	// Pokemon, whose branch is unknown.
	if branch == dittoBranchOpposite {
		getStatsCollector().IncDerivationBoostBranchDivergence(path)
	}

	// A record with no valid seen type had no sighting before this one.
	prior := pokemon.oldValues
	priorKnown := prior.SeenType.Valid
	strongChanged := priorKnown && prior.IsStrong != scan.Strong

	// A row is written for a disagreement: a value mismatch, an observed move
	// outside its pool, a conflict between the Ditto signals (see
	// dittoSignalConflict), or a strong flag the previous sighting got wrong.
	// Agreement is counted above and not logged, except for a strong
	// Pokemon, which is always logged.
	//
	// config.Config.DerivationLog gates only the [DERIVE] payload. The
	// counters above and the unknown-disguise warning below stay on: an
	// operator needs derivation_disagreed_total to notice the model breaking,
	// and needs to hear about a disguise the pool does not know, without
	// opting into the validation log.
	loggedBecause := derivationLogReasons(mismatch, moveOutOfPool,
		// The roll never marks a strong Pokemon (maybeMarkDittoFromRoll), so it
		// does not fire for one.
		dittoSignalConflict(!scan.Strong && (wasRollMarked || (dittoPoolMember && dittoSuspectRoll(derived.DittoRoll))),
			pokemon.IsDitto, !pokemon.IsDitto && hadDetectEvidence,
			branch == dittoBranchOpposite),
		scan.Strong, strongChanged)
	shouldLog := config.Config.DerivationLog && len(loggedBecause) > 0

	// Independent of shouldLog: see logUnknownDittoDisguise. An in-pool
	// "opposite" row is the pool's own business, and is logged when the roll
	// did not fire for it.
	if branch == dittoBranchOpposite && !dittoPoolMember {
		logUnknownDittoDisguise(encounterID, scan.Pokemon, scan.Form)
	}

	if shouldLog {
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
			ObservedMove1:     move1,
			ObservedMove2:     move2,
			LoggedBecause:     loggedBecause,
			PriorKnown:        priorKnown,
			PriorIsStrong:     prior.IsStrong,
			PriorSeenType:     prior.SeenType.Code,
		})
	}
}

// dittoSignalConflict reports whether the three Ditto signals disagree, and
// how. Agreement is not logged: with three independent signals, a row where
// they concur says nothing about which one is wrong, and such rows would be
// almost the whole corpus.
//
//   - rollFired: the record was roll-marked, or is a pool disguise whose
//     encounter-id roll is below the threshold — the roll says Ditto. A mark
//     counts even if the pool has since dropped the disguise.
//   - detectConfirmed: the record is a Ditto. A roll-marked record counts as
//     rollFired too, so its own mark reads as agreement, never as a Ditto
//     the roll missed.
//   - detectRejected: not a Ditto, and detectDitto had the evidence to say so.
//   - opposite: the observed values match the other boost branch, which a
//     Ditto's do when its boost state and its disguise's differ.
//
// The conflicts are the roll marking what detectDitto refutes, detectDitto
// confirming what the roll passed over, and the values showing a Ditto where
// the roll did not fire — including outside the pool, where it cannot.
func dittoSignalConflict(rollFired, detectConfirmed, detectRejected, opposite bool) string {
	switch {
	case rollFired && detectRejected:
		return "roll_refuted"
	case !rollFired && detectConfirmed:
		return "roll_missed_confirmed"
	case !rollFired && opposite:
		return "roll_missed_opposite"
	}
	return ""
}

// derivationLogReasons lists why a [DERIVE] row is written; an empty list
// means it is not. The reasons travel in the row, so a reader knows the
// corpus holds disagreements only — apart from "strong", which samples every
// strong encounter and which a reader holds apart for that reason.
func derivationLogReasons(mismatch, moveOutOfPool bool, dittoConflict string, strong, strongChanged bool) []string {
	var reasons []string
	if mismatch {
		reasons = append(reasons, "mismatch")
	}
	if moveOutOfPool {
		reasons = append(reasons, "move_out_of_pool")
	}
	if dittoConflict != "" {
		reasons = append(reasons, dittoConflict)
	}
	if strongChanged {
		reasons = append(reasons, "strong_changed")
	}
	if strong {
		reasons = append(reasons, "strong")
	}
	return reasons
}

// derivedMoveResult is derivedMoves' answer.
type derivedMoveResult struct {
	Move1, Move2           int32
	QuickPool, ChargedPool []int
	// Resolved is false — and every other field its zero value — when the
	// pools do not resolve at all: unknown species, an empty pool, or an
	// uninitialized masterfile.
	Resolved bool
}

// derivedMoves resolves pokemonId/form's masterfile move pools, draws the
// pool indices with v.MoveIndices, and translates them to move ids. Both
// applyDerivedMoves (writing a live record) and checkDerivation (auditing
// one) use it, so the audit checks exactly what production writes.
//
// It spends v's remaining generator state: MoveIndices may run once per Draw.
// The Initialized guard is the same as disguiseBoostedWeathers'.
// QuickPool/ChargedPool are returned so a caller testing an observed move for
// membership need not resolve them again.
func derivedMoves(v *encounter.Draw, pokemonId, form int16) derivedMoveResult {
	data := getMasterFileData()
	if !data.Initialized {
		return derivedMoveResult{}
	}
	quick, charged, ok := movePoolsFor(data, pokemonId, form)
	if !ok {
		return derivedMoveResult{}
	}
	quickIndex, chargedIndex := v.MoveIndices(int32(len(quick)), int32(len(charged)))
	return derivedMoveResult{
		Move1:       int32(quick[quickIndex]),
		Move2:       int32(charged[chargedIndex]),
		QuickPool:   quick,
		ChargedPool: charged,
		Resolved:    true,
	}
}

// moveInPool reports whether move appears anywhere in pool, by value — not
// whether it sits at any particular index. Used to tell "not in this pool at
// all" (no opinion; see the move1Mismatch/move2Mismatch comment in
// checkDerivation) apart from "in the pool, but at the wrong index" (a real
// mismatch, still counted as one).
func moveInPool(pool []int, move int32) bool {
	return slices.Contains(pool, int(move))
}

// derivedBranch mirrors encounter.Values with lower_snake_case JSON keys;
// encounter.Values carries no json tags.
type derivedBranch struct {
	Attack  int32 `json:"attack"`
	Defense int32 `json:"defense"`
	Stamina int32 `json:"stamina"`
	Level   int32 `json:"level"`
	Size    int32 `json:"size"`
}

func newDerivedBranch(v encounter.Values) derivedBranch {
	return derivedBranch{Attack: v.Attack, Defense: v.Defense, Stamina: v.Stamina, Level: v.Level, Size: v.Size}
}

// derivationLogPayload is the log row's shape, marshaled to JSON and carried
// in the message rather than as logrus fields.
//
// Golbat's only installed formatter renders level + timestamp +
// entry.Message and never reads entry.Data, which is what log.WithFields
// populates — fields set that way are silently dropped. Interpolating JSON
// into the message survives the formatter and stays machine-parseable.
type derivationLogPayload struct {
	// EncounterID is tagged ",string": encounter ids exceed 2^53, and a bare
	// JSON number is silently truncated by float64 parsers — every common
	// consumer (jq before 1.7, JavaScript, Go's own map[string]any) decodes
	// JSON numbers as float64. Untagged, this field — the corpus's join key
	// — would corrupt on ingestion even though the bytes golbat emits are
	// exact. See TestLogDerivationOutcomeEncodesEncounterIdAsExactJSONString.
	EncounterID      uint64 `json:"encounter_id,string"`
	Path             string `json:"path"`
	DisplayedSpecies int32  `json:"displayed_species"`
	Form             int32  `json:"form"`
	Costume          int32  `json:"costume"`
	IsDitto          bool   `json:"is_ditto"`
	DisplayPokemonId uint16 `json:"display_pokemon_id"`
	// DisguiseBoostedWeathers is the findBoostedWeathers mask for the
	// DISPLAYED species/form (DisplayedSpecies/Form), never Ditto's own
	// Normal typing: bit i is set when GameplayWeatherProto condition i
	// boosts it. Test bits with weatherBoosts. Context only; nothing branches
	// on it. It cannot identify a Ditto, because the game reports
	// weather_boosted_condition for the disguise too, so the two agree by
	// construction; see dittoBranch.
	//
	// disguiseBoostedWeathersUnavailable (-1) means the masterfile had not
	// loaded when the line was logged. 0 is a real answer: no weather boosts
	// this species.
	DisguiseBoostedWeathers int32 `json:"disguise_boosted_weathers"`
	// DittoRoll is the one advance the candidate rule reads: advance 3 as
	// nextFloat (a single next(24) as a float32), taken from the Draw
	// checkDerivation already computed; encounter.DittoCandidateRoll is the
	// same value. Any other advance can be recomputed from encounter_id.
	DittoRoll float32 `json:"ditto_roll"`
	// DittoPoolMember is inDittoPool(scan.Pokemon, scan.Form) when the line
	// was written. The pool is an observed sample that grows, partly from
	// this log, so after a pool change displayed_species alone cannot say
	// whether a species was in the pool at write time; the boolean pins it,
	// keeping correlations across a pool change sound.
	DittoPoolMember bool `json:"ditto_pool_member"`
	// DittoSuspectRoll is ditto_roll < dittoCandidateThreshold, recomputable
	// as `float32(ditto_roll) < float32(0.02)`.
	//
	// It is not gated on dittoPoolMember, although the candidate rule is "in
	// pool AND low roll": the pool is an observed sample, and gating would
	// hide a low roll on a species not yet in it, which is the evidence for
	// adding it. ditto_pool_member is logged alongside, so a reader can
	// apply the full rule.
	DittoSuspectRoll bool `json:"ditto_suspect_roll"`
	// DittoBranch is dittoBranchFor's result: which of derived_unboosted/
	// derived_boosted the observed values match, relative to the branch
	// weather_boosted_condition implies. See dittoBranch.
	DittoBranch dittoBranch `json:"ditto_branch"`
	// DittoDetect is whether detectDitto had the evidence to reach a
	// conclusion, not just its answer; see dittoDetect and
	// hadCrossBoostScanHistory.
	DittoDetect             dittoDetect `json:"ditto_detect"`
	WeatherBoostedCondition int32       `json:"weather_boosted_condition"`
	// CellWeather is the weather of the cell this encounter happened in, and
	// is not always observed: when the weather cache misses for a Ditto, it is
	// INFERRED from the observed values (see dittoBoostFromObservedValues),
	// because the branch they match is the cell's answer for a Normal-typed
	// Ditto. Sound, but an analysis that treats this column as a raw
	// observation for Dittos is reading one conclusion as two.
	CellWeather      int32         `json:"cell_weather"`
	DerivedUnboosted derivedBranch `json:"derived_unboosted"`
	DerivedBoosted   derivedBranch `json:"derived_boosted"`
	ObservedAttack   int32         `json:"observed_attack"`
	ObservedDefense  int32         `json:"observed_defense"`
	ObservedStamina  int32         `json:"observed_stamina"`
	ObservedLevel    int32         `json:"observed_level"`
	ObservedSize     int32         `json:"observed_size"`
	// DerivedMove1/DerivedMove2 are Draw.MoveIndices' pool-index draws
	// translated through the displayed species/form's pools, for scan's own
	// boost state only: the branch that decides moveMismatch.
	//
	// Both are 0 (MOVE_UNSET, never a real pool entry) when the pools did not
	// resolve; checkDerivation does not compare them then, so a 0 beside a
	// non-zero Observed* is not a disagreement.
	//
	// An observed legacy or elite move outside the species' masterfile pools
	// (Lickitung's Body Slam, for one) is not a mismatch but still logs, as
	// move_out_of_pool; ObservedMove1/2 beside DerivedMove1/2 shows which case
	// a row is.
	DerivedMove1  int32 `json:"derived_move_1"`
	DerivedMove2  int32 `json:"derived_move_2"`
	ObservedMove1 int32 `json:"observed_move_1"`
	ObservedMove2 int32 `json:"observed_move_2"`
	IsStrong      bool  `json:"is_strong"`
	// Model is the walk derived_unboosted, derived_boosted and the derived
	// moves came from: "strong" (encounter.ForStrong) or "ordinary"
	// (encounter.For). Rows written before strong derivation carry no model,
	// and their strong rows were derived with the ordinary walk.
	Model string `json:"model"`
	// PriorIsStrong and PriorSeenType are the record's strong flag and seen
	// type before this encounter, absent when there was no earlier sighting.
	// A prior_is_strong that differs from is_strong means whatever was
	// derived from that sighting used the wrong walk.
	PriorIsStrong *bool  `json:"prior_is_strong,omitempty"`
	PriorSeenType string `json:"prior_seen_type,omitempty"`
	// DittoRollMarked is whether the record was roll-marked
	// (maybeMarkDittoFromRoll; config ditto_derivation/ditto_pool) rather than
	// confirmed by detectDitto, which is what lets roll marking be audited.
	// It is captured before detectDitto can reset the mark (see
	// checkDerivation), so it is set even when this encounter's detectDitto
	// went on to clear it.
	DittoRollMarked bool `json:"ditto_roll_marked"`
	// LoggedBecause is why this row was written: "mismatch",
	// "move_out_of_pool", or one of dittoSignalConflict's conflicts. Rows are
	// written only for these, so a corpus that carries the field holds
	// disagreements and nothing else.
	LoggedBecause []string `json:"logged_because"`
}

// disguiseBoostedWeathersUnavailable is the sentinel
// derivationLogPayload.DisguiseBoostedWeathers carries when the masterfile
// was not yet initialized — see that field's doc comment for why 0 cannot
// serve as the sentinel.
const disguiseBoostedWeathersUnavailable int32 = -1

// disguiseBoostedWeathers returns the weather-boost bitmask for the
// displayed species/form (scan.Pokemon/scan.Form), or
// disguiseBoostedWeathersUnavailable if the masterfile has not finished
// loading.
//
// The Initialized guard also keeps the log quiet: findBoostedWeathers warns,
// without rate limiting, for every pokemonId it does not recognize, and
// before the first masterfile load that is every one, so each sighting
// logged in that window would emit an "Unknown PokemonId" warning. A species
// missing from a loaded masterfile still warns on every call.
func disguiseBoostedWeathers(scan *pokemonScan) int32 {
	data := getMasterFileData()
	if !data.Initialized {
		return disguiseBoostedWeathersUnavailable
	}
	return int32(findBoostedWeathers(data, int16(scan.Pokemon), int16(scan.Form)))
}

// dittoDetect records detectDitto's epistemic state for one sighting — did
// it have the cross-boost evidence needed to reach a conclusion, not merely
// what that conclusion was. is_ditto (a bool) cannot make this distinction
// once set to false: it looks identical whether detectDitto positively ruled
// "not a Ditto" from real history, or never had two boost states to compare
// in the first place. Only the former is usable as ground truth.
//
// This is the third of Ditto identity's three independent signals — Golbat's
// own detectDitto (this field), the PRNG roll (ditto_roll/
// ditto_suspect_roll), and the boost-branch comparison (ditto_branch) — and
// this field is what lets the corpus build the 2x2 the roll rule's
// false-positive rate depends on:
//
//	                 | detect confirmed   | detect rejected  | no evidence
//	roll fired        | true positive       | false positive   | unresolved prediction
//	roll did not fire | false negative      | true negative    | no information
//
// The roll rule's characteristic failure mode is a false positive, and a
// false positive can only be measured against a CONFIRMED negative — the
// "detect rejected" column above. is_ditto:false alone cannot separate that
// column from "no evidence" (the right-hand one), which is why this field
// exists: without it the corpus can count confirmed positives and nothing
// else, and half the 2x2 is uncomputable.
//
// A string enum, like dittoBranch, so the states cannot be confused by a
// reader of the corpus.
type dittoDetect string

const (
	// dittoDetectConfirmed: pokemon.IsDitto is set, by detectDitto or by the
	// roll rule; ditto_roll_marked says which.
	dittoDetectConfirmed dittoDetect = "confirmed"
	// dittoDetectRejected: not a Ditto, AND detectDitto had evidence for that
	// — either both boost states in history (hadCrossBoostScanHistory), or it
	// actively cleared the flag during this decode, which its 0N and BN
	// branches can do from a single prior scan. The second case carries no
	// cross-boost history at all and would otherwise read as "no evidence"
	// while being the clearest evidence there is. A genuine, usable negative:
	// the "detect rejected" column in the table above.
	dittoDetectRejected dittoDetect = "rejected"
	// dittoDetectNoEvidence: not a Ditto, but detectDitto had at most ONE
	// boost state to look at — sightings in only one boost state so far, or
	// none at all — so it had nothing to cross-check and its "not a Ditto"
	// answer carries no information.
	dittoDetectNoEvidence dittoDetect = "no_evidence"
)

// derivationOutcomeParams carries checkDerivation's already-computed results
// into logDerivationOutcome. It is filled with a keyed literal because
// several fields are adjacent values of one type (two bool pairs, four int32
// in a row) that a positional call could transpose and still compile.
type derivationOutcomeParams struct {
	Observed          encounter.Values
	Path              string
	WasRollMarked     bool
	HadDetectEvidence bool
	Derived           encounter.Draw
	// OppositeValues is the other boost branch's Values.
	OppositeValues  encounter.Values
	Boosted         bool
	DittoPoolMember bool
	Branch          dittoBranch
	DerivedMove1    int32
	DerivedMove2    int32
	ObservedMove1   int32
	ObservedMove2   int32
	LoggedBecause   []string
	// PriorKnown is whether the record had a sighting before this encounter;
	// PriorIsStrong and PriorSeenType describe it when it did.
	PriorKnown    bool
	PriorIsStrong bool
	PriorSeenType SeenTypeCode
}

// logDerivationOutcome emits one structured line recording how the
// derivation compared against a real encounter.
//
// pokemon supplies is_ditto and display_pokemon_id, read AFTER detectDitto
// has run: they are Golbat's own Ditto determination, not raw proto fields.
// Everything else per-sighting comes from scan, the pre-detectDitto snapshot
// (see checkDerivation) — reading species, form, costume or weather off
// pokemon here would pick up detectDitto's rewrite for exactly the Ditto
// population.
//
// DittoDetect combines pokemon.IsDitto with hadDetectEvidence. IsDitto
// wins outright as "confirmed" whatever the history, since a confirmed
// Ditto's provenance may be a roll-mark that never had any (ditto_roll_marked
// records that separately). Only when IsDitto is false does the history
// decide between "rejected" — a genuine negative — and "no_evidence", where
// detectDitto had nothing to compare.
//
// disguiseMask is the one value computed here rather than in
// checkDerivation: it needs a masterfile lookup, so it is paid only for rows
// actually logged.
func logDerivationOutcome(pokemon *Pokemon, scan *pokemonScan, outcome derivationOutcomeParams) {
	encounterID := uint64(pokemon.Id)
	unboosted, boostedValues := outcome.Derived.Values, outcome.OppositeValues
	if outcome.Boosted {
		unboosted, boostedValues = outcome.OppositeValues, outcome.Derived.Values
	}

	disguiseMask := disguiseBoostedWeathers(scan)

	// IsDitto wins over the history; see the doc comment above.
	dittoDetectState := dittoDetectNoEvidence
	switch {
	case pokemon.IsDitto:
		dittoDetectState = dittoDetectConfirmed
	case outcome.HadDetectEvidence:
		dittoDetectState = dittoDetectRejected
	}

	payload := derivationLogPayload{
		EncounterID:             encounterID,
		Path:                    outcome.Path,
		DisplayedSpecies:        scan.Pokemon,
		Form:                    scan.Form,
		Costume:                 scan.Costume,
		IsDitto:                 pokemon.IsDitto,
		DisplayPokemonId:        pokemon.DisplayPokemonId.ValueOrZero(),
		DisguiseBoostedWeathers: disguiseMask,
		DittoRoll:               outcome.Derived.DittoRoll,
		DittoPoolMember:         outcome.DittoPoolMember,
		DittoSuspectRoll:        dittoSuspectRoll(outcome.Derived.DittoRoll),
		DittoBranch:             outcome.Branch,
		DittoDetect:             dittoDetectState,
		WeatherBoostedCondition: scan.Weather,
		CellWeather:             scan.CellWeather,
		DerivedUnboosted:        newDerivedBranch(unboosted),
		DerivedBoosted:          newDerivedBranch(boostedValues),
		ObservedAttack:          outcome.Observed.Attack,
		ObservedDefense:         outcome.Observed.Defense,
		ObservedStamina:         outcome.Observed.Stamina,
		ObservedLevel:           outcome.Observed.Level,
		ObservedSize:            outcome.Observed.Size,
		DerivedMove1:            outcome.DerivedMove1,
		DerivedMove2:            outcome.DerivedMove2,
		ObservedMove1:           outcome.ObservedMove1,
		ObservedMove2:           outcome.ObservedMove2,
		IsStrong:                scan.Strong,
		Model:                   derivationModel(scan.Strong),
		DittoRollMarked:         outcome.WasRollMarked,
		LoggedBecause:           outcome.LoggedBecause,
	}

	if outcome.PriorKnown {
		priorIsStrong := outcome.PriorIsStrong
		payload.PriorIsStrong = &priorIsStrong
		payload.PriorSeenType = outcome.PriorSeenType.String()
	}

	data, err := json.Marshal(payload)
	if err != nil {
		// Unreachable in practice — every field is a plain number, bool, or
		// string — but this is an observational path: falling back to a
		// plain error log rather than panicking keeps a marshal failure
		// from ever taking down a real encounter.
		log.Errorf("[DERIVE] failed to marshal validation outcome for encounter %d: %s", encounterID, err)
		return
	}

	log.Infof("[DERIVE] %s", data)
}

// derivationModel names the walk a row's derived values came from; see
// derivationLogPayload.Model.
func derivationModel(strong bool) string {
	if strong {
		return "strong"
	}
	return "ordinary"
}
