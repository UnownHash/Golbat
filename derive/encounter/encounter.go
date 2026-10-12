// Package encounter answers what a wild encounter's values are, given its
// encounter id and whether it was weather boosted.
package encounter

import "golbat/derive/javarandom"

// Size classes, matching HoloPokemonSize.
const (
	SizeXXS int32 = 1
	SizeXS  int32 = 2
	SizeM   int32 = 3
	SizeXL  int32 = 4
	SizeXXL int32 = 5
)

// EncounterXor is the value the game uses to salt the encounter seed. Only the
// low 48 bits have any effect.
const EncounterXor = 0x18ADE5CD737B

// formSizes: Pumpkaboo and Gourgeist display a size fixed by their form
// rather than the ordinary rolled class. Keys are PokemonDisplayProto form
// enum values.
var formSizes = map[int32]int32{
	2641: SizeXS, 2642: SizeM, 2643: SizeXL, 2644: SizeXXL,
	2645: SizeXS, 2646: SizeM, 2647: SizeXL, 2648: SizeXXL,
}

type Values struct {
	Attack  int32
	Defense int32
	Stamina int32
	Level   int32
	Size    int32
}

// Draw is one encounter's derivation: the Values (embedded), the
// Ditto-candidate roll, and the generator state MoveIndices continues from.
// Every field is comparable, so two Draws compare directly.
type Draw struct {
	Values

	// DittoRoll is the encounter's 3rd advance read as nextFloat — the
	// primitive and the value the game's Ditto-candidate rule inspects.
	// Free: Next(bits) advances the LCG identically whatever bits is, so
	// reading 24 of them costs no more than discarding the advance.
	DittoRoll float32

	// gen is the live generator, positioned immediately after the size roll:
	// where MoveIndices continues from, and reachable without a pool size.
	gen javarandom.Random

	// moveIndicesDrawn guards MoveIndices against a second call.
	moveIndicesDrawn bool

	// Strong is whether this Draw is ForStrong's. A strong encounter's moves
	// are not derived (see ForStrong), so a caller writing moves checks it.
	Strong bool
}

// For derives one encounter's Values in a single walk, returning a Draw that
// MoveIndices continues. Pure: same inputs, same output.
func For(encounterID uint64, weatherBoosted bool, form int32) Draw {
	r := javarandom.New(encounterID ^ EncounterXor)

	// Advances 1 and 2 do not contribute to these values, but each one
	// advances the generator and so must be taken.
	r.Next(1)
	r.Next(1)
	// Advance 3 plays no part in Values either; it is read as nextFloat to
	// become DittoRoll.
	dittoRoll := r.Float()

	var floor, span, levelBonus int32
	if weatherBoosted {
		floor, span, levelBonus = 4, 12, 5
	} else {
		floor, span, levelBonus = 0, 16, 0
	}

	v := Values{
		Attack:  r.Int(span) + floor,
		Defense: r.Int(span) + floor,
		Stamina: r.Int(span) + floor,
		Level:   r.Int(30) + 1 + levelBonus,
	}
	v.Size = size(r, form)

	return Draw{
		Values:    v,
		DittoRoll: dittoRoll,
		gen:       *r,
	}
}

// ForStrong derives a strong (mighty) encounter's Values. The walk is
// For's with two more leading advances, IVs drawn from 13..15 whatever the
// weather, a level of 36..40, or 41..45 when weather boosted, and a size of
// XL or XXL only (strongSize).
//
// Every bound is the same in both weather branches, so the branches advance
// the generator identically: IVs, size and the move indices agree across
// them and the boosted level is the unboosted one plus 5.
//
// The move indices continue the walk as for an ordinary encounter and match
// where the game draws them, but an event can fix a strong species' move —
// often outside its pool — and a fixed slot takes no draw, shifting the
// other. Without a list of the fixed moves the indices cannot be trusted, so
// callers do not write moves from a strong Draw (see Draw.Strong).
//
// Checked against a scanner export of GO Wild Area 2025 (44,078 strong
// encounters) and two further datasets.
func ForStrong(encounterID uint64, weatherBoosted bool, form int32) Draw {
	r := javarandom.New(encounterID ^ EncounterXor)

	r.Next(1)
	r.Next(1)
	// Read as For reads it, so DittoRoll means the same thing on every Draw;
	// nothing consults it for a strong encounter.
	dittoRoll := r.Float()
	r.Next(1)
	r.Next(1)

	levelBase := int32(36)
	if weatherBoosted {
		levelBase = 41
	}

	v := Values{
		Attack:  r.Int(3) + 13,
		Defense: r.Int(3) + 13,
		Stamina: r.Int(3) + 13,
		Level:   r.Int(5) + levelBase,
	}
	v.Size = strongSize(r, form)

	return Draw{
		Values:    v,
		DittoRoll: dittoRoll,
		gen:       *r,
		Strong:    true,
	}
}

// strongSizeCut splits a strong encounter's size roll: XL below it, XXL at
// or above. Observed bracket 0.799975..0.800014 over 44,392 encounters, with
// no exception.
const strongSizeCut = 0.8

// strongSize is size for a strong encounter: the same draws, but only XL or
// XXL. A form with a fixed size keeps it, as for an ordinary encounter; no
// strong Pumpkaboo or Gourgeist has been observed to confirm that either way.
func strongSize(r *javarandom.Random, form int32) int32 {
	r.Gaussian()
	rolled := SizeXXL
	if r.Double() < strongSizeCut {
		rolled = SizeXL
	}
	if override, ok := formSizes[form]; ok {
		return override
	}
	return rolled
}

// Derive is For or ForStrong, chosen by whether the encounter is strong.
func Derive(encounterID uint64, weatherBoosted bool, strong bool, form int32) Draw {
	if strong {
		return ForStrong(encounterID, weatherBoosted, form)
	}
	return For(encounterID, weatherBoosted, form)
}

// dittoCandidateAdvanceIndex is the generator advance (1-based) the Ditto
// candidate rule reads. For captures the same advance as Draw.DittoRoll.
const dittoCandidateAdvanceIndex = 3

// DittoCandidateRoll reads advance dittoCandidateAdvanceIndex of one
// encounter's PRNG stream as nextFloat (a single next(24)): the one advance
// the game's Ditto candidate rule reads.
//
// The index is empirical, not specified: candidates predicted from advance 3
// were confirmed in game, and it agrees with Golbat's own cross-boost Ditto
// detection on every encounter that detection could resolve. Published
// descriptions of this rule name advance 2. They are wrong; do not change it.
//
// It uses a fresh generator, so it cannot disturb a derivation in progress.
// It is the roll-only path, 3 advances against For's ~15, for a caller with
// no Draw that wants nothing else from the encounter. A caller that also
// needs Values or moves should call For and read Draw.DittoRoll.
func DittoCandidateRoll(encounterID uint64) float32 {
	r := javarandom.New(encounterID ^ EncounterXor)

	var roll float32
	for i := 0; i < dittoCandidateAdvanceIndex; i++ {
		roll = r.Float()
	}
	return roll
}

func size(r *javarandom.Random, form int32) int32 {
	// The Gaussian draw is discarded, but it advances the generator a variable
	// number of times and so must be taken before the size roll.
	r.Gaussian()

	// nextDouble, not nextFloat: the size roll is the full 53-bit draw, and
	// it consumes two advances.
	rolled := classifySize(r.Double())

	if override, ok := formSizes[form]; ok {
		return override
	}
	return rolled
}

// MoveIndices draws the quick- and charged-move pool indices, continuing the
// walk For began rather than re-seeding. Pool sizes arrive as plain ints
// because this package knows nothing of golbat's masterfile; resolving them,
// including for an unknown species or one with no moves, is the caller's job.
// They must be positive, as javarandom.Int panics on a bound <= 0.
//
// It consumes generator state, so it may be called at most once per Draw and
// panics on a second call. The state a second draw would need is spent, and
// returning the first call's indices would look correct for whatever sizes
// the caller passed. A fresh pair of indices needs a fresh Draw.
func (d *Draw) MoveIndices(quickPoolSize, chargedPoolSize int32) (quickIndex, chargedIndex int32) {
	if d.moveIndicesDrawn {
		panic("encounter: MoveIndices called twice on the same Draw")
	}
	d.moveIndicesDrawn = true
	return d.gen.Int(quickPoolSize), d.gen.Int(chargedPoolSize)
}

// classifySize maps a roll in [0,1) to a size class. Separate from size so
// the boundaries can be tested directly: a live encounter's roll almost never
// lands between two of them, so a drift here would otherwise ship unnoticed.
//
// These cut points hold for every species outside events. A size event
// shifts them — one Rattata bonus moved the two lower cuts from 0.004 and
// 0.029 to 0.012 and 0.037 — and the derivation does not model events, so
// sizes derived during one can be a class off.
func classifySize(roll float64) int32 {
	switch {
	case roll < 0.004:
		return SizeXXS
	case roll < 0.029:
		return SizeXS
	case roll < 0.971:
		return SizeM
	case roll < 0.996:
		return SizeXL
	default:
		return SizeXXL
	}
}
