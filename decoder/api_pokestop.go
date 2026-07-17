package decoder

import (
	"context"
	"encoding/json"

	db "golbat/db"
)

// ApiPokestopResult is the API representation of a pokestop. Nullable database
// columns are represented as pointers (nil => JSON null) without omitempty so
// every key is always present.
type ApiPokestopResult struct {
	Id                            string                `json:"id" doc:"Fort ID of the pokestop"`
	Lat                           float64               `json:"lat" doc:"Latitude of the pokestop"`
	Lon                           float64               `json:"lon" doc:"Longitude of the pokestop"`
	Name                          *string               `json:"name" doc:"Name of the pokestop"`
	Url                           *string               `json:"url" doc:"Image URL of the pokestop"`
	LureExpireTimestamp           *int64                `json:"lure_expire_timestamp" doc:"Unix timestamp when the current lure expires"`
	LastModifiedTimestamp         *int64                `json:"last_modified_timestamp" doc:"Unix timestamp when the pokestop was last modified in-game"`
	Updated                       int64                 `json:"updated" doc:"Unix timestamp when the record was last updated"`
	Enabled                       *bool                 `json:"enabled" doc:"Whether the pokestop is enabled"`
	QuestType                     *int64                `json:"quest_type" doc:"Type of the AR quest"`
	QuestTimestamp                *int64                `json:"quest_timestamp" doc:"Unix timestamp when the AR quest was set"`
	QuestTarget                   *int64                `json:"quest_target" doc:"Target count for the AR quest"`
	QuestRewardType               *int64                `json:"quest_reward_type" doc:"Reward type of the AR quest (generated from quest_rewards[0].type)"`
	QuestItemId                   *int64                `json:"quest_item_id" doc:"Item id of the AR quest reward, if an item reward"`
	QuestRewardAmount             *int64                `json:"quest_reward_amount" doc:"Reward amount of the AR quest"`
	QuestPokemonId                *int64                `json:"quest_pokemon_id" doc:"Pokemon id of the AR quest reward, if a pokemon/candy reward"`
	QuestPokemonFormId            *int64                `json:"quest_pokemon_form_id" doc:"Form id of the AR quest reward pokemon, if a pokemon reward"`
	QuestConditions               *string               `json:"quest_conditions" doc:"Serialized conditions of the AR quest"`
	QuestRewards                  *json.RawMessage      `json:"quest_rewards" doc:"Rewards of the AR quest as native JSON (array of {type, info}); null when no quest"`
	QuestTemplate                 *string               `json:"quest_template" doc:"Template ID of the AR quest"`
	QuestTitle                    *string               `json:"quest_title" doc:"Title of the AR quest"`
	QuestExpiry                   *int64                `json:"quest_expiry" doc:"Unix timestamp when the AR quest expires"`
	CellId                        *int64                `json:"cell_id" doc:"S2 cell ID the pokestop belongs to"`
	Deleted                       bool                  `json:"deleted" doc:"Whether the pokestop has been deleted"`
	LureId                        int16                 `json:"lure_id" doc:"ID of the current lure module"`
	FirstSeenTimestamp            int16                 `json:"first_seen_timestamp" doc:"Unix timestamp when the pokestop was first seen"`
	SponsorId                     *int64                `json:"sponsor_id" doc:"Sponsor ID of the pokestop, if sponsored"`
	PartnerId                     *string               `json:"partner_id" doc:"Partner ID of the pokestop, if partnered"`
	ArScanEligible                *int64                `json:"ar_scan_eligible" doc:"Whether the pokestop is eligible for AR scanning"`
	PowerUpLevel                  *int64                `json:"power_up_level" doc:"Power-up level of the pokestop"`
	PowerUpPoints                 *int64                `json:"power_up_points" doc:"Power-up points accumulated for the pokestop"`
	PowerUpEndTimestamp           *int64                `json:"power_up_end_timestamp" doc:"Unix timestamp when the power-up ends"`
	AlternativeQuestType          *int64                `json:"alternative_quest_type" doc:"Type of the non-AR quest"`
	AlternativeQuestTimestamp     *int64                `json:"alternative_quest_timestamp" doc:"Unix timestamp when the non-AR quest was set"`
	AlternativeQuestTarget        *int64                `json:"alternative_quest_target" doc:"Target count for the non-AR quest"`
	AlternativeQuestRewardType    *int64                `json:"alternative_quest_reward_type" doc:"Reward type of the non-AR quest (generated from alternative_quest_rewards[0].type)"`
	AlternativeQuestItemId        *int64                `json:"alternative_quest_item_id" doc:"Item id of the non-AR quest reward, if an item reward"`
	AlternativeQuestRewardAmount  *int64                `json:"alternative_quest_reward_amount" doc:"Reward amount of the non-AR quest"`
	AlternativeQuestPokemonId     *int64                `json:"alternative_quest_pokemon_id" doc:"Pokemon id of the non-AR quest reward, if a pokemon/candy reward"`
	AlternativeQuestPokemonFormId *int64                `json:"alternative_quest_pokemon_form_id" doc:"Form id of the non-AR quest reward pokemon, if a pokemon reward"`
	AlternativeQuestConditions    *string               `json:"alternative_quest_conditions" doc:"Serialized conditions of the non-AR quest"`
	AlternativeQuestRewards       *json.RawMessage      `json:"alternative_quest_rewards" doc:"Rewards of the non-AR quest as native JSON (array of {type, info}); null when no quest"`
	AlternativeQuestTemplate      *string               `json:"alternative_quest_template" doc:"Template ID of the non-AR quest"`
	AlternativeQuestTitle         *string               `json:"alternative_quest_title" doc:"Title of the non-AR quest"`
	AlternativeQuestExpiry        *int64                `json:"alternative_quest_expiry" doc:"Unix timestamp when the non-AR quest expires"`
	Description                   *string               `json:"description" doc:"Description of the pokestop"`
	ShowcaseFocus                 *string               `json:"showcase_focus" doc:"Focus type of the showcase contest"`
	ShowcasePokemon               *int64                `json:"showcase_pokemon_id" doc:"Pokedex ID of the showcase contest pokemon"`
	ShowcasePokemonForm           *int64                `json:"showcase_pokemon_form_id" doc:"Form ID of the showcase contest pokemon"`
	ShowcasePokemonType           *int64                `json:"showcase_pokemon_type_id" doc:"Type ID of the showcase contest pokemon"`
	ShowcaseRankingStandard       *int64                `json:"showcase_ranking_standard" doc:"Ranking standard of the showcase contest"`
	ShowcaseExpiry                *int64                `json:"showcase_expiry" doc:"Unix timestamp when the showcase contest expires"`
	ShowcaseRankings              *string               `json:"showcase_rankings" doc:"Serialized showcase contest rankings"`
	Invasions                     []ApiPokestopIncident `json:"invasions,omitempty" doc:"Active incidents; present when the pokestop has active incidents (always attempted on by-id, on scans only when with_incidents is set)"`
}

func buildPokestopResult(stop *Pokestop) ApiPokestopResult {
	return ApiPokestopResult{
		Id:                            stop.Id,
		Lat:                           stop.Lat,
		Lon:                           stop.Lon,
		Name:                          stop.Name.Ptr(),
		Url:                           stop.Url.Ptr(),
		LureExpireTimestamp:           stop.LureExpireTimestamp.Ptr(),
		LastModifiedTimestamp:         stop.LastModifiedTimestamp.Ptr(),
		Updated:                       stop.Updated,
		Enabled:                       stop.Enabled.Ptr(),
		QuestType:                     stop.QuestType.Ptr(),
		QuestTimestamp:                stop.QuestTimestamp.Ptr(),
		QuestTarget:                   stop.QuestTarget.Ptr(),
		QuestRewardType:               stop.QuestRewardType.Ptr(),
		QuestItemId:                   stop.QuestItemId.Ptr(),
		QuestRewardAmount:             stop.QuestRewardAmount.Ptr(),
		QuestPokemonId:                stop.QuestPokemonId.Ptr(),
		QuestPokemonFormId:            stop.QuestPokemonFormId.Ptr(),
		QuestConditions:               stop.QuestConditions.Ptr(),
		QuestRewards:                  jsonRaw(stop.QuestRewards),
		QuestTemplate:                 stop.QuestTemplate.Ptr(),
		QuestTitle:                    stop.QuestTitle.Ptr(),
		QuestExpiry:                   stop.QuestExpiry.Ptr(),
		CellId:                        stop.CellId.Ptr(),
		Deleted:                       stop.Deleted,
		LureId:                        stop.LureId,
		FirstSeenTimestamp:            stop.FirstSeenTimestamp,
		SponsorId:                     stop.SponsorId.Ptr(),
		PartnerId:                     stop.PartnerId.Ptr(),
		ArScanEligible:                stop.ArScanEligible.Ptr(),
		PowerUpLevel:                  stop.PowerUpLevel.Ptr(),
		PowerUpPoints:                 stop.PowerUpPoints.Ptr(),
		PowerUpEndTimestamp:           stop.PowerUpEndTimestamp.Ptr(),
		AlternativeQuestType:          stop.AlternativeQuestType.Ptr(),
		AlternativeQuestTimestamp:     stop.AlternativeQuestTimestamp.Ptr(),
		AlternativeQuestTarget:        stop.AlternativeQuestTarget.Ptr(),
		AlternativeQuestRewardType:    stop.AlternativeQuestRewardType.Ptr(),
		AlternativeQuestItemId:        stop.AlternativeQuestItemId.Ptr(),
		AlternativeQuestRewardAmount:  stop.AlternativeQuestRewardAmount.Ptr(),
		AlternativeQuestPokemonId:     stop.AlternativeQuestPokemonId.Ptr(),
		AlternativeQuestPokemonFormId: stop.AlternativeQuestPokemonFormId.Ptr(),
		AlternativeQuestConditions:    stop.AlternativeQuestConditions.Ptr(),
		AlternativeQuestRewards:       jsonRaw(stop.AlternativeQuestRewards),
		AlternativeQuestTemplate:      stop.AlternativeQuestTemplate.Ptr(),
		AlternativeQuestTitle:         stop.AlternativeQuestTitle.Ptr(),
		AlternativeQuestExpiry:        stop.AlternativeQuestExpiry.Ptr(),
		Description:                   stop.Description.Ptr(),
		ShowcaseFocus:                 stop.ShowcaseFocus.Ptr(),
		ShowcasePokemon:               stop.ShowcasePokemon.Ptr(),
		ShowcasePokemonForm:           stop.ShowcasePokemonForm.Ptr(),
		ShowcasePokemonType:           stop.ShowcasePokemonType.Ptr(),
		ShowcaseRankingStandard:       stop.ShowcaseRankingStandard.Ptr(),
		ShowcaseExpiry:                stop.ShowcaseExpiry.Ptr(),
		ShowcaseRankings:              stop.ShowcaseRankings.Ptr(),
	}
}

func BuildPokestopResult(stop *Pokestop) ApiPokestopResult {
	return buildPokestopResult(stop)
}

// ApiPokestopIncident is one active incident (whole row) on a pokestop, as
// returned in a scan/by-id response when with_incidents is set. Sourced from
// incidentCache via the FortLookup fetch handle; nullable slots are pointers.
type ApiPokestopIncident struct {
	Id             string `json:"id" doc:"Incident id"`
	PokestopId     string `json:"pokestop_id" doc:"Fort id of the parent pokestop"`
	DisplayType    int16  `json:"display_type" doc:"Incident display type (1-4 rocket, 7 goldstop, 8 kecleon, 9 showcase)"`
	Style          int16  `json:"style" doc:"Incident style"`
	Character      int16  `json:"character" doc:"Invasion character id (grunt/leader/giovanni); 0 for non-rocket"`
	StartTime      int64  `json:"start" doc:"Unix timestamp when the incident started"`
	ExpirationTime int64  `json:"expiration" doc:"Unix timestamp when the incident expires"`
	Confirmed      bool   `json:"confirmed" doc:"True when the lineup is confirmed (grunts only)"`
	Updated        int64  `json:"updated" doc:"Unix timestamp when the incident was last updated"`
	Slot1PokemonId *int64 `json:"slot_1_pokemon_id" doc:"Confirmed lead pokemon id, else null"`
	Slot1Form      *int64 `json:"slot_1_form" doc:"Confirmed lead pokemon form, else null"`
	Slot2PokemonId *int64 `json:"slot_2_pokemon_id" doc:"Slot 2 pokemon id, else null"`
	Slot2Form      *int64 `json:"slot_2_form" doc:"Slot 2 form, else null"`
	Slot3PokemonId *int64 `json:"slot_3_pokemon_id" doc:"Slot 3 pokemon id, else null"`
	Slot3Form      *int64 `json:"slot_3_form" doc:"Slot 3 form, else null"`
}

func buildPokestopIncident(inc *Incident) ApiPokestopIncident {
	return ApiPokestopIncident{
		Id:             inc.Id,
		PokestopId:     inc.PokestopId,
		DisplayType:    inc.DisplayType,
		Style:          inc.Style,
		Character:      inc.Character,
		StartTime:      inc.StartTime,
		ExpirationTime: inc.ExpirationTime,
		Confirmed:      inc.Confirmed,
		Updated:        inc.Updated,
		Slot1PokemonId: inc.Slot1PokemonId.Ptr(),
		Slot1Form:      inc.Slot1Form.Ptr(),
		Slot2PokemonId: inc.Slot2PokemonId.Ptr(),
		Slot2Form:      inc.Slot2Form.Ptr(),
		Slot3PokemonId: inc.Slot3PokemonId.Ptr(),
		Slot3Form:      inc.Slot3Form.Ptr(),
	}
}

// CollectPokestopIncidents returns the whole-row active incidents for a fort,
// resolved from incidentCache via the string handles in the fort's FortLookup
// (read-through to DB on the rare cache miss). Callers MUST NOT hold the
// pokestop lock — this locks incidents, and saveIncidentRecord locks
// incident->pokestop, so holding pokestop here would invert the order.
func CollectPokestopIncidents(ctx context.Context, dbDetails db.DbDetails, fortId string, now int64) []ApiPokestopIncident {
	fl, ok := fortLookupCache.Load(fortId)
	if !ok || len(fl.Incidents) == 0 {
		return nil
	}
	out := make([]ApiPokestopIncident, 0, len(fl.Incidents))
	for _, li := range fl.Incidents {
		if li.ExpireTimestamp <= now || li.Id == "" {
			continue
		}
		inc, unlock, err := getIncidentRecordReadOnly(ctx, dbDetails, li.Id, "API.CollectPokestopIncidents")
		if err != nil || inc == nil {
			if unlock != nil {
				unlock()
			}
			continue
		}
		out = append(out, buildPokestopIncident(inc))
		unlock()
	}
	return out
}
