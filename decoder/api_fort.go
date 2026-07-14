package decoder

import (
	"context"
	"slices"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/db"
)

type ApiFortScan struct {
	Min        ApiLatLon          `json:"min" doc:"SW (minimum lat/lon) corner of the bounding box."`
	Max        ApiLatLon          `json:"max" doc:"NE (maximum lat/lon) corner of the bounding box."`
	Limit      int                `json:"limit" required:"false" doc:"Max results to return; 0 uses the server default."`
	DnfFilters []ApiFortDnfFilter `json:"filters" required:"false" doc:"OR'd filter clauses; a fort matches if it satisfies any one clause. List conditions apply only when present: omit or send null for no constraint — an explicitly empty list matches nothing."`
}

type ApiFortDnfFilter struct {
	PowerUpLevel     *ApiFortDnfMinMax `json:"power_up_level" required:"false" doc:"Inclusive power-up level range; null means no power-up level constraint."`
	IsArScanEligible *bool             `json:"is_ar_scan_eligible" required:"false" doc:"When true, only match forts that are AR scan eligible; null means no AR eligibility constraint."`

	// Gym
	AvailableSlots *ApiFortDnfMinMax `json:"available_slots" required:"false" doc:"Gym only: inclusive range of open defender slots; null means no slot constraint."`
	TeamId         []int8            `json:"team_id" required:"false" doc:"Gym only: allowed controlling team ids; omitted or null means no team constraint."`
	RaidLevel      []int8            `json:"raid_level" required:"false" doc:"Gym only: allowed active raid levels; omitted or null means no raid level constraint. Only matches gyms with an active raid."`
	RaidPokemon    []ApiDnfId        `json:"raid_pokemon_id" required:"false" doc:"Gym only: allowed active raid boss pokemon/form pairs; omitted or null means no raid pokemon constraint. Only matches gyms with an active raid."`

	// Pokestop - unified quest (matches AR or no-AR)
	LureId             []int16           `json:"lure_id" required:"false" doc:"Pokestop only: allowed active lure module ids; omitted or null means no lure constraint."`
	QuestRewardType    []int16           `json:"quest_reward_type" required:"false" doc:"Pokestop only: allowed quest reward types; matched against either the AR or no-AR quest. Omitted or null means no reward type constraint."`
	QuestRewardAmount  *ApiFortDnfMinMax `json:"quest_reward_amount" required:"false" doc:"Pokestop only: inclusive quest reward amount range; matched against either the AR or no-AR quest. Null means no reward amount constraint."`
	QuestRewardItemId  []int16           `json:"quest_reward_item_id" required:"false" doc:"Pokestop only: allowed quest reward item ids; matched against either the AR or no-AR quest. Omitted or null means no reward item constraint."`
	QuestRewardPokemon []ApiDnfId        `json:"quest_reward_pokemon" required:"false" doc:"Pokestop only: allowed quest reward pokemon/form pairs; matched against either the AR or no-AR quest. Omitted or null means no reward pokemon constraint."`

	// Pokestop - incident
	IncidentDisplayType []int8     `json:"incident_display_type" required:"false" doc:"Pokestop only: allowed incident display types; omitted or null means no incident display type constraint."`
	IncidentStyle       []int8     `json:"incident_style" required:"false" doc:"Pokestop only: allowed incident styles; omitted or null means no incident style constraint."`
	IncidentCharacter   []int16    `json:"incident_character" required:"false" doc:"Pokestop only: allowed incident character ids; omitted or null means no incident character constraint."`
	IncidentPokemon     []ApiDnfId `json:"incident_pokemon" required:"false" doc:"Pokestop only: allowed incident pokemon/form pairs; omitted or null means no incident pokemon constraint."`

	// Pokestop - contest
	ContestPokemon      []ApiDnfId        `json:"contest_pokemon" required:"false" doc:"Pokestop only: allowed contest focus pokemon/form pairs; omitted or null means no contest pokemon constraint."`
	ContestPokemonType  []int8            `json:"contest_pokemon_type" required:"false" doc:"Pokestop only: allowed contest pokemon types; omitted or null means no contest type constraint."`
	ContestTotalEntries *ApiFortDnfMinMax `json:"contest_total_entries" required:"false" doc:"Pokestop only: inclusive range for the contest's total number of entries; null means no contest entries constraint."`

	// Station
	BattleLevel   []int8     `json:"battle_level" required:"false" doc:"Station only: allowed active max battle levels; omitted or null means no battle level constraint. Only matches stations with an active battle."`
	BattlePokemon []ApiDnfId `json:"battle_pokemon" required:"false" doc:"Station only: allowed active max battle pokemon/form pairs; omitted or null means no battle pokemon constraint. Only matches stations with an active battle."`
}

type ApiDnfId struct {
	Pokemon int16  `json:"pokemon_id" doc:"Pokedex id to match. Required within an entry — a form without an id can never match."`
	Form    *int16 `json:"form" required:"false" doc:"Form id to match; null matches any form of the given id."`
}

// ApiFortDnfMinMax is an inclusive integer range used by the fort filter clauses
// (int16 internally — wide enough for all fort range fields).
type ApiFortDnfMinMax struct {
	Min int16 `json:"min" required:"false" doc:"Minimum value (inclusive). An omitted bound defaults to 0."`
	Max int16 `json:"max" required:"false" doc:"Maximum value (inclusive). An omitted bound defaults to 0, so a range with only min can never match — send both bounds."`
}

type ApiGymScanResult struct {
	Gyms     []*ApiGymResult `json:"gyms" doc:"Matching gyms within the bounding box."`
	Examined int             `json:"examined" doc:"Number of forts examined during the spatial scan."`
	Skipped  int             `json:"skipped" doc:"Number of forts skipped because they were not found in the lookup cache."`
	Total    int             `json:"total" doc:"Total number of forts in the spatial index at scan time."`
}

type ApiPokestopScanResult struct {
	Pokestops []*ApiPokestopResult `json:"pokestops" doc:"Matching pokestops within the bounding box."`
	Examined  int                  `json:"examined" doc:"Number of forts examined during the spatial scan."`
	Skipped   int                  `json:"skipped" doc:"Number of forts skipped because they were not found in the lookup cache."`
	Total     int                  `json:"total" doc:"Total number of forts in the spatial index at scan time."`
}

type ApiStationScanResult struct {
	Stations []*ApiStationResult `json:"stations" doc:"Matching stations within the bounding box."`
	Examined int                 `json:"examined" doc:"Number of forts examined during the spatial scan."`
	Skipped  int                 `json:"skipped" doc:"Number of forts skipped because they were not found in the lookup cache."`
	Total    int                 `json:"total" doc:"Total number of forts in the spatial index at scan time."`
}

type ApiFortCombinedScanResult struct {
	Gyms      []*ApiGymResult      `json:"gyms" doc:"Matching gyms within the bounding box."`
	Pokestops []*ApiPokestopResult `json:"pokestops" doc:"Matching pokestops within the bounding box."`
	Stations  []*ApiStationResult  `json:"stations" doc:"Matching stations within the bounding box."`
	Examined  int                  `json:"examined" doc:"Number of forts examined during the spatial scan."`
	Skipped   int                  `json:"skipped" doc:"Number of forts skipped because they were not found in the lookup cache."`
	Total     int                  `json:"total" doc:"Total number of forts in the spatial index at scan time."`
}

// matchDnfIdPair checks if any ApiDnfId in the filter matches the given pokemon/form pair
func matchDnfIdPair(filter []ApiDnfId, pokemonId int16, form int16) bool {
	for _, f := range filter {
		if f.Pokemon == pokemonId && (f.Form == nil || *f.Form == form) {
			return true
		}
	}
	return false
}

func isFortDnfMatch(fortType FortType, fortLookup *FortLookup, filter *ApiFortDnfFilter, now int64) bool {
	// fortType 0 means "match any type" (used by combined scan)
	if fortType != 0 && fortType != fortLookup.FortType {
		return false
	}
	if filter.PowerUpLevel != nil && (int16(fortLookup.PowerUpLevel) < filter.PowerUpLevel.Min || int16(fortLookup.PowerUpLevel) > filter.PowerUpLevel.Max) {
		return false
	}
	if filter.IsArScanEligible != nil && !fortLookup.IsArScanEligible {
		return false
	}

	switch fortLookup.FortType {
	case GYM:
		if filter.AvailableSlots != nil && (int16(fortLookup.AvailableSlots) < filter.AvailableSlots.Min || int16(fortLookup.AvailableSlots) > filter.AvailableSlots.Max) {
			return false
		}
		if filter.TeamId != nil && !slices.Contains(filter.TeamId, fortLookup.TeamId) {
			return false
		}
		if filter.RaidLevel != nil || filter.RaidPokemon != nil {
			// Check if raid has expired
			raidActive := fortLookup.RaidBattleTimestamp > now || fortLookup.RaidEndTimestamp > now
			if !raidActive {
				return false
			}
			if filter.RaidLevel != nil && !slices.Contains(filter.RaidLevel, fortLookup.RaidLevel) {
				return false
			}
			if filter.RaidPokemon != nil && !matchDnfIdPair(filter.RaidPokemon, fortLookup.RaidPokemonId, fortLookup.RaidPokemonForm) {
				return false
			}
		}
	case POKESTOP:
		if filter.LureId != nil &&
			(fortLookup.LureExpireTimestamp <= now || !slices.Contains(filter.LureId, fortLookup.LureId)) {
			return false
		}

		// Unified quest filters - match if AR or no-AR value matches
		if filter.QuestRewardType != nil {
			if !slices.Contains(filter.QuestRewardType, fortLookup.QuestArRewardType) &&
				!slices.Contains(filter.QuestRewardType, fortLookup.QuestNoArRewardType) {
				return false
			}
		}
		if filter.QuestRewardAmount != nil {
			arMatch := fortLookup.QuestArRewardAmount >= filter.QuestRewardAmount.Min && fortLookup.QuestArRewardAmount <= filter.QuestRewardAmount.Max
			noArMatch := fortLookup.QuestNoArRewardAmount >= filter.QuestRewardAmount.Min && fortLookup.QuestNoArRewardAmount <= filter.QuestRewardAmount.Max
			if !arMatch && !noArMatch {
				return false
			}
		}
		if filter.QuestRewardItemId != nil {
			if !slices.Contains(filter.QuestRewardItemId, fortLookup.QuestArRewardItemId) &&
				!slices.Contains(filter.QuestRewardItemId, fortLookup.QuestNoArRewardItemId) {
				return false
			}
		}
		if filter.QuestRewardPokemon != nil {
			arMatch := matchDnfIdPair(filter.QuestRewardPokemon, fortLookup.QuestArRewardPokemonId, fortLookup.QuestArRewardPokemonForm)
			noArMatch := matchDnfIdPair(filter.QuestRewardPokemon, fortLookup.QuestNoArRewardPokemonId, fortLookup.QuestNoArRewardPokemonForm)
			if !arMatch && !noArMatch {
				return false
			}
		}

		// Contest filters
		if filter.ContestPokemonType != nil &&
			(fortLookup.ShowcaseExpiry <= now || !slices.Contains(filter.ContestPokemonType, fortLookup.ContestPokemonType)) {
			return false
		}
		if filter.ContestTotalEntries != nil &&
			(fortLookup.ShowcaseExpiry <= now ||
				fortLookup.ContestTotalEntries < filter.ContestTotalEntries.Min || fortLookup.ContestTotalEntries > filter.ContestTotalEntries.Max) {
			return false
		}
		if filter.ContestPokemon != nil &&
			(fortLookup.ShowcaseExpiry <= now || !matchDnfIdPair(filter.ContestPokemon, fortLookup.ContestPokemonId, fortLookup.ContestPokemonForm)) {
			return false
		}

		// Incident filters - flat field checks
		if filter.IncidentDisplayType != nil && !slices.Contains(filter.IncidentDisplayType, fortLookup.IncidentDisplayType) {
			return false
		}
		if filter.IncidentStyle != nil && !slices.Contains(filter.IncidentStyle, fortLookup.IncidentStyle) {
			return false
		}
		if filter.IncidentCharacter != nil && !slices.Contains(filter.IncidentCharacter, fortLookup.IncidentCharacter) {
			return false
		}
		if filter.IncidentPokemon != nil && !matchDnfIdPair(filter.IncidentPokemon, fortLookup.IncidentPokemonId, fortLookup.IncidentPokemonForm) {
			return false
		}
	case STATION:
		if filter.BattleLevel != nil || filter.BattlePokemon != nil {
			if len(fortLookup.StationBattles) == 0 {
				if fortLookup.BattleEndTimestamp <= now {
					return false
				}
				if filter.BattleLevel != nil && !slices.Contains(filter.BattleLevel, fortLookup.BattleLevel) {
					return false
				}
				if filter.BattlePokemon != nil && !matchDnfIdPair(filter.BattlePokemon, fortLookup.BattlePokemonId, fortLookup.BattlePokemonForm) {
					return false
				}
				return true
			}

			matchedBattle := false
			for _, battle := range fortLookup.StationBattles {
				if battle.BattleEndTimestamp <= now {
					continue
				}
				if filter.BattleLevel != nil && !slices.Contains(filter.BattleLevel, battle.BattleLevel) {
					continue
				}
				if filter.BattlePokemon != nil && !matchDnfIdPair(filter.BattlePokemon, battle.BattlePokemonId, battle.BattlePokemonForm) {
					continue
				}
				matchedBattle = true
				break
			}
			if !matchedBattle {
				return false
			}
		}
	}
	return true
}

func internalGetForts(fortType FortType, retrieveParameters ApiFortScan) ([]string, int, int, int) {
	start := time.Now()

	minLocation := retrieveParameters.Min.Location()
	maxLocation := retrieveParameters.Max.Location()

	maxForts := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.Limit > 0 && retrieveParameters.Limit < maxForts {
		maxForts = retrieveParameters.Limit
	}

	fortsExamined := 0
	fortsSkipped := 0
	now := time.Now().Unix()

	fortTreeCopy := getFortTreeSnapshot()

	lockedTime := time.Since(start)

	var returnKeys []string
	// Dedupe: the shared snapshot can briefly hold duplicate points for one
	// id (eviction delete still queued while a save re-added the point).
	seen := make(map[string]struct{})

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
					if isFortDnfMatch(fortType, &fortLookup, &retrieveParameters.DnfFilters[i], now) {
						matched = true
						break
					}
				}
			}

			if matched {
				if _, dup := seen[fortId]; dup {
					return true
				}
				seen[fortId] = struct{}{}
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
		gym, unlock, err := GetGymRecordReadOnly(context.Background(), dbDetails, key, "API.GetScanGym")
		if err == nil && gym != nil {
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
		pokestop, unlock, err := getPokestopRecordReadOnly(context.Background(), dbDetails, key, "API.GetScanpokemon")
		if err == nil && pokestop != nil {
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

func StationScanEndpoint(retrieveParameters ApiFortScan, dbDetails db.DbDetails) *ApiStationScanResult {
	returnKeys, examined, skipped, total := internalGetForts(STATION, retrieveParameters)
	results := make([]*ApiStationResult, 0, len(returnKeys))
	start := time.Now()

	for _, key := range returnKeys {
		station, unlock, err := GetStationRecordReadOnly(context.Background(), dbDetails, key, "API.GetScanStation")
		if err == nil && station != nil {
			stationCopy := BuildStationResult(station)
			results = append(results, &stationCopy)
		}
		if unlock != nil {
			unlock()
		}
	}
	log.Infof("StationScan - result buffer time %s, %d added", time.Since(start), len(results))

	return &ApiStationScanResult{
		Stations: results,
		Examined: examined,
		Skipped:  skipped,
		Total:    total,
	}
}

func FortCombinedScanEndpoint(retrieveParameters ApiFortScan, dbDetails db.DbDetails) *ApiFortCombinedScanResult {
	gymKeys, pokestopKeys, stationKeys, examined, skipped, total := internalGetFortsCombined(retrieveParameters)
	start := time.Now()

	gyms := make([]*ApiGymResult, 0, len(gymKeys))
	for _, key := range gymKeys {
		gym, unlock, err := GetGymRecordReadOnly(context.Background(), dbDetails, key, "API.GetScanGymPokemon")
		if err == nil && gym != nil {
			gymCopy := buildGymResult(gym)
			gyms = append(gyms, &gymCopy)
		}
		if unlock != nil {
			unlock()
		}
	}

	pokestops := make([]*ApiPokestopResult, 0, len(pokestopKeys))
	for _, key := range pokestopKeys {
		pokestop, unlock, err := getPokestopRecordReadOnly(context.Background(), dbDetails, key, "API.GetScanpokemonPokemon")
		if err == nil && pokestop != nil {
			pokestopCopy := buildPokestopResult(pokestop)
			pokestops = append(pokestops, &pokestopCopy)
		}
		if unlock != nil {
			unlock()
		}
	}

	stations := make([]*ApiStationResult, 0, len(stationKeys))
	for _, key := range stationKeys {
		station, unlock, err := GetStationRecordReadOnly(context.Background(), dbDetails, key, "API.GetScanStationPokemon")
		if err == nil && station != nil {
			stationCopy := BuildStationResult(station)
			stations = append(stations, &stationCopy)
		}
		if unlock != nil {
			unlock()
		}
	}

	log.Infof("FortCombinedScan - result buffer time %s, %d+%d+%d added",
		time.Since(start), len(gyms), len(pokestops), len(stations))

	return &ApiFortCombinedScanResult{
		Gyms:      gyms,
		Pokestops: pokestops,
		Stations:  stations,
		Examined:  examined,
		Skipped:   skipped,
		Total:     total,
	}
}

func internalGetFortsCombined(retrieveParameters ApiFortScan) (gymKeys, pokestopKeys, stationKeys []string, examined, skipped, total int) {
	start := time.Now()

	minLocation := retrieveParameters.Min.Location()
	maxLocation := retrieveParameters.Max.Location()

	maxForts := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.Limit > 0 && retrieveParameters.Limit < maxForts {
		maxForts = retrieveParameters.Limit
	}

	now := time.Now().Unix()
	totalMatched := 0
	// Dedupe: see internalGetForts.
	seenCombined := make(map[string]struct{})

	fortTreeCopy := getFortTreeSnapshot()

	lockedTime := time.Since(start)

	fortTreeCopy.Search([2]float64{minLocation.Longitude, minLocation.Latitude}, [2]float64{maxLocation.Longitude, maxLocation.Latitude},
		func(min, max [2]float64, fortId string) bool {
			examined++

			fortLookup, found := fortLookupCache.Load(fortId)
			if !found {
				skipped++
				return true
			}

			matched := false
			if len(retrieveParameters.DnfFilters) == 0 {
				matched = true
			} else {
				for i := range retrieveParameters.DnfFilters {
					if isFortDnfMatch(0, &fortLookup, &retrieveParameters.DnfFilters[i], now) {
						matched = true
						break
					}
				}
			}

			if matched {
				if _, dup := seenCombined[fortId]; dup {
					return true
				}
				seenCombined[fortId] = struct{}{}
				switch fortLookup.FortType {
				case GYM:
					gymKeys = append(gymKeys, fortId)
				case POKESTOP:
					pokestopKeys = append(pokestopKeys, fortId)
				case STATION:
					stationKeys = append(stationKeys, fortId)
				}
				totalMatched++
				if totalMatched >= maxForts {
					log.Infof("GetFortsInArea - result would exceed maximum size (%d), stopping scan", maxForts)
					return false
				}
			}

			return true
		})

	log.Infof("GetFortsInArea (combined) - scan time %s (locked time %s), %d scanned, %d skipped, %d+%d+%d returned, tree size %d",
		time.Since(start), lockedTime, examined, skipped, len(gymKeys), len(pokestopKeys), len(stationKeys), fortTreeCopy.Len())

	total = fortTreeCopy.Len()
	return
}
