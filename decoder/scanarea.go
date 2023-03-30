package decoder

import (
	"golbat/config"
	"golbat/geo"
	"strings"
)

type ScanParameters struct {
	ProcessPokemon bool
	processWild    bool
	processNearby  bool
}

func FindScanArea(scanContext string, lat, lon float64) ScanParameters {
	var areas []geo.AreaName
	areaLookedUp := false

	for _, rule := range config.Config.ScanRules {
		if len(rule.AreaNames) > 0 {
			if !areaLookedUp {
				areas = geo.MatchGeofences(statsFeatureCollection, lat, lon)
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

		return ScanParameters{
			ProcessPokemon: rule.ProcessPokemon,
			processWild:    rule.ProcessWilds,
			processNearby:  rule.ProcessNearby,
		}
	}

	return ScanParameters{
		ProcessPokemon: true,
		processWild:    true,
		processNearby:  true,
	}
}
