package decoder

import (
	"time"

	"golbat/derive/encounter"
)

// dimState is one dimension's verdict in the derivation events log: whether
// the derivation agreed with the game on it, disagreed, or had nothing to
// compare.
type dimState int8

const (
	// dimNoOpinion: nothing to compare — the value was not reported, the
	// form fixes the size, or the move pools did not resolve.
	dimNoOpinion dimState = iota
	dimAgree
	dimDisagree
)

// dimAgreement is classifyDimensions' per-dimension answer for one encounter.
type dimAgreement struct{ IVs, Level, Size, Quick, Charged dimState }

// Deviates reports whether any dimension disagreed. No opinion is not a
// deviation.
func (a dimAgreement) Deviates() bool {
	return a.IVs == dimDisagree || a.Level == dimDisagree || a.Size == dimDisagree ||
		a.Quick == dimDisagree || a.Charged == dimDisagree
}

// classifyDimensions splits one encounter's comparison into dimensions, so
// an event modifier shows up as the one dimension it moves (a level bonus,
// a forced move, shifted size cutoffs) rather than as a whole-row mismatch.
//
// IVs agree only when all three stats do. Size has no opinion when the game
// did not report one (0) or form fixes it (encounter.FormFixesSize). A move
// slot has no opinion when the pools did not resolve or the move was not
// reported (0).
//
// Unlike checkDerivation, an observed move outside its pool is a
// disagreement here, not "no opinion": an event forcing a move the species
// cannot roll is exactly what this log exists to find.
func classifyDimensions(observed, derived encounter.Values, form int32,
	moves derivedMoveResult, move1, move2 int32) dimAgreement {
	var a dimAgreement
	a.IVs = agreeIf(observed.Attack == derived.Attack &&
		observed.Defense == derived.Defense &&
		observed.Stamina == derived.Stamina)
	a.Level = agreeIf(observed.Level == derived.Level)
	if observed.Size != 0 && !encounter.FormFixesSize(form) {
		a.Size = agreeIf(observed.Size == derived.Size)
	}
	if moves.Resolved {
		a.Quick = classifyMove(move1, moves.Move1, moves.QuickPool)
		a.Charged = classifyMove(move2, moves.Move2, moves.ChargedPool)
	}
	return a
}

// classifyMove is one resolved move slot's verdict: no opinion when the move
// is unreported, disagreement when it is outside pool or not the derived one.
func classifyMove(observed, derived int32, pool []int) dimState {
	if observed == 0 {
		return dimNoOpinion
	}
	if !moveInPool(pool, observed) {
		return dimDisagree
	}
	return agreeIf(observed == derived)
}

func agreeIf(equal bool) dimState {
	if equal {
		return dimAgree
	}
	return dimDisagree
}

// derivationCheck is what checkDerivation computed for one encounter,
// returned so the derivation events log can reuse it without deriving again.
// Callers that ignore it are unaffected.
type derivationCheck struct {
	// Observed is the encounter's reported values; Size 0 is unreported.
	Observed encounter.Values
	// Derived is the branch scan's weather implies, Opposite the other one.
	Derived, Opposite encounter.Values
	// SizeRoll is Derived's raw size roll (encounter.Draw.SizeRoll).
	SizeRoll float64
	// Moves is the Derived branch's move draw; Move1/Move2 are the observed
	// moves (0 = not reported).
	Moves        derivedMoveResult
	Move1, Move2 int32
	Branch       dittoBranch
	Boosted      bool
}

// deriveEventCheck is one real encounter's entry for the derivation events
// log: the check plus the context the writer buckets and keys it by.
type deriveEventCheck struct {
	Time                              time.Time
	EncounterID                       uint64
	PokemonID, Form, Costume, Weather int32
	Strong, Boosted                   bool
	CellID                            int64   // the server's level-15 map cell (Pokemon.CellId); 0 = none
	Lat, Lon                          float64 // fallback when CellID is 0; never written
	SpawnID                           int64   // 0 = none; hashed by the writer, never written raw
	FirstSeen                         uint32  // 0 = unknown (set only in savePokemonRecord)
	Account                           string  // raw; hashed by the writer, never written raw
	Check                             derivationCheck
	Agree                             dimAgreement
}

// newDeriveEventCheck builds the log entry for one encounter, from the same
// pre-detectDitto scan checkDerivation was given. It returns ok=false for an
// encounter the log excludes: a known Ditto (pokemon.IsDitto), or one whose
// values match the opposite boost branch, which marks a likely Ditto. Either
// would read as an IV/level deviation that no event caused.
func newDeriveEventCheck(p *Pokemon, scan *pokemonScan, chk derivationCheck,
	username string, now time.Time) (deriveEventCheck, bool) {
	if p.IsDitto || chk.Branch == dittoBranchOpposite {
		return deriveEventCheck{}, false
	}
	return deriveEventCheck{
		Time:        now,
		EncounterID: uint64(p.Id),
		PokemonID:   scan.Pokemon,
		Form:        scan.Form,
		Costume:     scan.Costume,
		Weather:     scan.Weather,
		Strong:      scan.Strong,
		Boosted:     chk.Boosted,
		CellID:      p.CellId.ValueOrZero(),
		Lat:         p.Lat,
		Lon:         p.Lon,
		SpawnID:     p.SpawnId.ValueOrZero(),
		FirstSeen:   p.FirstSeenTimestamp,
		Account:     username,
		Check:       chk,
		Agree:       classifyDimensions(chk.Observed, chk.Derived, scan.Form, chk.Moves, chk.Move1, chk.Move2),
	}, true
}
