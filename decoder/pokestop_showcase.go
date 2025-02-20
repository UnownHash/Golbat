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

func createFocusStoreFromContestProto(contest *pogo.ContestProto) map[contestFocusType]map[string]any {
	focusStore := make(map[contestFocusType]map[string]any)

	for _, focus := range contest.GetFocuses() {
		if pok := focus.GetPokemon(); pok != nil {
			result := make(map[string]any)
			result["pokemon_id"] = int32(pok.PokedexId)
			if pok.RequireFormToMatch {
				result["pokemon_form"] = int32(pok.PokemonDisplay.Form)
			}
			focusStore[focusPokemon] = result
		}
		if pokType := focus.GetType(); pokType != nil {
			result := make(map[string]any)
			result["pokemon_type_1"] = int32(pokType.GetPokemonType1())
			if type2 := pokType.GetPokemonType2(); type2 != pogo.HoloPokemonType_POKEMON_TYPE_NONE {
				result["pokemon_type_2"] = int32(type2)
			}
			focusStore[focusPokemonType] = result
		}
		if alignment := focus.GetAlignment(); alignment != nil {
			// unset, purified, shadow
			focusStore[focusPokemonAlignment] = map[string]any{
				"pokemon_alignment": int32(alignment.GetRequiredAlignment()),
			}
		}
		if pokemonClass := focus.GetPokemonClass(); pokemonClass != nil {
			// normal, legendary, mythic, ultra beast
			focusStore[focusPokemonClass] = map[string]any{
				"pokemon_class": int32(pokemonClass.GetRequiredClass()),
			}
		}
		if pokemonFamily := focus.GetPokemonFamily(); pokemonFamily != nil {
			// family pikachu, zubat e.g.
			focusStore[focusPokemonFamily] = map[string]any{
				"pokemon_family": int32(pokemonFamily.GetRequiredFamily()),
			}
		}
		if buddy := focus.GetBuddy(); buddy != nil {
			focusStore[focusBuddy] = map[string]any{
				"min_level": int32(buddy.GetMinBuddyLevel()),
			}
		}
		if generation := focus.GetGeneration(); generation != nil {
			focusStore[focusGeneration] = map[string]any{
				"generation": int32(generation.GetPokemonGeneration()),
			}
		}
		if hatched := focus.GetHatched(); hatched != nil {
			focusStore[focusHatched] = map[string]any{
				"hatched": hatched.GetRequireToBeHatched(),
			}
		}
		if mega := focus.GetMega(); mega != nil {
			focusStore[focusMega] = map[string]any{
				"temp_evolution": int32(mega.GetTemporaryEvolutionRequired()),
				"restriction":    int32(mega.GetRestriction()),
			}
		}
		if shiny := focus.GetShiny(); shiny != nil {
			focusStore[focusShiny] = map[string]any{
				"shiny": shiny.GetRequireToBeShiny(),
			}
		}
	}
	return focusStore
}
