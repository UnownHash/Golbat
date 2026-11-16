package encounter

import "golbat/derive/javarandom"

// MiniorCoreRoll is the value that picks a wild Minior's core colour: the
// first nextFloat of java.util.Random seeded with the encounter id itself (not
// EncounterXor). The colour is the same for every player; the wild Pokemon
// only shows the colourless Meteor form.
func MiniorCoreRoll(encounterID uint64) float32 {
	return javarandom.New(encounterID).Float()
}

// CoreWeight is one colour of a weighted core pick.
type CoreWeight struct {
	Form   uint16
	Weight float64
}

// MiniorCoreForm walks table in order and returns the colour whose cumulative
// share first exceeds the roll. Weights are relative (they need not sum to 1);
// ok is false for an empty table or one with no positive weight. Which colours
// an event offers, and their weights, are not derivable.
func MiniorCoreForm(encounterID uint64, table []CoreWeight) (form uint16, ok bool) {
	var total float64
	for _, c := range table {
		if c.Weight > 0 {
			total += c.Weight
		}
	}
	if total <= 0 {
		return 0, false
	}
	roll := float64(MiniorCoreRoll(encounterID)) * total
	var acc float64
	last := uint16(0)
	for _, c := range table {
		if c.Weight <= 0 {
			continue
		}
		acc += c.Weight
		last = c.Form
		if roll < acc {
			return c.Form, true
		}
	}
	// Float rounding can leave roll == total; it belongs to the last colour.
	return last, true
}
