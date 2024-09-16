package decoder

import (
	"golbat/config"
	"golbat/geo"
	"strings"
)

type ScanParameters struct {
	ProcessPokemon   bool
	ProcessWild      bool
	ProcessNearby    bool
	ProcessWeather   bool
	ProcessPokestops bool
	ProcessGyms      bool
	ProcessStations  bool
	ProcessCells     bool
}

func FindScanConfiguration(scanContext string, lat, lon float64) ScanParameters {
	var areas []geo.AreaName
	areaLookedUp := false

	for _, rule := range config.Config.ScanRules {
		if len(rule.AreaNames) > 0 {
			if !areaLookedUp {
				areas = MatchStatsGeofence(lat, lon)
				areaLookedUp = true
			}
			if !geo.AreaMatchWithWildcards(areas, rule.AreaNames) {
				continue
			}
		}
		if len(rule.ScanContext) > 0 {
			found := false
			for _, context := range rule.ScanContext {
				if strings.EqualFold(context, scanContext) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// We have a match

		defaultTrue := func(value *bool) bool {
			if value == nil {
				return true
			}
			return *value
		}
		return ScanParameters{
			ProcessPokemon:   defaultTrue(rule.ProcessPokemon),
			ProcessWild:      defaultTrue(rule.ProcessWilds),
			ProcessNearby:    defaultTrue(rule.ProcessNearby),
			ProcessCells:     defaultTrue(rule.ProcessCells),
			ProcessWeather:   defaultTrue(rule.ProcessWeather),
			ProcessPokestops: defaultTrue(rule.ProcessPokestops),
			ProcessGyms:      defaultTrue(rule.ProcessGyms),
			ProcessStations:  defaultTrue(rule.ProcessStations),
		}
	}

	return ScanParameters{
		ProcessPokemon:   true,
		ProcessWild:      true,
		ProcessNearby:    true,
		ProcessCells:     true,
		ProcessWeather:   true,
		ProcessGyms:      true,
		ProcessPokestops: true,
		ProcessStations:  true,
	}
}
