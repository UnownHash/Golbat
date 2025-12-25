package stats_collector

import (
	"gopkg.in/guregu/null.v4"

	"golbat/geo"
)

var _ StatsCollector = (*noopCollector)(nil)

type noopCollector struct {
}

func (col *noopCollector) IncRawRequests(string, string)                         {}
func (col *noopCollector) IncDecodeMethods(string, string, string)               {}
func (col *noopCollector) IncDecodeFortDetails(string, string)                   {}
func (col *noopCollector) IncDecodeGetMapForts(string, string)                   {}
func (col *noopCollector) IncDecodeGetGymInfo(string, string)                    {}
func (col *noopCollector) IncDecodeEncounter(string, string)                     {}
func (col *noopCollector) IncDecodeDiskEncounter(string, string)                 {}
func (col *noopCollector) IncDecodeQuest(string, string)                         {}
func (col *noopCollector) IncDecodeSocialActionWithRequest(string, string)       {}
func (col *noopCollector) IncDecodeGetFriendDetails(string, string)              {}
func (col *noopCollector) IncDecodeSearchPlayer(string, string)                  {}
func (col *noopCollector) IncDecodeGMO(string, string)                           {}
func (col *noopCollector) AddDecodeGMOType(string, float64)                      {}
func (col *noopCollector) IncDecodeStartIncident(string, string)                 {}
func (col *noopCollector) IncDecodeOpenInvasion(string, string)                  {}
func (col *noopCollector) AddPokemonStatsResetCount(string, float64)             {}
func (col *noopCollector) IncPokemonCountNew(string)                             {}
func (col *noopCollector) IncPokemonCountIv(string)                              {}
func (col *noopCollector) IncPokemonCountShiny(string, string)                   {}
func (col *noopCollector) IncPokemonCountNonShiny(string, string)                {}
func (col *noopCollector) IncPokemonCountShundo()                                {}
func (col *noopCollector) IncPokemonCountSnundo()                                {}
func (col *noopCollector) IncPokemonCountHundo(string)                           {}
func (col *noopCollector) IncPokemonCountNundo(string)                           {}
func (col *noopCollector) UpdateVerifiedTtl(geo.AreaName, null.String, null.Int) {}
func (col *noopCollector) UpdateRaidCount([]geo.AreaName, int64)                 {}
func (col *noopCollector) UpdateFortCount([]geo.AreaName, string, string)        {}
func (col *noopCollector) UpdateIncidentCount([]geo.AreaName)                    {}
func (col *noopCollector) IncDuplicateEncounters(bool)                           {}
func (col *noopCollector) IncDbQuery(string, error)                              {}
func (col *noopCollector) SetGyms(int8, bool, float64)                           {}
func (col *noopCollector) SetRaids(int64, float64)                               {}
func (col *noopCollector) SetIncidents(int8, bool, float64)                      {}
func (col *noopCollector) SetLures(int32, float64)                               {}
func (col *noopCollector) SetQuests(float64, float64)                            {}
func (col *noopCollector) IncPokemons(bool, null.String)                         {}
func (col *noopCollector) DecPokemons(bool, null.String)                         {}
func (col *noopCollector) UpdateMaxBattleCount([]geo.AreaName, int64)            {}
func (col *noopCollector) IncFortChange(string)                                  {}

func NewNoopStatsCollector() StatsCollector {
	return &noopCollector{}
}
