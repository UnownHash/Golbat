package decoder

import (
	"encoding/json"

	"golbat/pogo"
	"golbat/pogoshim"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
)

// extractShowcaseTop parses a ShowcaseRankings JSON blob and returns the
// rank-1 entry's score and pokemon_id. Used to detect leaderboard top-1
// movement for webhook firing.
//
// The blob is the JSON produced by updatePokestopFromGetPokemonSizeContestEntryOutProto,
// shape: {"contest_entries": [{"rank": 1, "score": ..., "pokemon_id": ...}, ...]}.
// Returns invalid null values when the blob is missing or has no rank-1 entry.
func extractShowcaseTop(rankings null.String) (null.Float, null.Int) {
	if !rankings.Valid {
		return null.Float{}, null.Int{}
	}
	type entry struct {
		Rank      int     `json:"rank"`
		Score     float64 `json:"score"`
		PokemonId int     `json:"pokemon_id"`
	}
	var data struct {
		ContestEntries []entry `json:"contest_entries"`
	}
	if err := json.Unmarshal([]byte(rankings.ValueOrZero()), &data); err != nil {
		return null.Float{}, null.Int{}
	}
	for _, e := range data.ContestEntries {
		if e.Rank == 1 {
			return null.FloatFrom(e.Score), null.IntFrom(int64(e.PokemonId))
		}
	}
	return null.Float{}, null.Int{}
}

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

func createFocusStoreFromContestProto(contest pogoshim.ContestProto) map[contestFocusType]map[string]any {
	focusStore := make(map[contestFocusType]map[string]any)

	for focus := range contest.GetFocuses().All() {
		// Has<Field>() replaces each nil-pointer check below: a shim getter
		// never returns Go nil (a zero-value ContestPokemonFocusProto etc.
		// with every Get* chaining to its own zero default), so "pok != nil"
		// has to become an explicit presence check on the parent field.
		if focus.HasPokemon() {
			pok := focus.GetPokemon()
			result := make(map[string]any)
			result["pokemon_id"] = int32(pok.GetPokedexId())
			if pok.GetRequireFormToMatch() {
				// pok.GetPokemonDisplay() degrades to a zero shim when
				// PokemonDisplay is absent (GetForm() -> 0) instead of the
				// pre-shim code's un-guarded pok.PokemonDisplay.Form, which
				// would nil-panic here if RequireFormToMatch were true with
				// no display set -- same latent-panic-removal class as every
				// prior wave's shim conversions.
				result["pokemon_form"] = int32(pok.GetPokemonDisplay().GetForm())
			}
			focusStore[focusPokemon] = result
		}
		if focus.HasType() {
			pokType := focus.GetType()
			result := make(map[string]any)
			result["pokemon_type_1"] = int32(pokType.GetPokemonType1())
			if type2 := pokType.GetPokemonType2(); type2 != pogo.HoloPokemonType_POKEMON_TYPE_NONE {
				result["pokemon_type_2"] = int32(type2)
			}
			focusStore[focusPokemonType] = result
		}
		if focus.HasAlignment() {
			// unset, purified, shadow
			focusStore[focusPokemonAlignment] = map[string]any{
				"pokemon_alignment": int32(focus.GetAlignment().GetRequiredAlignment()),
			}
		}
		if focus.HasPokemonClass() {
			// normal, legendary, mythic, ultra beast
			focusStore[focusPokemonClass] = map[string]any{
				"pokemon_class": int32(focus.GetPokemonClass().GetRequiredClass()),
			}
		}
		if focus.HasPokemonFamily() {
			// family pikachu, zubat e.g.
			focusStore[focusPokemonFamily] = map[string]any{
				"pokemon_family": int32(focus.GetPokemonFamily().GetRequiredFamily()),
			}
		}
		if focus.HasBuddy() {
			focusStore[focusBuddy] = map[string]any{
				"min_level": int32(focus.GetBuddy().GetMinBuddyLevel()),
			}
		}
		if focus.HasGeneration() {
			focusStore[focusGeneration] = map[string]any{
				"generation": int32(focus.GetGeneration().GetPokemonGeneration()),
			}
		}
		if focus.HasHatched() {
			focusStore[focusHatched] = map[string]any{
				"hatched": focus.GetHatched().GetRequireToBeHatched(),
			}
		}
		if focus.HasMega() {
			mega := focus.GetMega()
			focusStore[focusMega] = map[string]any{
				"temp_evolution": int32(mega.GetTemporaryEvolutionRequired()),
				"restriction":    int32(mega.GetRestriction()),
			}
		}
		if focus.HasShiny() {
			focusStore[focusShiny] = map[string]any{
				"shiny": focus.GetShiny().GetRequireToBeShiny(),
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
			stop.SetShowcasePokemon(null.IntFrom(int64(pokemonID)))
		} else {
			log.Warnf("SHOWCASE: Stop '%s' - Missing or invalid 'pokemon_id'", stop.Id)
			stop.SetShowcasePokemon(null.IntFromPtr(nil))
		}

		if form, ok := focus["pokemon_form"].(int32); ok {
			stop.SetShowcasePokemonForm(null.IntFrom(int64(form)))
		} else {
			stop.SetShowcasePokemonForm(null.IntFromPtr(nil))
		}
	} else {
		stop.SetShowcasePokemon(null.IntFromPtr(nil))
		stop.SetShowcasePokemonForm(null.IntFromPtr(nil))
	}

	if key == focusPokemonType {
		if type1, ok := focus["pokemon_type_1"].(int32); ok {
			stop.SetShowcasePokemonType(null.IntFrom(int64(type1)))
		} else {
			log.Warnf("SHOWCASE: Stop '%s' - Missing or invalid 'pokemon_type_1'", stop.Id)
			stop.SetShowcasePokemonType(null.IntFromPtr(nil))
		}

		if type2, ok := focus["pokemon_type_2"].(int32); ok {
			if type2Int64 := int64(type2); type2Int64 != 0 {
				log.Warnf("SHOWCASE: Stop: '%s' with Focused Pokemon Type 2: %d", stop.Id, type2Int64)
			}
		}
	} else {
		stop.SetShowcasePokemonType(null.IntFromPtr(nil))
	}
}
