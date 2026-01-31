package decoder

import "github.com/guregu/null/v6"

type ApiPokestopResult struct {
	Id                         string      `json:"id"`
	Lat                        float64     `json:"lat"`
	Lon                        float64     `json:"lon"`
	Name                       null.String `json:"name"`
	Url                        null.String `json:"url"`
	LureExpireTimestamp        null.Int    `json:"lure_expire_timestamp"`
	LastModifiedTimestamp      null.Int    `json:"last_modified_timestamp"`
	Updated                    int64       `json:"updated"`
	Enabled                    null.Bool   `json:"enabled"`
	QuestType                  null.Int    `json:"quest_type"`
	QuestTimestamp             null.Int    `json:"quest_timestamp"`
	QuestTarget                null.Int    `json:"quest_target"`
	QuestConditions            null.String `json:"quest_conditions"`
	QuestRewards               null.String `json:"quest_rewards"`
	QuestTemplate              null.String `json:"quest_template"`
	QuestTitle                 null.String `json:"quest_title"`
	QuestExpiry                null.Int    `json:"quest_expiry"`
	CellId                     null.Int    `json:"cell_id"`
	Deleted                    bool        `json:"deleted"`
	LureId                     int16       `json:"lure_id"`
	FirstSeenTimestamp         int16       `json:"first_seen_timestamp"`
	SponsorId                  null.Int    `json:"sponsor_id"`
	PartnerId                  null.String `json:"partner_id"`
	ArScanEligible             null.Int    `json:"ar_scan_eligible"`
	PowerUpLevel               null.Int    `json:"power_up_level"`
	PowerUpPoints              null.Int    `json:"power_up_points"`
	PowerUpEndTimestamp        null.Int    `json:"power_up_end_timestamp"`
	AlternativeQuestType       null.Int    `json:"alternative_quest_type"`
	AlternativeQuestTimestamp  null.Int    `json:"alternative_quest_timestamp"`
	AlternativeQuestTarget     null.Int    `json:"alternative_quest_target"`
	AlternativeQuestConditions null.String `json:"alternative_quest_conditions"`
	AlternativeQuestRewards    null.String `json:"alternative_quest_rewards"`
	AlternativeQuestTemplate   null.String `json:"alternative_quest_template"`
	AlternativeQuestTitle      null.String `json:"alternative_quest_title"`
	AlternativeQuestExpiry     null.Int    `json:"alternative_quest_expiry"`
	Description                null.String `json:"description"`
	ShowcaseFocus              null.String `json:"showcase_focus"`
	ShowcasePokemon            null.Int    `json:"showcase_pokemon_id"`
	ShowcasePokemonForm        null.Int    `json:"showcase_pokemon_form_id"`
	ShowcasePokemonType        null.Int    `json:"showcase_pokemon_type_id"`
	ShowcaseRankingStandard    null.Int    `json:"showcase_ranking_standard"`
	ShowcaseExpiry             null.Int    `json:"showcase_expiry"`
	ShowcaseRankings           null.String `json:"showcase_rankings"`
}

func buildPokestopResult(stop *Pokestop) ApiPokestopResult {
	return ApiPokestopResult{
		Id:                         stop.Id,
		Lat:                        stop.Lat,
		Lon:                        stop.Lon,
		Name:                       stop.Name,
		Url:                        stop.Url,
		LureExpireTimestamp:        stop.LureExpireTimestamp,
		LastModifiedTimestamp:      stop.LastModifiedTimestamp,
		Updated:                    stop.Updated,
		Enabled:                    stop.Enabled,
		QuestType:                  stop.QuestType,
		QuestTimestamp:             stop.QuestTimestamp,
		QuestTarget:                stop.QuestTarget,
		QuestConditions:            stop.QuestConditions,
		QuestRewards:               stop.QuestRewards,
		QuestTemplate:              stop.QuestTemplate,
		QuestTitle:                 stop.QuestTitle,
		QuestExpiry:                stop.QuestExpiry,
		CellId:                     stop.CellId,
		Deleted:                    stop.Deleted,
		LureId:                     stop.LureId,
		FirstSeenTimestamp:         stop.FirstSeenTimestamp,
		SponsorId:                  stop.SponsorId,
		PartnerId:                  stop.PartnerId,
		ArScanEligible:             stop.ArScanEligible,
		PowerUpLevel:               stop.PowerUpLevel,
		PowerUpPoints:              stop.PowerUpPoints,
		PowerUpEndTimestamp:        stop.PowerUpEndTimestamp,
		AlternativeQuestType:       stop.AlternativeQuestType,
		AlternativeQuestTimestamp:  stop.AlternativeQuestTimestamp,
		AlternativeQuestTarget:     stop.AlternativeQuestTarget,
		AlternativeQuestConditions: stop.AlternativeQuestConditions,
		AlternativeQuestRewards:    stop.AlternativeQuestRewards,
		AlternativeQuestTemplate:   stop.AlternativeQuestTemplate,
		AlternativeQuestTitle:      stop.AlternativeQuestTitle,
		AlternativeQuestExpiry:     stop.AlternativeQuestExpiry,
		Description:                stop.Description,
		ShowcaseFocus:              stop.ShowcaseFocus,
		ShowcasePokemon:            stop.ShowcasePokemon,
		ShowcasePokemonForm:        stop.ShowcasePokemonForm,
		ShowcasePokemonType:        stop.ShowcasePokemonType,
		ShowcaseRankingStandard:    stop.ShowcaseRankingStandard,
		ShowcaseExpiry:             stop.ShowcaseExpiry,
		ShowcaseRankings:           stop.ShowcaseRankings,
	}
}

func BuildPokestopResult(stop *Pokestop) ApiPokestopResult {
	return buildPokestopResult(stop)
}
