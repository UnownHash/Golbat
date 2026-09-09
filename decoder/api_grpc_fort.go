package decoder

import (
	"encoding/json"

	"golbat/db"
	pb "golbat/grpc"
)

// gRPC adapter for the fort scan API. Requests convert to the same
// ApiFortScan / ApiFortCombinedScan the HTTP endpoints bind, the scan
// endpoints run unchanged, and the Api result structs mirror into proto.
// Stored JSON blobs pass through as text exactly as the HTTP API emits them.

func intRangeToFortMinMax(r *pb.IntRange) *ApiFortDnfMinMax {
	minV, maxV, ok := intRangeBounds(r)
	if !ok {
		return nil
	}
	return &ApiFortDnfMinMax{Min: minV, Max: maxV}
}

func dnfIdsToFort(ids []*pb.DnfId) []ApiDnfId {
	if len(ids) == 0 {
		return nil
	}
	out := make([]ApiDnfId, 0, len(ids))
	for _, id := range ids {
		if id == nil {
			continue
		}
		entry := ApiDnfId{Pokemon: clampInt16(id.GetPokemonId())}
		if id.Form != nil {
			form := clampInt16(id.GetForm())
			entry.Form = &form
		}
		out = append(out, entry)
	}
	return out
}

func contestFocusFromProto(in []*pb.ContestFocus) []ApiFortDnfContestFocus {
	if len(in) == 0 {
		return nil
	}
	out := make([]ApiFortDnfContestFocus, 0, len(in))
	for _, f := range in {
		if f == nil {
			continue
		}
		entry := ApiFortDnfContestFocus{Type: f.GetType()}
		if f.MinLevel != nil {
			level := int8(f.GetMinLevel())
			entry.MinLevel = &level
		}
		out = append(out, entry)
	}
	return out
}

func fortDnfFilterFromProto(f *pb.FortDnfFilter) ApiFortDnfFilter {
	return ApiFortDnfFilter{
		IsArScanEligible:       f.IsArScanEligible,
		AvailableSlots:         intRangeToFortMinMax(f.GetAvailableSlots()),
		TeamId:                 int32sTo[int8](f.GetTeamId()),
		RaidLevel:              int32sTo[int8](f.GetRaidLevel()),
		RaidPokemon:            dnfIdsToFort(f.GetRaidPokemonId()),
		RaidTempEvolutionId:    int32sTo[int8](f.GetRaidTempEvolutionId()),
		LureId:                 int32sTo[int16](f.GetLureId()),
		QuestRewardType:        int32sTo[int16](f.GetQuestRewardType()),
		QuestRewardAmount:      intRangeToFortMinMax(f.GetQuestRewardAmount()),
		QuestRewardItemId:      int32sTo[int16](f.GetQuestRewardItemId()),
		QuestRewardPokemon:     dnfIdsToFort(f.GetQuestRewardPokemon()),
		IncidentDisplayType:    int32sTo[int8](f.GetIncidentDisplayType()),
		IncidentCharacter:      int32sTo[int16](f.GetIncidentCharacter()),
		ContestPokemon:         dnfIdsToFort(f.GetContestPokemon()),
		ContestPokemonType:     int32sTo[int8](f.GetContestPokemonType()),
		ContestFocus:           contestFocusFromProto(f.GetContestFocus()),
		ContestRankingStandard: int32sTo[int8](f.GetContestRankingStandard()),
		BattleLevel:            int32sTo[int8](f.GetBattleLevel()),
		BattlePokemon:          dnfIdsToFort(f.GetBattlePokemon()),
		StationedGmax:          f.StationedGmax,
		StationActive:          f.StationActive,
	}
}

// fortDnfFiltersFromProto returns nil for no clauses: the JSON "omitted"
// representation, which matches every fort of the requested type.
func fortDnfFiltersFromProto(in []*pb.FortDnfFilter) []ApiFortDnfFilter {
	if len(in) == 0 {
		return nil
	}
	out := make([]ApiFortDnfFilter, 0, len(in))
	for _, f := range in {
		if f != nil {
			out = append(out, fortDnfFilterFromProto(f))
		}
	}
	return out
}

func fortScanRequestFromProto(req *pb.FortScanRequest) ApiFortScan {
	return ApiFortScan{
		Min:           latLonFromProto(req.GetMin()),
		Max:           latLonFromProto(req.GetMax()),
		Limit:         int(req.GetLimit()),
		DnfFilters:    fortDnfFiltersFromProto(req.GetFilters()),
		WithIncidents: req.GetWithIncidents(),
	}
}

// fortTypeGroupFromProto keeps the presence distinction the combined scan
// relies on: an unset group excludes the type, a present group with no
// clauses matches every fort of the type.
func fortTypeGroupFromProto(g *pb.FortTypeScanGroup) *ApiFortTypeScanGroup {
	if g == nil {
		return nil
	}
	return &ApiFortTypeScanGroup{DnfFilters: fortDnfFiltersFromProto(g.GetFilters())}
}

func fortCombinedScanRequestFromProto(req *pb.FortCombinedScanRequest) ApiFortCombinedScan {
	return ApiFortCombinedScan{
		Min:           latLonFromProto(req.GetMin()),
		Max:           latLonFromProto(req.GetMax()),
		Limit:         int(req.GetLimit()),
		WithIncidents: req.GetWithIncidents(),
		Gyms:          fortTypeGroupFromProto(req.GetGyms()),
		Pokestops:     fortTypeGroupFromProto(req.GetPokestops()),
		Stations:      fortTypeGroupFromProto(req.GetStations()),
	}
}

// rawJsonToProto passes a stored JSON document through as text; nil/empty is
// unset (JSON null on the HTTP side).
func rawJsonToProto(m *json.RawMessage) *string {
	if m == nil || len(*m) == 0 {
		return nil
	}
	s := string(*m)
	return &s
}

// jsonBytesToProto is rawJsonToProto for the named []byte passthrough types
// (ApiGymGuardingPokemonRaw, ApiGymDefendersRaw).
func jsonBytesToProto[T ~[]byte](b T) *string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	return &s
}

func gymToProto(g *ApiGymResult) *pb.Gym {
	return &pb.Gym{
		Id:                         g.Id,
		Lat:                        g.Lat,
		Lon:                        g.Lon,
		Name:                       g.Name,
		Url:                        g.Url,
		LastModifiedTimestamp:      g.LastModifiedTimestamp,
		RaidEndTimestamp:           g.RaidEndTimestamp,
		RaidSpawnTimestamp:         g.RaidSpawnTimestamp,
		RaidBattleTimestamp:        g.RaidBattleTimestamp,
		Updated:                    g.Updated,
		RaidPokemonId:              g.RaidPokemonId,
		GuardingPokemonId:          g.GuardingPokemonId,
		GuardingPokemonDisplayJson: jsonBytesToProto(g.GuardingPokemonDisplay),
		AvailableSlots:             g.AvailableSlots,
		TeamId:                     g.TeamId,
		RaidLevel:                  g.RaidLevel,
		Enabled:                    g.Enabled,
		ExRaidEligible:             g.ExRaidEligible,
		InBattle:                   g.InBattle,
		RaidPokemonMove_1:          g.RaidPokemonMove1,
		RaidPokemonMove_2:          g.RaidPokemonMove2,
		RaidPokemonForm:            g.RaidPokemonForm,
		RaidPokemonAlignment:       g.RaidPokemonAlignment,
		RaidPokemonCp:              g.RaidPokemonCp,
		RaidIsExclusive:            g.RaidIsExclusive,
		CellId:                     g.CellId,
		Deleted:                    g.Deleted,
		TotalCp:                    g.TotalCp,
		FirstSeenTimestamp:         g.FirstSeenTimestamp,
		RaidPokemonGender:          g.RaidPokemonGender,
		SponsorId:                  g.SponsorId,
		PartnerId:                  g.PartnerId,
		RaidPokemonCostume:         g.RaidPokemonCostume,
		RaidPokemonEvolution:       g.RaidPokemonEvolution,
		ArScanEligible:             g.ArScanEligible,
		PowerUpLevel:               g.PowerUpLevel,
		PowerUpPoints:              g.PowerUpPoints,
		PowerUpEndTimestamp:        g.PowerUpEndTimestamp,
		Description:                g.Description,
		DefendersJson:              jsonBytesToProto(g.Defenders),
		RsvpsJson:                  rawJsonToProto(g.Rsvps),
	}
}

func incidentToProto(i *ApiPokestopIncident) *pb.Incident {
	return &pb.Incident{
		Id:              i.Id,
		PokestopId:      i.PokestopId,
		DisplayType:     int32(i.DisplayType),
		Style:           int32(i.Style),
		Character:       int32(i.Character),
		Start:           i.StartTime,
		Expiration:      i.ExpirationTime,
		Confirmed:       i.Confirmed,
		Updated:         i.Updated,
		Slot_1PokemonId: i.Slot1PokemonId,
		Slot_1Form:      i.Slot1Form,
		Slot_2PokemonId: i.Slot2PokemonId,
		Slot_2Form:      i.Slot2Form,
		Slot_3PokemonId: i.Slot3PokemonId,
		Slot_3Form:      i.Slot3Form,
	}
}

func incidentsToProto(in []ApiPokestopIncident) []*pb.Incident {
	if len(in) == 0 {
		return nil
	}
	out := make([]*pb.Incident, len(in))
	for i := range in {
		out[i] = incidentToProto(&in[i])
	}
	return out
}

func pokestopToProto(p *ApiPokestopResult) *pb.Pokestop {
	return &pb.Pokestop{
		Id:                             p.Id,
		Lat:                            p.Lat,
		Lon:                            p.Lon,
		Name:                           p.Name,
		Url:                            p.Url,
		LureExpireTimestamp:            p.LureExpireTimestamp,
		LastModifiedTimestamp:          p.LastModifiedTimestamp,
		Updated:                        p.Updated,
		Enabled:                        p.Enabled,
		QuestType:                      p.QuestType,
		QuestTimestamp:                 p.QuestTimestamp,
		QuestTarget:                    p.QuestTarget,
		QuestRewardType:                p.QuestRewardType,
		QuestItemId:                    p.QuestItemId,
		QuestRewardAmount:              p.QuestRewardAmount,
		QuestPokemonId:                 p.QuestPokemonId,
		QuestPokemonFormId:             p.QuestPokemonFormId,
		QuestConditionsJson:            rawJsonToProto(p.QuestConditions),
		QuestRewardsJson:               rawJsonToProto(p.QuestRewards),
		QuestTemplate:                  p.QuestTemplate,
		QuestTitle:                     p.QuestTitle,
		QuestExpiry:                    p.QuestExpiry,
		CellId:                         p.CellId,
		Deleted:                        p.Deleted,
		LureId:                         int32(p.LureId),
		FirstSeenTimestamp:             p.FirstSeenTimestamp,
		SponsorId:                      p.SponsorId,
		PartnerId:                      p.PartnerId,
		ArScanEligible:                 p.ArScanEligible,
		PowerUpLevel:                   p.PowerUpLevel,
		PowerUpPoints:                  p.PowerUpPoints,
		PowerUpEndTimestamp:            p.PowerUpEndTimestamp,
		AlternativeQuestType:           p.AlternativeQuestType,
		AlternativeQuestTimestamp:      p.AlternativeQuestTimestamp,
		AlternativeQuestTarget:         p.AlternativeQuestTarget,
		AlternativeQuestRewardType:     p.AlternativeQuestRewardType,
		AlternativeQuestItemId:         p.AlternativeQuestItemId,
		AlternativeQuestRewardAmount:   p.AlternativeQuestRewardAmount,
		AlternativeQuestPokemonId:      p.AlternativeQuestPokemonId,
		AlternativeQuestPokemonFormId:  p.AlternativeQuestPokemonFormId,
		AlternativeQuestConditionsJson: rawJsonToProto(p.AlternativeQuestConditions),
		AlternativeQuestRewardsJson:    rawJsonToProto(p.AlternativeQuestRewards),
		AlternativeQuestTemplate:       p.AlternativeQuestTemplate,
		AlternativeQuestTitle:          p.AlternativeQuestTitle,
		AlternativeQuestExpiry:         p.AlternativeQuestExpiry,
		Description:                    p.Description,
		ShowcaseFocusJson:              rawJsonToProto(p.ShowcaseFocus),
		ShowcasePokemonId:              p.ShowcasePokemon,
		ShowcasePokemonFormId:          p.ShowcasePokemonForm,
		ShowcasePokemonTypeId:          p.ShowcasePokemonType,
		ShowcaseRankingStandard:        p.ShowcaseRankingStandard,
		ShowcaseExpiry:                 p.ShowcaseExpiry,
		ShowcaseRankingsJson:           rawJsonToProto(p.ShowcaseRankings),
		Invasions:                      incidentsToProto(p.Invasions),
	}
}

func stationBattleToProto(b *ApiStationBattleResult) *pb.StationBattle {
	return &pb.StationBattle{
		BreadBattleSeed:           b.BreadBattleSeed,
		BattleLevel:               int32(b.BattleLevel),
		BattleStart:               b.BattleStart,
		BattleEnd:                 b.BattleEnd,
		Updated:                   b.Updated,
		BattlePokemonId:           b.BattlePokemonId,
		BattlePokemonForm:         b.BattlePokemonForm,
		BattlePokemonCostume:      b.BattlePokemonCostume,
		BattlePokemonGender:       b.BattlePokemonGender,
		BattlePokemonAlignment:    b.BattlePokemonAlignment,
		BattlePokemonBreadMode:    b.BattlePokemonBreadMode,
		BattlePokemonMove_1:       b.BattlePokemonMove1,
		BattlePokemonMove_2:       b.BattlePokemonMove2,
		BattlePokemonStamina:      b.BattlePokemonStamina,
		BattlePokemonCpMultiplier: b.BattlePokemonCpMultiplier,
	}
}

func stationBattlesToProto(in []ApiStationBattleResult) []*pb.StationBattle {
	if len(in) == 0 {
		return nil
	}
	out := make([]*pb.StationBattle, len(in))
	for i := range in {
		out[i] = stationBattleToProto(&in[i])
	}
	return out
}

func stationToProto(s *ApiStationResult) *pb.Station {
	return &pb.Station{
		Id:                        s.Id,
		Lat:                       s.Lat,
		Lon:                       s.Lon,
		Name:                      s.Name,
		CellId:                    s.CellId,
		StartTime:                 s.StartTime,
		EndTime:                   s.EndTime,
		CooldownComplete:          s.CooldownComplete,
		IsBattleAvailable:         s.IsBattleAvailable,
		IsInactive:                s.IsInactive,
		Updated:                   s.Updated,
		BattleLevel:               s.BattleLevel,
		BattleStart:               s.BattleStart,
		BattleEnd:                 s.BattleEnd,
		BattlePokemonId:           s.BattlePokemonId,
		BattlePokemonForm:         s.BattlePokemonForm,
		BattlePokemonCostume:      s.BattlePokemonCostume,
		BattlePokemonGender:       s.BattlePokemonGender,
		BattlePokemonAlignment:    s.BattlePokemonAlignment,
		BattlePokemonBreadMode:    s.BattlePokemonBreadMode,
		BattlePokemonMove_1:       s.BattlePokemonMove1,
		BattlePokemonMove_2:       s.BattlePokemonMove2,
		BattlePokemonStamina:      s.BattlePokemonStamina,
		BattlePokemonCpMultiplier: s.BattlePokemonCpMultiplier,
		TotalStationedPokemon:     s.TotalStationedPokemon,
		TotalStationedGmax:        s.TotalStationedGmax,
		StationedPokemonJson:      rawJsonToProto(s.StationedPokemon),
		Battles:                   stationBattlesToProto(s.Battles),
	}
}

func gymsToProto(in []*ApiGymResult) []*pb.Gym {
	out := make([]*pb.Gym, 0, len(in))
	for _, g := range in {
		if g != nil {
			out = append(out, gymToProto(g))
		}
	}
	return out
}

func pokestopsToProto(in []*ApiPokestopResult) []*pb.Pokestop {
	out := make([]*pb.Pokestop, 0, len(in))
	for _, p := range in {
		if p != nil {
			out = append(out, pokestopToProto(p))
		}
	}
	return out
}

func stationsToProto(in []*ApiStationResult) []*pb.Station {
	out := make([]*pb.Station, 0, len(in))
	for _, s := range in {
		if s != nil {
			out = append(out, stationToProto(s))
		}
	}
	return out
}

// GrpcScanGyms is the gRPC counterpart of GymScanEndpoint.
func GrpcScanGyms(req *pb.FortScanRequest, dbDetails db.DbDetails) *pb.GymScanResponse {
	res := GymScanEndpoint(fortScanRequestFromProto(req), dbDetails)
	return &pb.GymScanResponse{
		Gyms:         gymsToProto(res.Gyms),
		Examined:     int32(res.Examined),
		Skipped:      int32(res.Skipped),
		Total:        int32(res.Total),
		LimitReached: res.LimitReached,
	}
}

// GrpcScanPokestops is the gRPC counterpart of PokestopScanEndpoint.
func GrpcScanPokestops(req *pb.FortScanRequest, dbDetails db.DbDetails) *pb.PokestopScanResponse {
	res := PokestopScanEndpoint(fortScanRequestFromProto(req), dbDetails)
	return &pb.PokestopScanResponse{
		Pokestops:    pokestopsToProto(res.Pokestops),
		Examined:     int32(res.Examined),
		Skipped:      int32(res.Skipped),
		Total:        int32(res.Total),
		LimitReached: res.LimitReached,
	}
}

// GrpcScanStations is the gRPC counterpart of StationScanEndpoint.
func GrpcScanStations(req *pb.FortScanRequest, dbDetails db.DbDetails) *pb.StationScanResponse {
	res := StationScanEndpoint(fortScanRequestFromProto(req), dbDetails)
	return &pb.StationScanResponse{
		Stations:     stationsToProto(res.Stations),
		Examined:     int32(res.Examined),
		Skipped:      int32(res.Skipped),
		Total:        int32(res.Total),
		LimitReached: res.LimitReached,
	}
}

// GrpcScanForts is the gRPC counterpart of FortCombinedScanEndpoint.
func GrpcScanForts(req *pb.FortCombinedScanRequest, dbDetails db.DbDetails) *pb.FortScanResponse {
	res := FortCombinedScanEndpoint(fortCombinedScanRequestFromProto(req), dbDetails)
	return &pb.FortScanResponse{
		Gyms:         gymsToProto(res.Gyms),
		Pokestops:    pokestopsToProto(res.Pokestops),
		Stations:     stationsToProto(res.Stations),
		Examined:     int32(res.Examined),
		Skipped:      int32(res.Skipped),
		Total:        int32(res.Total),
		LimitReached: res.LimitReached,
	}
}
