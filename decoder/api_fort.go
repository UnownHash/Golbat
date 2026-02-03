package decoder

import (
	"context"
	"slices"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
	"golbat/geo"
)

type ApiFortScan struct {
	Min        geo.Location       `json:"min"`
	Max        geo.Location       `json:"max"`
	Limit      int                `json:"limit"`
	DnfFilters []ApiFortDnfFilter `json:"filters"`
}

type ApiFortDnfFilter struct {
	PowerUpLevel     *ApiFortDnfMinMax8 `json:"power_up_level"`
	IsArScanEligible bool               `json:"is_ar_scan_eligible"`

	AvailableSlots *ApiFortDnfMinMax8 `json:"available_slots"`
	TeamId         []*int8            `json:"team_id"`
	InBattle       bool               `json:"in_battle"`
	RaidLevel      []*int8            `json:"raid_level"`
	RaidPokemon    []ApiDnfId         `json:"raid_pokemon_id"`

	LureId []*int16 `json:"lure_id"`

	ArQuestRewardType    []*int16            `json:"ar_quest_reward_type"`
	ArQuestRewardAmount  *ApiFortDnfMinMax16 `json:"ar_quest_reward_amount"`
	ArQuestRewardItemId  []*int16            `json:"ar_quest_reward_item_id"`
	ArQuestRewardPokemon []ApiDnfId          `json:"ar_quest_reward_pokemon"`
	ArQuestType          []*int16            `json:"ar_quest_type"`
	ArQuestTarget        []*int16            `json:"ar_quest_target"`
	ArQuestTemplate      []string            `json:"ar_quest_template"`

	NoArQuestRewardType    []*int16            `json:"noar_quest_reward_type"`
	NoArQuestRewardAmount  *ApiFortDnfMinMax16 `json:"noar_quest_reward_amount"`
	NoArQuestRewardItemId  []*int16            `json:"noar_quest_reward_item_id"`
	NoArQuestRewardPokemon []ApiDnfId          `json:"noar_quest_reward_pokemon"`
	NoArQuestType          []*int16            `json:"noar_quest_type"`
	NoArQuestTarget        []*int16            `json:"noar_quest_target"`
	NoArQuestTemplate      []string            `json:"noar_quest_template"`

	IncidentDisplayType    []*int8             `json:"incident_display_type"`
	IncidentStyle          []*int8             `json:"incident_style"`
	IncidentCharacter      []*int16            `json:"incident_character"`
	IncidentSlot1          []ApiDnfId          `json:"incident_slot_1"`
	IncidentSlot2          []ApiDnfId          `json:"incident_slot_2"`
	IncidentSlot3          []ApiDnfId          `json:"incident_slot_3"`
	ContestPokemon         []ApiDnfId          `json:"contest_pokemon"`
	ContestPokemonType1    []*int8             `json:"contest_pokemon_type_1"`
	ContestPokemonType2    []*int8             `json:"contest_pokemon_type_2"`
	ContestRankingStandard []*int8             `json:"contest_ranking_standard"`
	ContestTotalEntries    *ApiFortDnfMinMax16 `json:"contest_total_entries"`
}

type ApiDnfId struct {
	Pokemon int16  `json:"pokemon_id"`
	Form    *int16 `json:"form"`
}

type ApiFortDnfMinMax8 struct {
	Min int8 `json:"min"`
	Max int8 `json:"max"`
}

type ApiFortDnfMinMax16 struct {
	Min int16 `json:"min"`
	Max int16 `json:"max"`
}

type ApiGymScanResult struct {
	Gyms     []*ApiGymResult `json:"gyms"`
	Examined int             `json:"examined"`
	Skipped  int             `json:"skipped"`
	Total    int             `json:"total"`
}

type ApiPokestopScanResult struct {
	Pokestops []*ApiPokestopResult `json:"pokestops"`
	Examined  int                  `json:"examined"`
	Skipped   int                  `json:"skipped"`
	Total     int                  `json:"total"`
}

func isFortDnfMatch(fortType FortType, fortLookup *FortLookup, filter *ApiFortDnfFilter) bool {
	if fortType != fortLookup.FortType {
		return false
	}
	if filter.PowerUpLevel != nil && (fortLookup.PowerUpLevel < filter.PowerUpLevel.Min || fortLookup.PowerUpLevel > filter.PowerUpLevel.Max) {
		return false
	}
	if filter.IsArScanEligible && !fortLookup.IsArScanEligible {
		return false
	}

	if fortLookup.FortType == GYM {
		if filter.AvailableSlots != nil && (fortLookup.AvailableSlots < filter.AvailableSlots.Min || fortLookup.AvailableSlots > filter.AvailableSlots.Max) {
			return false
		}
		if filter.TeamId != nil && !slices.Contains(filter.TeamId, &fortLookup.TeamId) {
			return false
		}
		if filter.InBattle && !fortLookup.InBattle {
			return false
		}
		if filter.RaidLevel != nil && !slices.Contains(filter.RaidLevel, &fortLookup.RaidLevel) {
			return false
		}
		if filter.RaidPokemon != nil {
			raidPokemonMatch := false
			for _, raidPokemon := range filter.RaidPokemon {
				if raidPokemon.Pokemon == fortLookup.RaidPokemonId && (raidPokemon.Form == nil || *raidPokemon.Form == fortLookup.RaidPokemonForm) {
					raidPokemonMatch = true
					break
				}
			}
			if !raidPokemonMatch {
				return false
			}
		}
	} else if fortLookup.FortType == POKESTOP {
		if filter.LureId != nil && !slices.Contains(filter.LureId, &fortLookup.LureId) {
			return false
		}
		// AR Quest Filters
		if filter.ArQuestRewardType != nil && !slices.Contains(filter.ArQuestRewardType, &fortLookup.QuestArRewardType) {
			return false
		}
		if filter.ArQuestRewardAmount != nil && (fortLookup.QuestArRewardAmount < filter.ArQuestRewardAmount.Min || fortLookup.QuestArRewardAmount > filter.ArQuestRewardAmount.Max) {
			return false
		}
		if filter.ArQuestRewardItemId != nil && !slices.Contains(filter.ArQuestRewardItemId, &fortLookup.QuestArRewardItemId) {
			return false
		}
		if filter.ArQuestType != nil && !slices.Contains(filter.ArQuestType, &fortLookup.QuestArType) {
			return false
		}
		if filter.ArQuestTarget != nil && !slices.Contains(filter.ArQuestTarget, &fortLookup.QuestArTarget) {
			return false
		}
		if filter.ArQuestTemplate != nil && !slices.Contains(filter.ArQuestTemplate, fortLookup.QuestArTemplate) {
			return false
		}
		if filter.ArQuestRewardPokemon != nil {
			match := false
			for _, pkm := range filter.ArQuestRewardPokemon {
				if pkm.Pokemon == fortLookup.QuestArRewardPokemonId && (pkm.Form == nil || *pkm.Form == fortLookup.QuestArRewardPokemonForm) {
					match = true
					break
				}
			}
			if !match {
				return false
			}
		}

		// No-AR Quest Filters
		if filter.NoArQuestRewardType != nil && !slices.Contains(filter.NoArQuestRewardType, &fortLookup.QuestNoArRewardType) {
			return false
		}
		if filter.NoArQuestRewardAmount != nil && (fortLookup.QuestNoArRewardAmount < filter.NoArQuestRewardAmount.Min || fortLookup.QuestNoArRewardAmount > filter.NoArQuestRewardAmount.Max) {
			return false
		}
		if filter.NoArQuestRewardItemId != nil && !slices.Contains(filter.NoArQuestRewardItemId, &fortLookup.QuestNoArRewardItemId) {
			return false
		}
		if filter.NoArQuestType != nil && !slices.Contains(filter.NoArQuestType, &fortLookup.QuestNoArType) {
			return false
		}
		if filter.NoArQuestTarget != nil && !slices.Contains(filter.NoArQuestTarget, &fortLookup.QuestNoArTarget) {
			return false
		}
		if filter.NoArQuestTemplate != nil && !slices.Contains(filter.NoArQuestTemplate, fortLookup.QuestNoArTemplate) {
			return false
		}
		if filter.NoArQuestRewardPokemon != nil {
			match := false
			for _, pkm := range filter.NoArQuestRewardPokemon {
				if pkm.Pokemon == fortLookup.QuestNoArRewardPokemonId && (pkm.Form == nil || *pkm.Form == fortLookup.QuestNoArRewardPokemonForm) {
					match = true
					break
				}
			}
			if !match {
				return false
			}
		}

		// Contest Filters
		if filter.ContestPokemonType1 != nil && !slices.Contains(filter.ContestPokemonType1, &fortLookup.ContestPokemonType1) {
			return false
		}
		if filter.ContestPokemonType2 != nil && !slices.Contains(filter.ContestPokemonType2, &fortLookup.ContestPokemonType2) {
			return false
		}
		if filter.ContestRankingStandard != nil && !slices.Contains(filter.ContestRankingStandard, &fortLookup.ContestRankingStandard) {
			return false
		}
		if filter.ContestTotalEntries != nil && (fortLookup.ContestTotalEntries < filter.ContestTotalEntries.Min || fortLookup.ContestTotalEntries > filter.ContestTotalEntries.Max) {
			return false
		}
		if filter.ContestPokemon != nil {
			match := false
			for _, pkm := range filter.ContestPokemon {
				if pkm.Pokemon == fortLookup.ContestPokemonId && (pkm.Form == nil || *pkm.Form == fortLookup.ContestPokemonForm) {
					match = true
					break
				}
			}
			if !match {
				return false
			}
		}

		// Incident Filters
		if len(filter.IncidentDisplayType) > 0 || len(filter.IncidentStyle) > 0 || len(filter.IncidentCharacter) > 0 || len(filter.IncidentSlot1) > 0 || len(filter.IncidentSlot2) > 0 || len(filter.IncidentSlot3) > 0 {
			incidentMatch := false
			for _, incident := range fortLookup.Incidents {
				incidentFilterMatch := true
				if filter.IncidentDisplayType != nil && !slices.Contains(filter.IncidentDisplayType, &incident.DisplayType) {
					incidentFilterMatch = false
				}
				if incidentFilterMatch && filter.IncidentStyle != nil && !slices.Contains(filter.IncidentStyle, &incident.Style) {
					incidentFilterMatch = false
				}
				if incidentFilterMatch && filter.IncidentCharacter != nil && !slices.Contains(filter.IncidentCharacter, &incident.Character) {
					incidentFilterMatch = false
				}
				if incidentFilterMatch && filter.IncidentSlot1 != nil {
					slotMatch := false
					for _, pkm := range filter.IncidentSlot1 {
						if pkm.Pokemon == incident.Slot1PokemonId && (pkm.Form == nil || *pkm.Form == incident.Slot1PokemonForm) {
							slotMatch = true
							break
						}
					}
					if !slotMatch {
						incidentFilterMatch = false
					}
				}
				if incidentFilterMatch && filter.IncidentSlot2 != nil {
					slotMatch := false
					for _, pkm := range filter.IncidentSlot2 {
						if pkm.Pokemon == incident.Slot2PokemonId && (pkm.Form == nil || *pkm.Form == incident.Slot2PokemonForm) {
							slotMatch = true
							break
						}
					}
					if !slotMatch {
						incidentFilterMatch = false
					}
				}
				if incidentFilterMatch && filter.IncidentSlot3 != nil {
					slotMatch := false
					for _, pkm := range filter.IncidentSlot3 {
						if pkm.Pokemon == incident.Slot3PokemonId && (pkm.Form == nil || *pkm.Form == incident.Slot3PokemonForm) {
							slotMatch = true
							break
						}
					}
					if !slotMatch {
						incidentFilterMatch = false
					}
				}
				if incidentFilterMatch {
					incidentMatch = true
					break
				}
			}
			if !incidentMatch {
				return false
			}
		}
	}
	return true
}

func internalGetForts(fortType FortType, retrieveParameters ApiFortScan) ([]string, int, int, int) {
	start := time.Now()

	minLocation := retrieveParameters.Min
	maxLocation := retrieveParameters.Max

	maxForts := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.Limit > 0 && retrieveParameters.Limit < maxForts {
		maxForts = retrieveParameters.Limit
	}

	fortsExamined := 0
	fortsSkipped := 0

	fortTreeMutex.RLock()
	fortTreeCopy := fortTree.Copy()
	fortTreeMutex.RUnlock()

	lockedTime := time.Since(start)

	var returnKeys []string

	fortTreeCopy.Search([2]float64{minLocation.Longitude, minLocation.Latitude}, [2]float64{maxLocation.Longitude, maxLocation.Latitude},
		func(min, max [2]float64, fortId string) bool {
			fortsExamined++

			fortLookup, found := fortLookupCache.Load(fortId)
			if !found {
				fortsSkipped++
				return true
			}

			matched := false
			if len(retrieveParameters.DnfFilters) == 0 {
				matched = fortType == fortLookup.FortType
			} else {
				for i := 0; i < len(retrieveParameters.DnfFilters); i++ {
					if isFortDnfMatch(fortType, &fortLookup, &retrieveParameters.DnfFilters[i]) {
						matched = true
						break
					}
				}
			}

			if matched {
				returnKeys = append(returnKeys, fortId)
				if len(returnKeys) >= maxForts {
					log.Infof("GetFortsInArea - result would exceed maximum size (%d), stopping scan", maxForts)
					return false
				}
			}

			return true
		})

	log.Infof("GetFortsInArea - scan time %s (locked time %s), %d scanned, %d skipped, %d returned, tree size %d",
		time.Since(start), lockedTime, fortsExamined, fortsSkipped, len(returnKeys), fortTreeCopy.Len())

	return returnKeys, fortsExamined, fortsSkipped, fortTreeCopy.Len()
}

func GymScanEndpoint(retrieveParameters ApiFortScan, dbDetails db.DbDetails) *ApiGymScanResult {
	returnKeys, examined, skipped, total := internalGetForts(GYM, retrieveParameters)
	results := make([]*ApiGymResult, 0, len(returnKeys))
	start := time.Now()

	for _, key := range returnKeys {
		gym, unlock, err := GetGymRecordReadOnly(context.Background(), dbDetails, key)
		if err == nil && gym != nil {
			// Make a copy to avoid holding locks
			gymCopy := buildGymResult(gym)
			results = append(results, &gymCopy)
		}
		if unlock != nil {
			unlock()
		}
	}
	log.Infof("GymScan - result buffer time %s, %d added", time.Since(start), len(results))

	return &ApiGymScanResult{
		Gyms:     results,
		Examined: examined,
		Skipped:  skipped,
		Total:    total,
	}
}

func PokestopScanEndpoint(retrieveParameters ApiFortScan, dbDetails db.DbDetails) *ApiPokestopScanResult {
	returnKeys, examined, skipped, total := internalGetForts(POKESTOP, retrieveParameters)
	results := make([]*ApiPokestopResult, 0, len(returnKeys))
	start := time.Now()

	for _, key := range returnKeys {
		pokestop, unlock, err := getPokestopRecordReadOnly(context.Background(), dbDetails, key)
		if err == nil && pokestop != nil {
			// Make a copy to avoid holding locks
			pokestopCopy := buildPokestopResult(pokestop)
			results = append(results, &pokestopCopy)
		}
		if unlock != nil {
			unlock()
		}
	}
	log.Infof("PokestopScan - result buffer time %s, %d added", time.Since(start), len(results))

	return &ApiPokestopScanResult{
		Pokestops: results,
		Examined:  examined,
		Skipped:   skipped,
		Total:     total,
	}
}
