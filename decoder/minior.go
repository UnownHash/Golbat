package decoder

import (
	"golbat/config"
	"golbat/derive/encounter"
	"golbat/pogo"

	"github.com/guregu/null/v6"
)

// A wild Minior shows MINIOR_METEOR and turns into a core colour when caught.
// With derive_iv on, the colour is derived from the encounter id: form is what
// you catch, display_pokemon_id/form is what the map shows.

// miniorCores is the core pick, in roll order: green 5%, then yellow.
var miniorCores = []encounter.CoreWeight{
	{Form: uint16(pogo.PokemonDisplayProto_MINIOR_GREEN), Weight: 5},
	{Form: uint16(pogo.PokemonDisplayProto_MINIOR_YELLOW), Weight: 95},
}

// hasDerivedDisplay reports whether the record is a Minior whose core was
// derived.
func (pokemon *Pokemon) hasDerivedDisplay() bool {
	return !pokemon.IsDitto && pokemon.DisplayPokemonId.Valid
}

// shownForm is the form the map reports for this record.
func (pokemon *Pokemon) shownForm() int64 {
	if pokemon.hasDerivedDisplay() {
		return int64OrZero(pokemon.DisplayPokemonForm)
	}
	return int64OrZero(pokemon.Form)
}

// deriveMiniorCore sets a Meteor Minior's form to its derived core colour and
// moves the Meteor form to the display fields. No-op once derived.
func (pokemon *Pokemon) deriveMiniorCore() {
	if pokemon.PokemonId != int16(pogo.HoloPokemonId_MINIOR) ||
		int64OrZero(pokemon.Form) != int64(pogo.PokemonDisplayProto_MINIOR_METEOR) ||
		!config.Config.DeriveIv {
		return
	}
	form, ok := encounter.MiniorCoreForm(uint64(pokemon.Id), miniorCores)
	if !ok {
		return
	}
	pokemon.SetDisplayPokemonId(null.IntFrom(int64(pokemon.PokemonId)))
	pokemon.SetDisplayPokemonForm(nullIntFromUint(pokemon.Form))
	pokemon.SetForm(null.IntFrom(int64(form)))
}
