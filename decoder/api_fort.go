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
	IsArScanEligible *bool              `json:"is_ar_scan_eligible"`

	// Gym
	AvailableSlots *ApiFortDnfMinMax8 `json:"available_slots"`
	TeamId         []int8             `json:"team_id"`
	RaidLevel      []int8             `json:"raid_level"`
	RaidPokemon    []ApiDnfId         `json:"raid_pokemon_id"`

	// Pokestop - unified quest (matches AR or no-AR)
	LureId             []int16             `json:"lure_id"`
	QuestRewardType    []int16             `json:"quest_reward_type"`
	QuestRewardAmount  *ApiFortDnfMinMax16 `json:"quest_reward_amount"`
	QuestRewardItemId  []int16             `json:"quest_reward_item_id"`
	QuestRewardPokemon []ApiDnfId          `json:"quest_reward_pokemon"`

	// Pokestop - incident
	IncidentDisplayType []int8     `json:"incident_display_type"`
	IncidentStyle       []int8     `json:"incident_style"`
	IncidentCharacter   []int16    `json:"incident_character"`
	IncidentPokemon     []ApiDnfId `json:"incident_pokemon"`

	// Pokestop - contest
	ContestPokemon      []ApiDnfId          `json:"contest_pokemon"`
	ContestPokemonType  []int8              `json:"contest_pokemon_type"`
	ContestTotalEntries *ApiFortDnfMinMax16 `json:"contest_total_entries"`

	// Station
	BattleLevel   []int8     `json:"battle_level"`
	BattlePokemon []ApiDnfId `json:"battle_pokemon"`
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

type ApiStationScanResult struct {
	Stations []*ApiStationResult `json:"stations"`
	Examined int                 `json:"examined"`
	Skipped  int                 `json:"skipped"`
	Total    int                 `json:"total"`
}

type ApiFortCombinedScanResult struct {
	Gyms      []*ApiGymResult      `json:"gyms"`
	Pokestops []*ApiPokestopResult `json:"pokestops"`
	Stations  []*ApiStationResult  `json:"stations"`
	Examined  int                  `json:"examined"`
	Skipped   int                  `json:"skipped"`
	Total     int                  `json:"total"`
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
	if filter.PowerUpLevel != nil && (fortLookup.PowerUpLevel < filter.PowerUpLevel.Min || fortLookup.PowerUpLevel > filter.PowerUpLevel.Max) {
		return false
	}
	if filter.IsArScanEligible != nil && !fortLookup.IsArScanEligible {
		return false
	}

	if fortLookup.FortType == GYM {
		if filter.AvailableSlots != nil && (fortLookup.AvailableSlots < filter.AvailableSlots.Min || fortLookup.AvailableSlots > filter.AvailableSlots.Max) {
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
	} else if fortLookup.FortType == POKESTOP {
		if filter.LureId != nil && !slices.Contains(filter.LureId, fortLookup.LureId) {
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
		if filter.ContestPokemonType != nil && !slices.Contains(filter.ContestPokemonType, fortLookup.ContestPokemonType) {
			return false
		}
		if filter.ContestTotalEntries != nil && (fortLookup.ContestTotalEntries < filter.ContestTotalEntries.Min || fortLookup.ContestTotalEntries > filter.ContestTotalEntries.Max) {
			return false
		}
		if filter.ContestPokemon != nil && !matchDnfIdPair(filter.ContestPokemon, fortLookup.ContestPokemonId, fortLookup.ContestPokemonForm) {
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
	} else if fortLookup.FortType == STATION {
		if filter.BattleLevel != nil || filter.BattlePokemon != nil {
			// Check if battle has expired
			if fortLookup.BattleEndTimestamp <= now {
				return false
			}
			if filter.BattleLevel != nil && !slices.Contains(filter.BattleLevel, fortLookup.BattleLevel) {
				return false
			}
			if filter.BattlePokemon != nil && !matchDnfIdPair(filter.BattlePokemon, fortLookup.BattlePokemonId, fortLookup.BattlePokemonForm) {
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
	now := time.Now().Unix()

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
					if isFortDnfMatch(fortType, &fortLookup, &retrieveParameters.DnfFilters[i], now) {
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
		station, unlock, err := getStationRecordReadOnly(context.Background(), dbDetails, key)
		if err == nil && station != nil {
			stationCopy := buildStationResult(station)
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
		gym, unlock, err := GetGymRecordReadOnly(context.Background(), dbDetails, key)
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
		pokestop, unlock, err := getPokestopRecordReadOnly(context.Background(), dbDetails, key)
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
		station, unlock, err := getStationRecordReadOnly(context.Background(), dbDetails, key)
		if err == nil && station != nil {
			stationCopy := buildStationResult(station)
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

	minLocation := retrieveParameters.Min
	maxLocation := retrieveParameters.Max

	maxForts := config.Config.Tuning.MaxPokemonResults
	if retrieveParameters.Limit > 0 && retrieveParameters.Limit < maxForts {
		maxForts = retrieveParameters.Limit
	}

	now := time.Now().Unix()
	totalMatched := 0

	fortTreeMutex.RLock()
	fortTreeCopy := fortTree.Copy()
	fortTreeMutex.RUnlock()

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
