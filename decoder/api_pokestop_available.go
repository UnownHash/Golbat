package decoder

import (
	log "github.com/sirupsen/logrus"
)

// ApiPokestopQuestAvailable is one distinct quest option (reward + title +
// target, AR/no-AR distinguished by WithAr) currently offered by resident
// pokestops, with how many forts offer it. Sourced solely from the maintained
// quest-conditions map (Task 3) — FortLookup itself omits title/target.
type ApiPokestopQuestAvailable struct {
	WithAr     bool   `json:"with_ar" doc:"True for the AR quest slot, false for the no-AR slot"`
	RewardType int16  `json:"reward_type" doc:"Quest reward type (pogo enum: 1=xp, 2=item, 3=stardust, 4=candy, 7=pokemon, 9=xl_candy, 12=mega_energy, …)"`
	ItemId     int16  `json:"item_id" doc:"Item id when reward_type is item (2), else 0"`
	Amount     int16  `json:"amount" doc:"Reward amount for stardust/xp/mega-energy rewards, else 0"`
	PokemonId  int16  `json:"pokemon_id" doc:"Reward pokemon id for candy/xl-candy/pokemon/mega rewards, else 0"`
	FormId     int16  `json:"form_id" doc:"Reward pokemon form id, else 0"`
	Title      string `json:"title" doc:"Quest title/template string, for the advanced title/target sub-filter"`
	Target     int32  `json:"target" doc:"Quest target count"`
	Count      int    `json:"count" doc:"Number of resident forts currently offering this exact reward+title+target"`
}

// ApiPokestopInvasionAvailable is one distinct active invasion (grunt/leader/
// showcase character + slot1 reward) currently present on resident
// pokestops, with how many forts carry it. Sourced from FortLookup.Incidents.
type ApiPokestopInvasionAvailable struct {
	Character      int16 `json:"character" doc:"Invasion character id (grunt/leader/giovanni); 0 for non-rocket displays"`
	DisplayType    int16 `json:"display_type" doc:"Incident display type (1-4 rocket, 7 goldstop, 8 kecleon, 9 showcase/contest)"`
	Confirmed      bool  `json:"confirmed" doc:"True when the lineup is confirmed (grunts only)"`
	Slot1PokemonId int16 `json:"slot1_pokemon_id" doc:"Confirmed lead pokemon id (grunts only), else 0"`
	Slot1Form      int16 `json:"slot1_form" doc:"Confirmed lead pokemon form, else 0"`
}

// ApiPokestopLureAvailable is one distinct active lure type currently carried
// by resident pokestops.
type ApiPokestopLureAvailable struct {
	LureId int16 `json:"lure_id" doc:"Active lure module id"`
}

// ApiPokestopShowcaseAvailable is one distinct active showcase contest
// (pokemon/form/type) currently run by resident pokestops.
type ApiPokestopShowcaseAvailable struct {
	PokemonId int16 `json:"pokemon_id" doc:"Showcase focus pokemon id, else 0"`
	Form      int16 `json:"form" doc:"Showcase focus pokemon form, else 0"`
	TypeId    int8  `json:"type_id" doc:"Showcase focus pokemon type id (type-based showcases), else 0"`
}

// ApiAvailablePokestops is the whole-instance snapshot served by
// GET /api/pokestop/available.
type ApiAvailablePokestops struct {
	Quests    []ApiPokestopQuestAvailable    `json:"quests" doc:"Distinct quest reward + title/target options currently offered"`
	Invasions []ApiPokestopInvasionAvailable `json:"invasions" doc:"Distinct active invasion signatures"`
	Lures     []ApiPokestopLureAvailable     `json:"lures" doc:"Distinct active lure module ids"`
	Showcases []ApiPokestopShowcaseAvailable `json:"showcases" doc:"Distinct active showcase focus pokemon/type"`
}

// buildAvailablePokestops assembles the pokestop availability snapshot from
// the maintained lure/showcase/invasion indexes and the maintained
// quest-conditions aggregate (quests unchanged) — no fort scan, no logging.
// Shared by GetAvailablePokestops (which logs its own line for the per-type
// endpoint) and GetAvailableForts (which folds these counts into its single
// combined log line instead of logging again here).
func buildAvailablePokestops(now int64) *ApiAvailablePokestops {
	res := &ApiAvailablePokestops{
		Quests:    []ApiPokestopQuestAvailable{},
		Invasions: readInvasions(now),
		Lures:     readLures(now),
		Showcases: readShowcases(now),
	}
	for _, c := range GetAvailableQuestConditions() {
		res.Quests = append(res.Quests, ApiPokestopQuestAvailable(c))
	}
	return res
}

// GetAvailablePokestops reads the maintained lure/showcase/invasion indexes and
// the maintained quest-conditions aggregate (quests unchanged) — no fort scan.
func GetAvailablePokestops(now int64) *ApiAvailablePokestops {
	res := buildAvailablePokestops(now)
	log.Infof("available-pokestops: %d quests, %d invasions, %d lures, %d showcases (maintained)",
		len(res.Quests), len(res.Invasions), len(res.Lures), len(res.Showcases))
	return res
}
