package decoder

import (
	"golbat/pogo"
)

type contestFocusType string

const (
	focusPokemon          contestFocusType = "pokemon"
	focusPokemonType      contestFocusType = "type"
	focusPokemonAlignment contestFocusType = "alignment"
	focusPokemonClass     contestFocusType = "class"
	focusPokemonFamily    contestFocusType = "family"
	focusBuddy            contestFocusType = "buddy"
	focusGeneration       contestFocusType = "generation"
	focusHatched          contestFocusType = "hatched"
	focusMega             contestFocusType = "mega"
	focusShiny            contestFocusType = "shiny"
)

type contestFocusRequirementProvider interface {
	Requirements() map[string]any
}
type contestPokemonFocus struct {
	*pogo.ContestPokemonFocusProto
}
type contestPokemonTypeFocus struct {
	*pogo.ContestPokemonTypeFocusProto
}
type contestPokemonAlignmentFocus struct {
	*pogo.ContestPokemonAlignmentFocusProto
}
type contestPokemonClassFocus struct {
	*pogo.ContestPokemonClassFocusProto
}
type contestPokemonFamilyFocus struct {
	*pogo.ContestPokemonFamilyFocusProto
}
type contestBuddyFocus struct {
	*pogo.ContestBuddyFocusProto
}
type contestGenerationFocus struct {
	*pogo.ContestGenerationFocusProto
}
type contestHatchedFocus struct {
	*pogo.ContestHatchedFocusProto
}
type contestMegaFocus struct {
	*pogo.ContestTemporaryEvolutionFocusProto
}
type contestShinyFocus struct {
	*pogo.ContestShinyFocusProto
}

func (p *contestPokemonFocus) Requirements() map[string]any {
	result := make(map[string]any)
	result["pokemon_id"] = p.PokedexId
	if p.RequireFormToMatch {
		result["pokemon_form"] = p.PokemonDisplay.Form
	}
	return result

}
func (p *contestPokemonTypeFocus) Requirements() map[string]any {
	result := make(map[string]any)
	result["pokemon_type_1"] = p.GetPokemonType1()
	if type2 := p.GetPokemonType2(); type2 != pogo.HoloPokemonType_POKEMON_TYPE_NONE {
		result["pokemon_type_2"] = type2
	}
	return result
}
func (p *contestPokemonAlignmentFocus) Requirements() map[string]any {
	// unset, purified, shadow
	return map[string]any{
		"pokemon_alignment": p.GetRequiredAlignment(),
	}
}
func (p *contestPokemonClassFocus) Requirements() map[string]any {
	// normal, legendary, mythic, ultra beast
	return map[string]any{
		"pokemon_class": p.GetRequiredClass(),
	}
}
func (p *contestPokemonFamilyFocus) Requirements() map[string]any {
	// family pikachu, zubat e.g.
	return map[string]any{
		"pokemon_family": p.GetRequiredFamily(),
	}
}
func (p *contestBuddyFocus) Requirements() map[string]any {
	return map[string]any{
		"min_level": p.GetMinBuddyLevel(),
	}
}
func (p *contestGenerationFocus) Requirements() map[string]any {
	// gen 1 - 9
	return map[string]any{
		"generation": p.GetPokemonGeneration(),
	}
}
func (p *contestHatchedFocus) Requirements() map[string]any {
	return map[string]any{
		"hatched": p.GetRequireToBeHatched(),
	}
}
func (p *contestMegaFocus) Requirements() map[string]any {
	// GetRestriction()                 -> MEGA, NOT_TEMP_EVO
	// GetTemporaryEvolutionRequired()  -> MEGA, MEGA_X, MEGA_Y, PRIMAL
	return map[string]any{
		"temp_evolution": p.GetTemporaryEvolutionRequired(),
		"restriction":    p.GetRestriction(),
	}
}
func (p *contestShinyFocus) Requirements() map[string]any {
	return map[string]any{
		"shiny": p.GetRequireToBeShiny(),
	}
}

func createFocusStoreFromContestProto(contest *pogo.ContestProto) map[contestFocusType]contestFocusRequirementProvider {
	focusStore := make(map[contestFocusType]contestFocusRequirementProvider)

	for _, focus := range contest.GetFocuses() {
		if pok := focus.GetPokemon(); pok != nil {
			focusStore[focusPokemon] = &contestPokemonFocus{pok}
		}
		if pokType := focus.GetType(); pokType != nil {
			focusStore[focusPokemonType] = &contestPokemonTypeFocus{pokType}
		}
		if alignment := focus.GetAlignment(); alignment != nil {
			focusStore[focusPokemonAlignment] = &contestPokemonAlignmentFocus{alignment}
		}
		if pokemonClass := focus.GetPokemonClass(); pokemonClass != nil {
			focusStore[focusPokemonClass] = &contestPokemonClassFocus{pokemonClass}
		}
		if pokemonFamily := focus.GetPokemonFamily(); pokemonFamily != nil {
			focusStore[focusPokemonFamily] = &contestPokemonFamilyFocus{pokemonFamily}
		}
		if buddy := focus.GetBuddy(); buddy != nil {
			focusStore[focusBuddy] = &contestBuddyFocus{buddy}
		}
		if generation := focus.GetGeneration(); generation != nil {
			focusStore[focusGeneration] = &contestGenerationFocus{generation}
		}
		if hatched := focus.GetHatched(); hatched != nil {
			focusStore[focusHatched] = &contestHatchedFocus{hatched}
		}
		if mega := focus.GetMega(); mega != nil {
			focusStore[focusMega] = &contestMegaFocus{mega}
		}
		if shiny := focus.GetShiny(); shiny != nil {
			focusStore[focusShiny] = &contestShinyFocus{shiny}
		}
	}
	return focusStore
}
