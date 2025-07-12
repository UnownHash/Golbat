package decoder

import (
	"golbat/pogo"

	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
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

// Deprecated: to support backward compatibility - can be removed if external tools don't reference it anymore
// this info is now stored in showcase_focus directly
func (stop *Pokestop) extractShowcasePokemonInfoDeprecated(key contestFocusType, focus map[string]any) {
	if key == focusPokemon {
		if pokemonID, ok := focus["pokemon_id"].(int32); ok {
			stop.ShowcasePokemon = null.IntFrom(int64(pokemonID))
		} else {
			log.Warnf("SHOWCASE: Stop '%s' - Missing or invalid 'pokemon_id'", stop.Id)
			stop.ShowcasePokemon = null.IntFromPtr(nil)
		}

		if form, ok := focus["pokemon_form"].(int32); ok {
			stop.ShowcasePokemonForm = null.IntFrom(int64(form))
		} else {
			stop.ShowcasePokemonForm = null.IntFromPtr(nil)
		}
	} else {
		stop.ShowcasePokemon = null.IntFromPtr(nil)
		stop.ShowcasePokemonForm = null.IntFromPtr(nil)
	}

	if key == focusPokemonType {
		if type1, ok := focus["pokemon_type_1"].(int32); ok {
			stop.ShowcasePokemonType = null.IntFrom(int64(type1))
		} else {
			log.Warnf("SHOWCASE: Stop '%s' - Missing or invalid 'pokemon_type_1'", stop.Id)
			stop.ShowcasePokemonType = null.IntFromPtr(nil)
		}

		if type2, ok := focus["pokemon_type_2"].(int32); ok {
			if type2Int64 := int64(type2); type2Int64 != 0 {
				log.Warnf("SHOWCASE: Stop: '%s' with Focused Pokemon Type 2: %d", stop.Id, type2Int64)
			}
		}
	} else {
		stop.ShowcasePokemonType = null.IntFromPtr(nil)
	}
}
