package decoder

// ApiPokestopResult is the API representation of a pokestop. Nullable database
// columns are represented as pointers (nil => JSON null) without omitempty so
// every key is always present.
type ApiPokestopResult struct {
	Id                         string  `json:"id" doc:"Fort ID of the pokestop"`
	Lat                        float64 `json:"lat" doc:"Latitude of the pokestop"`
	Lon                        float64 `json:"lon" doc:"Longitude of the pokestop"`
	Name                       *string `json:"name" doc:"Name of the pokestop"`
	Url                        *string `json:"url" doc:"Image URL of the pokestop"`
	LureExpireTimestamp        *int64  `json:"lure_expire_timestamp" doc:"Unix timestamp when the current lure expires"`
	LastModifiedTimestamp      *int64  `json:"last_modified_timestamp" doc:"Unix timestamp when the pokestop was last modified in-game"`
	Updated                    int64   `json:"updated" doc:"Unix timestamp when the record was last updated"`
	Enabled                    *bool   `json:"enabled" doc:"Whether the pokestop is enabled"`
	QuestType                  *int64  `json:"quest_type" doc:"Type of the AR quest"`
	QuestTimestamp             *int64  `json:"quest_timestamp" doc:"Unix timestamp when the AR quest was set"`
	QuestTarget                *int64  `json:"quest_target" doc:"Target count for the AR quest"`
	QuestConditions            *string `json:"quest_conditions" doc:"Serialized conditions of the AR quest"`
	QuestRewards               *string `json:"quest_rewards" doc:"Serialized rewards of the AR quest"`
	QuestTemplate              *string `json:"quest_template" doc:"Template ID of the AR quest"`
	QuestTitle                 *string `json:"quest_title" doc:"Title of the AR quest"`
	QuestExpiry                *int64  `json:"quest_expiry" doc:"Unix timestamp when the AR quest expires"`
	CellId                     *int64  `json:"cell_id" doc:"S2 cell ID the pokestop belongs to"`
	Deleted                    bool    `json:"deleted" doc:"Whether the pokestop has been deleted"`
	LureId                     int16   `json:"lure_id" doc:"ID of the current lure module"`
	FirstSeenTimestamp         int16   `json:"first_seen_timestamp" doc:"Unix timestamp when the pokestop was first seen"`
	SponsorId                  *int64  `json:"sponsor_id" doc:"Sponsor ID of the pokestop, if sponsored"`
	PartnerId                  *string `json:"partner_id" doc:"Partner ID of the pokestop, if partnered"`
	ArScanEligible             *int64  `json:"ar_scan_eligible" doc:"Whether the pokestop is eligible for AR scanning"`
	PowerUpLevel               *int64  `json:"power_up_level" doc:"Power-up level of the pokestop"`
	PowerUpPoints              *int64  `json:"power_up_points" doc:"Power-up points accumulated for the pokestop"`
	PowerUpEndTimestamp        *int64  `json:"power_up_end_timestamp" doc:"Unix timestamp when the power-up ends"`
	AlternativeQuestType       *int64  `json:"alternative_quest_type" doc:"Type of the non-AR quest"`
	AlternativeQuestTimestamp  *int64  `json:"alternative_quest_timestamp" doc:"Unix timestamp when the non-AR quest was set"`
	AlternativeQuestTarget     *int64  `json:"alternative_quest_target" doc:"Target count for the non-AR quest"`
	AlternativeQuestConditions *string `json:"alternative_quest_conditions" doc:"Serialized conditions of the non-AR quest"`
	AlternativeQuestRewards    *string `json:"alternative_quest_rewards" doc:"Serialized rewards of the non-AR quest"`
	AlternativeQuestTemplate   *string `json:"alternative_quest_template" doc:"Template ID of the non-AR quest"`
	AlternativeQuestTitle      *string `json:"alternative_quest_title" doc:"Title of the non-AR quest"`
	AlternativeQuestExpiry     *int64  `json:"alternative_quest_expiry" doc:"Unix timestamp when the non-AR quest expires"`
	Description                *string `json:"description" doc:"Description of the pokestop"`
	ShowcaseFocus              *string `json:"showcase_focus" doc:"Focus type of the showcase contest"`
	ShowcasePokemon            *int64  `json:"showcase_pokemon_id" doc:"Pokedex ID of the showcase contest pokemon"`
	ShowcasePokemonForm        *int64  `json:"showcase_pokemon_form_id" doc:"Form ID of the showcase contest pokemon"`
	ShowcasePokemonType        *int64  `json:"showcase_pokemon_type_id" doc:"Type ID of the showcase contest pokemon"`
	ShowcaseRankingStandard    *int64  `json:"showcase_ranking_standard" doc:"Ranking standard of the showcase contest"`
	ShowcaseExpiry             *int64  `json:"showcase_expiry" doc:"Unix timestamp when the showcase contest expires"`
	ShowcaseRankings           *string `json:"showcase_rankings" doc:"Serialized showcase contest rankings"`
}

func buildPokestopResult(stop *Pokestop) ApiPokestopResult {
	return ApiPokestopResult{
		Id:                         stop.Id,
		Lat:                        stop.Lat,
		Lon:                        stop.Lon,
		Name:                       stop.Name.Ptr(),
		Url:                        stop.Url.Ptr(),
		LureExpireTimestamp:        stop.LureExpireTimestamp.Ptr(),
		LastModifiedTimestamp:      stop.LastModifiedTimestamp.Ptr(),
		Updated:                    stop.Updated,
		Enabled:                    stop.Enabled.Ptr(),
		QuestType:                  stop.QuestType.Ptr(),
		QuestTimestamp:             stop.QuestTimestamp.Ptr(),
		QuestTarget:                stop.QuestTarget.Ptr(),
		QuestConditions:            stop.QuestConditions.Ptr(),
		QuestRewards:               stop.QuestRewards.Ptr(),
		QuestTemplate:              stop.QuestTemplate.Ptr(),
		QuestTitle:                 stop.QuestTitle.Ptr(),
		QuestExpiry:                stop.QuestExpiry.Ptr(),
		CellId:                     stop.CellId.Ptr(),
		Deleted:                    stop.Deleted,
		LureId:                     stop.LureId,
		FirstSeenTimestamp:         stop.FirstSeenTimestamp,
		SponsorId:                  stop.SponsorId.Ptr(),
		PartnerId:                  stop.PartnerId.Ptr(),
		ArScanEligible:             stop.ArScanEligible.Ptr(),
		PowerUpLevel:               stop.PowerUpLevel.Ptr(),
		PowerUpPoints:              stop.PowerUpPoints.Ptr(),
		PowerUpEndTimestamp:        stop.PowerUpEndTimestamp.Ptr(),
		AlternativeQuestType:       stop.AlternativeQuestType.Ptr(),
		AlternativeQuestTimestamp:  stop.AlternativeQuestTimestamp.Ptr(),
		AlternativeQuestTarget:     stop.AlternativeQuestTarget.Ptr(),
		AlternativeQuestConditions: stop.AlternativeQuestConditions.Ptr(),
		AlternativeQuestRewards:    stop.AlternativeQuestRewards.Ptr(),
		AlternativeQuestTemplate:   stop.AlternativeQuestTemplate.Ptr(),
		AlternativeQuestTitle:      stop.AlternativeQuestTitle.Ptr(),
		AlternativeQuestExpiry:     stop.AlternativeQuestExpiry.Ptr(),
		Description:                stop.Description.Ptr(),
		ShowcaseFocus:              stop.ShowcaseFocus.Ptr(),
		ShowcasePokemon:            stop.ShowcasePokemon.Ptr(),
		ShowcasePokemonForm:        stop.ShowcasePokemonForm.Ptr(),
		ShowcasePokemonType:        stop.ShowcasePokemonType.Ptr(),
		ShowcaseRankingStandard:    stop.ShowcaseRankingStandard.Ptr(),
		ShowcaseExpiry:             stop.ShowcaseExpiry.Ptr(),
		ShowcaseRankings:           stop.ShowcaseRankings.Ptr(),
	}
}

func BuildPokestopResult(stop *Pokestop) ApiPokestopResult {
	return buildPokestopResult(stop)
}
