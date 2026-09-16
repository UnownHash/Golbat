package decoder

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/guregu/null/v6"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/db"
	pb "golbat/grpc"
)

// assertNoPresentOptionals fails the test for every populated field in msg
// that has explicit presence (an "optional" scalar, a message field, or a
// oneof member — see protoreflect.FieldDescriptor.HasPresence) and whose
// name is not in allow. Implicit-presence fields (plain proto3 scalars,
// repeated fields) are never flagged since a conversion has no way to leave
// them "unset" as opposed to zero-valued.
func assertNoPresentOptionals(t *testing.T, msg proto.Message, allow ...string) {
	t.Helper()
	allowed := make(map[string]bool, len(allow))
	for _, a := range allow {
		allowed[a] = true
	}
	msg.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())
		if fd.HasPresence() && !allowed[name] {
			t.Errorf("%s.%s unexpectedly present: %v", msg.ProtoReflect().Descriptor().Name(), name, v.Interface())
		}
		return true
	})
}

func TestFortScanRequestFromProto(t *testing.T) {
	req := &pb.FortScanRequest{
		Min:           &pb.LatLon{Lat: 1, Lon: 2},
		Max:           &pb.LatLon{Lat: 3, Lon: 4},
		Limit:         25,
		WithIncidents: true,
		UpdatedAfter:  ptr(int64(1700000000)),
		Filters: []*pb.FortDnfFilter{{
			IsArScanEligible:  ptr(true),
			AvailableSlots:    &pb.IntRange{Min: ptr(int32(1))},
			TeamId:            []int32{1, 3},
			RaidLevel:         []int32{5},
			RaidPokemonId:     []*pb.DnfId{{PokemonId: 150, Form: ptr(int32(1))}},
			LureId:            []int32{501},
			QuestRewardType:   []int32{7},
			QuestRewardAmount: &pb.IntRange{Min: ptr(int32(2)), Max: ptr(int32(5))},
			IncidentCharacter: []int32{41},
			ContestFocus:      []*pb.ContestFocus{{Type: "buddy", MinLevel: ptr(int32(3))}, {Type: "other"}},
			BattleLevel:       []int32{6},
			StationedGmax:     ptr(false),
			BattleAvailable:   ptr(true),
		}},
	}
	got := fortScanRequestFromProto(req)
	if got.Min != (ApiLatLon{Lat: 1, Lon: 2}) || got.Max != (ApiLatLon{Lat: 3, Lon: 4}) || got.Limit != 25 || !got.WithIncidents || got.UpdatedAfter != 1700000000 {
		t.Fatalf("bbox/limit/with_incidents/updated_after = %+v", got)
	}
	if len(got.DnfFilters) != 1 {
		t.Fatalf("filters = %d, want 1", len(got.DnfFilters))
	}
	f := got.DnfFilters[0]
	if f.IsArScanEligible == nil || !*f.IsArScanEligible {
		t.Error("is_ar_scan_eligible must carry through")
	}
	if f.AvailableSlots == nil || f.AvailableSlots.Min != 1 || f.AvailableSlots.Max != math.MaxInt16 {
		t.Errorf("available_slots = %+v", f.AvailableSlots)
	}
	if len(f.TeamId) != 2 || f.TeamId[0] != 1 || f.TeamId[1] != 3 || len(f.RaidLevel) != 1 || f.RaidLevel[0] != 5 {
		t.Errorf("team/raid level = %v %v", f.TeamId, f.RaidLevel)
	}
	if len(f.RaidPokemon) != 1 || f.RaidPokemon[0].Pokemon != 150 || f.RaidPokemon[0].Form == nil || *f.RaidPokemon[0].Form != 1 {
		t.Errorf("raid pokemon = %+v", f.RaidPokemon)
	}
	if len(f.LureId) != 1 || f.LureId[0] != 501 || len(f.QuestRewardType) != 1 || f.QuestRewardType[0] != 7 {
		t.Errorf("lure/reward type = %v %v", f.LureId, f.QuestRewardType)
	}
	if f.QuestRewardAmount == nil || f.QuestRewardAmount.Min != 2 || f.QuestRewardAmount.Max != 5 {
		t.Errorf("reward amount = %+v", f.QuestRewardAmount)
	}
	if len(f.IncidentCharacter) != 1 || f.IncidentCharacter[0] != 41 {
		t.Errorf("incident character = %v", f.IncidentCharacter)
	}
	if len(f.ContestFocus) != 2 || f.ContestFocus[0].Type != "buddy" || f.ContestFocus[0].MinLevel == nil || *f.ContestFocus[0].MinLevel != 3 ||
		f.ContestFocus[1].Type != "other" || f.ContestFocus[1].MinLevel != nil {
		t.Errorf("contest focus = %+v", f.ContestFocus)
	}
	if len(f.BattleLevel) != 1 || f.BattleLevel[0] != 6 || f.StationedGmax == nil || *f.StationedGmax || f.StationActive != nil {
		t.Errorf("battle level / gmax / active = %v %v %v", f.BattleLevel, f.StationedGmax, f.StationActive)
	}
	if f.BattleAvailable == nil || !*f.BattleAvailable {
		t.Errorf("battle_available = %v, want true", f.BattleAvailable)
	}
	// Unsent lists are nil (no constraint), never empty non-nil slices.
	if f.RaidTempEvolutionId != nil || f.QuestRewardItemId != nil || f.QuestRewardPokemon != nil || f.IncidentDisplayType != nil ||
		f.ContestPokemon != nil || f.ContestPokemonType != nil || f.ContestRankingStandard != nil || f.BattlePokemon != nil {
		t.Errorf("unsent lists must be nil: %+v", f)
	}

	bare := fortScanRequestFromProto(&pb.FortScanRequest{Min: &pb.LatLon{}, Max: &pb.LatLon{}})
	if bare.DnfFilters != nil {
		t.Errorf("no filters must be a nil slice, got %#v", bare.DnfFilters)
	}
}

func TestFortCombinedScanRequestFromProto(t *testing.T) {
	req := &pb.FortCombinedScanRequest{
		Min:           &pb.LatLon{Lat: 1, Lon: 2},
		Max:           &pb.LatLon{Lat: 3, Lon: 4},
		Limit:         10,
		WithIncidents: true,
		UpdatedAfter:  ptr(int64(42)),
		Gyms:          &pb.FortTypeScanGroup{Filters: []*pb.FortDnfFilter{{RaidLevel: []int32{5}}}, Limit: 7},
		Pokestops:     &pb.FortTypeScanGroup{},
	}
	got := fortCombinedScanRequestFromProto(req)
	if got.Gyms == nil || got.Gyms.Limit != 7 || got.Pokestops == nil || got.Pokestops.Limit != 0 {
		t.Errorf("per-type limits = gyms %+v pokestops %+v, want 7 and 0", got.Gyms, got.Pokestops)
	}
	if got.Limit != 10 || !got.WithIncidents || got.Min.Lat != 1 || got.Max.Lon != 4 || got.UpdatedAfter != 42 {
		t.Fatalf("header = %+v", got)
	}
	if got.Gyms == nil || len(got.Gyms.DnfFilters) != 1 || len(got.Gyms.DnfFilters[0].RaidLevel) != 1 {
		t.Errorf("gyms group = %+v", got.Gyms)
	}
	if got.Pokestops == nil || got.Pokestops.DnfFilters != nil {
		t.Errorf("present-but-empty group must be non-nil with nil filters (match all), got %+v", got.Pokestops)
	}
	if got.Stations != nil {
		t.Errorf("unset group must stay nil (excluded type), got %+v", got.Stations)
	}
}

func TestGymToProto(t *testing.T) {
	g := ApiGymResult{
		Id: "0123456789abcdef0123456789abcdef.16", Lat: 40, Lon: -70,
		Name: ptr("Gym"), Url: ptr("https://img/gym.png"),
		LastModifiedTimestamp: ptr(int64(1)), RaidEndTimestamp: ptr(int64(2)), RaidSpawnTimestamp: ptr(int64(3)), RaidBattleTimestamp: ptr(int64(4)),
		Updated: 5, RaidPokemonId: ptr(int64(150)), GuardingPokemonId: ptr(int64(25)),
		GuardingPokemonDisplay: ApiGymGuardingPokemonRaw(`{"gender":1}`),
		AvailableSlots:         ptr(int64(3)), TeamId: ptr(int64(2)), RaidLevel: ptr(int64(5)), Enabled: ptr(int64(1)), ExRaidEligible: ptr(int64(0)), InBattle: ptr(int64(0)),
		RaidPokemonMove1: ptr(int64(10)), RaidPokemonMove2: ptr(int64(20)), RaidPokemonForm: ptr(int64(1)), RaidPokemonAlignment: ptr(int64(0)), RaidPokemonCp: ptr(int64(45000)), RaidIsExclusive: ptr(int64(0)),
		CellId: ptr(int64(99)), Deleted: false, TotalCp: ptr(int64(9000)), FirstSeenTimestamp: 6, RaidPokemonGender: ptr(int64(1)), SponsorId: ptr(int64(7)), PartnerId: ptr("p"),
		RaidPokemonCostume: ptr(int64(0)), RaidPokemonEvolution: ptr(int64(1)), ArScanEligible: ptr(int64(1)), PowerUpLevel: ptr(int64(2)), PowerUpPoints: ptr(int64(50)), PowerUpEndTimestamp: ptr(int64(8)),
		Description: ptr("desc"), Defenders: ApiGymDefendersRaw(`[{"pokemon_id":25}]`), Rsvps: rawMsg(`{"going":1}`),
	}
	got := gymToProto(&g)
	if got.Id != g.Id || got.Lat != 40 || got.Lon != -70 || got.GetName() != "Gym" || got.GetUrl() != "https://img/gym.png" {
		t.Errorf("identity = %+v", got)
	}
	if got.GetLastModifiedTimestamp() != 1 || got.GetRaidEndTimestamp() != 2 || got.GetRaidSpawnTimestamp() != 3 || got.GetRaidBattleTimestamp() != 4 || got.Updated != 5 || got.FirstSeenTimestamp != 6 || got.GetPowerUpEndTimestamp() != 8 {
		t.Errorf("timestamps = %+v", got)
	}
	if got.GetRaidPokemonId() != 150 || got.GetGuardingPokemonId() != 25 || got.GetAvailableSlots() != 3 || got.GetTeamId() != 2 || got.GetRaidLevel() != 5 || got.GetRaidPokemonMove_1() != 10 || got.GetRaidPokemonMove_2() != 20 || got.GetRaidPokemonCp() != 45000 || got.GetTotalCp() != 9000 {
		t.Errorf("raid/gym numbers = %+v", got)
	}
	if got.ExRaidEligible == nil || *got.ExRaidEligible != 0 || got.InBattle == nil || got.RaidIsExclusive == nil || got.RaidPokemonAlignment == nil || got.RaidPokemonCostume == nil {
		t.Error("zero-valued present int64 pointers must stay present")
	}
	if got.GetGuardingPokemonDisplayJson() != `{"gender":1}` || got.GetDefendersJson() != `[{"pokemon_id":25}]` || got.GetRsvpsJson() != `{"going":1}` {
		t.Errorf("json passthrough = %q %q %q", got.GetGuardingPokemonDisplayJson(), got.GetDefendersJson(), got.GetRsvpsJson())
	}
	if got.GetDescription() != "desc" || got.GetPartnerId() != "p" || got.GetSponsorId() != 7 || got.GetCellId() != 99 || got.Deleted {
		t.Errorf("misc = %+v", got)
	}
	if got.GetEnabled() != 1 || got.GetRaidPokemonForm() != 1 || got.GetRaidPokemonGender() != 1 || got.GetRaidPokemonEvolution() != 1 || got.GetArScanEligible() != 1 || got.GetPowerUpLevel() != 2 || got.GetPowerUpPoints() != 50 {
		t.Errorf("enabled/raid form/gender/evolution/ar/power-up = %+v", got)
	}

	empty := gymToProto(&ApiGymResult{Id: "x"})
	if empty.Name != nil || empty.GuardingPokemonDisplayJson != nil || empty.DefendersJson != nil || empty.RsvpsJson != nil || empty.RaidPokemonId != nil {
		t.Errorf("nil API fields must be unset: %+v", empty)
	}
	assertNoPresentOptionals(t, empty, "id")
}

func rawMsg(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

func TestPokestopToProto(t *testing.T) {
	p := ApiPokestopResult{
		Id: "fedcba9876543210fedcba9876543210.16", Lat: 12.3, Lon: -65.4, Name: ptr("Stop"), Url: ptr("u"),
		LureExpireTimestamp: ptr(int64(1)), LastModifiedTimestamp: ptr(int64(2)), Updated: 3, Enabled: ptr(true),
		QuestType: ptr(int64(7)), QuestTimestamp: ptr(int64(4)), QuestTarget: ptr(int64(3)), QuestRewardType: ptr(int64(1)), QuestItemId: ptr(int64(0)), QuestRewardAmount: ptr(int64(100)),
		QuestPokemonId: ptr(int64(25)), QuestPokemonFormId: ptr(int64(0)),
		QuestConditions: rawMsg(`[]`), QuestRewards: rawMsg(`[{"type":1}]`), QuestTemplate: ptr("tmpl"), QuestTitle: ptr("title"), QuestExpiry: ptr(int64(5)),
		CellId: ptr(int64(9)), Deleted: true, LureId: 501, FirstSeenTimestamp: 6, SponsorId: ptr(int64(8)), PartnerId: ptr("pp"), ArScanEligible: ptr(int64(1)),
		PowerUpLevel: ptr(int64(2)), PowerUpPoints: ptr(int64(50)), PowerUpEndTimestamp: ptr(int64(10)),
		AlternativeQuestType: ptr(int64(7)), AlternativeQuestTimestamp: ptr(int64(11)), AlternativeQuestTarget: ptr(int64(5)), AlternativeQuestRewardType: ptr(int64(2)), AlternativeQuestItemId: ptr(int64(1)), AlternativeQuestRewardAmount: ptr(int64(3)),
		AlternativeQuestPokemonId: ptr(int64(1)), AlternativeQuestPokemonFormId: ptr(int64(2)),
		AlternativeQuestConditions: rawMsg(`[{"type":2}]`), AlternativeQuestRewards: rawMsg(`[{"type":2}]`), AlternativeQuestTemplate: ptr("alt"), AlternativeQuestTitle: ptr("Alt"), AlternativeQuestExpiry: ptr(int64(12)),
		Description: ptr("d"), ShowcaseFocus: rawMsg(`{"buddy":3}`), ShowcasePokemon: ptr(int64(1)), ShowcasePokemonForm: ptr(int64(2)), ShowcasePokemonType: ptr(int64(3)), ShowcaseRankingStandard: ptr(int64(4)), ShowcaseExpiry: ptr(int64(13)), ShowcaseRankings: rawMsg(`[]`),
		Invasions: []ApiPokestopIncident{{
			Id: "-5", PokestopId: "fedcba9876543210fedcba9876543210.16", DisplayType: 1, Style: 2, Character: 41, StartTime: 14, ExpirationTime: 15, Confirmed: true, Updated: 16,
			Slot1PokemonId: ptr(int64(1)), Slot1Form: ptr(int64(0)), Slot2PokemonId: ptr(int64(2)),
		}},
	}
	got := pokestopToProto(&p)
	if got.Id != p.Id || got.Lat != 12.3 || got.Lon != -65.4 || got.GetName() != "Stop" || got.GetUrl() != "u" || !got.GetEnabled() || !got.Deleted || got.LureId != 501 {
		t.Errorf("identity = %+v", got)
	}
	if got.GetLureExpireTimestamp() != 1 || got.GetLastModifiedTimestamp() != 2 || got.Updated != 3 || got.GetQuestTimestamp() != 4 || got.GetQuestExpiry() != 5 || got.FirstSeenTimestamp != 6 || got.GetPowerUpEndTimestamp() != 10 || got.GetAlternativeQuestTimestamp() != 11 || got.GetAlternativeQuestExpiry() != 12 || got.GetShowcaseExpiry() != 13 {
		t.Errorf("timestamps = %+v", got)
	}
	if got.GetQuestType() != 7 || got.GetQuestTarget() != 3 || got.GetQuestRewardType() != 1 || got.QuestItemId == nil || got.GetQuestRewardAmount() != 100 || got.GetQuestPokemonId() != 25 || got.QuestPokemonFormId == nil {
		t.Errorf("quest = %+v", got)
	}
	if got.GetQuestConditionsJson() != `[]` || got.GetQuestRewardsJson() != `[{"type":1}]` || got.GetAlternativeQuestConditionsJson() != `[{"type":2}]` || got.GetAlternativeQuestRewardsJson() != `[{"type":2}]` || got.GetShowcaseFocusJson() != `{"buddy":3}` || got.GetShowcaseRankingsJson() != `[]` {
		t.Errorf("json passthrough = %+v", got)
	}
	if got.GetQuestTemplate() != "tmpl" || got.GetQuestTitle() != "title" || got.GetAlternativeQuestTemplate() != "alt" || got.GetAlternativeQuestTitle() != "Alt" || got.GetDescription() != "d" || got.GetPartnerId() != "pp" {
		t.Errorf("strings = %+v", got)
	}
	if got.GetAlternativeQuestType() != 7 || got.GetAlternativeQuestTarget() != 5 || got.GetAlternativeQuestRewardType() != 2 || got.GetAlternativeQuestItemId() != 1 || got.GetAlternativeQuestRewardAmount() != 3 || got.GetAlternativeQuestPokemonId() != 1 || got.GetAlternativeQuestPokemonFormId() != 2 {
		t.Errorf("alternative quest = %+v", got)
	}
	if got.GetShowcasePokemonId() != 1 || got.GetShowcasePokemonFormId() != 2 || got.GetShowcasePokemonTypeId() != 3 || got.GetShowcaseRankingStandard() != 4 {
		t.Errorf("showcase = %+v", got)
	}
	if got.GetCellId() != 9 || got.GetSponsorId() != 8 || got.GetArScanEligible() != 1 || got.GetPowerUpLevel() != 2 || got.GetPowerUpPoints() != 50 {
		t.Errorf("misc = %+v", got)
	}
	if len(got.Invasions) != 1 {
		t.Fatalf("invasions = %+v", got.Invasions)
	}
	inc := got.Invasions[0]
	if inc.Id != "-5" || inc.PokestopId != p.Id || inc.DisplayType != 1 || inc.Style != 2 || inc.Character != 41 || inc.Start != 14 || inc.Expiration != 15 || !inc.Confirmed || inc.Updated != 16 {
		t.Errorf("incident = %+v", inc)
	}
	if inc.GetSlot_1PokemonId() != 1 || inc.Slot_1Form == nil || *inc.Slot_1Form != 0 || inc.GetSlot_2PokemonId() != 2 || inc.Slot_2Form != nil || inc.Slot_3PokemonId != nil || inc.Slot_3Form != nil {
		t.Errorf("incident slots = %+v", inc)
	}

	empty := pokestopToProto(&ApiPokestopResult{Id: "x"})
	if empty.Name != nil || empty.QuestConditionsJson != nil || empty.ShowcaseRankingsJson != nil || empty.Enabled != nil || len(empty.Invasions) != 0 {
		t.Errorf("nil API fields must be unset: %+v", empty)
	}
	assertNoPresentOptionals(t, empty, "id")
}

func TestStationToProto(t *testing.T) {
	s := ApiStationResult{
		Id: "0000000000000000000000000000abcd.11", Lat: 1, Lon: 2, Name: "Station", CellId: 3, StartTime: 4, EndTime: 5, CooldownComplete: 6, IsBattleAvailable: true, IsInactive: false, Updated: 7,
		BattleLevel: ptr(int64(6)), BattleStart: ptr(int64(8)), BattleEnd: ptr(int64(9)), BattlePokemonId: ptr(int64(150)), BattlePokemonForm: ptr(int64(1)), BattlePokemonCostume: ptr(int64(0)), BattlePokemonGender: ptr(int64(1)),
		BattlePokemonAlignment: ptr(int64(0)), BattlePokemonBreadMode: ptr(int64(2)), BattlePokemonMove1: ptr(int64(10)), BattlePokemonMove2: ptr(int64(20)), BattlePokemonStamina: ptr(int64(300)), BattlePokemonCpMultiplier: ptr(0.79),
		TotalStationedPokemon: ptr(int64(4)), TotalStationedGmax: ptr(int64(1)), StationedPokemon: rawMsg(`[{"pokemon_id":1}]`),
		Battles: []ApiStationBattleResult{{BreadBattleSeed: 11, BattleLevel: 6, BattleStart: 8, BattleEnd: 9, Updated: 7, BattlePokemonId: ptr(int64(150)), BattlePokemonMove1: ptr(int64(10)), BattlePokemonCpMultiplier: ptr(0.79)}},
	}
	got := stationToProto(&s)
	if got.Id != s.Id || got.Lat != 1 || got.Lon != 2 || got.Name != "Station" || got.CellId != 3 || got.StartTime != 4 || got.EndTime != 5 || got.CooldownComplete != 6 || !got.IsBattleAvailable || got.IsInactive || got.Updated != 7 {
		t.Errorf("identity = %+v", got)
	}
	if got.GetBattleLevel() != 6 || got.GetBattleStart() != 8 || got.GetBattleEnd() != 9 || got.GetBattlePokemonId() != 150 || got.GetBattlePokemonForm() != 1 || got.BattlePokemonCostume == nil || got.GetBattlePokemonGender() != 1 || got.BattlePokemonAlignment == nil || got.GetBattlePokemonBreadMode() != 2 || got.GetBattlePokemonMove_1() != 10 || got.GetBattlePokemonMove_2() != 20 || got.GetBattlePokemonStamina() != 300 || got.GetBattlePokemonCpMultiplier() != 0.79 {
		t.Errorf("top battle = %+v", got)
	}
	if got.GetTotalStationedPokemon() != 4 || got.GetTotalStationedGmax() != 1 || got.GetStationedPokemonJson() != `[{"pokemon_id":1}]` {
		t.Errorf("stationed = %+v", got)
	}
	if len(got.Battles) != 1 {
		t.Fatalf("battles = %+v", got.Battles)
	}
	b := got.Battles[0]
	if b.BreadBattleSeed != 11 || b.BattleLevel != 6 || b.BattleStart != 8 || b.BattleEnd != 9 || b.Updated != 7 || b.GetBattlePokemonId() != 150 || b.GetBattlePokemonMove_1() != 10 || b.GetBattlePokemonCpMultiplier() != 0.79 || b.BattlePokemonForm != nil {
		t.Errorf("battle = %+v", b)
	}

	empty := stationToProto(&ApiStationResult{Id: "x"})
	if empty.BattleLevel != nil || empty.StationedPokemonJson != nil || len(empty.Battles) != 0 {
		t.Errorf("nil API fields must be unset: %+v", empty)
	}
	assertNoPresentOptionals(t, empty, "id")
}

// A gym present in the tree, lookup cache and record cache comes back over
// the gRPC path with its record fields, proving the endpoint delegation and
// result conversion end to end (no database involved: the record is cached).
func TestGrpcScanGymsReturnsLiveGym(t *testing.T) {
	withScanLimits(t)
	id := mustFortId(t, "0123456789abcdef0123456789abcdef.16")
	const lat, lon = -33.25, 151.75
	gym := &Gym{GymData: GymData{Id: id, Lat: lat, Lon: lon, Name: null.StringFrom("Live Gym"), Updated: 42}}

	fortTreeMutex.Lock()
	fortTree.Insert([2]float64{lon, lat}, [2]float64{lon, lat}, id)
	fortTreeMutex.Unlock()
	fortLookupCache.Store(id, FortLookup{FortType: GYM, Lat: lat, Lon: lon})
	gymCache.Set(id, gym, time.Minute)
	fortTreeSnapshot.Store(nil)
	t.Cleanup(func() {
		fortLookupCache.Delete(id)
		gymCache.Delete(id)
		fortTreeMutex.Lock()
		fortTree.Delete([2]float64{lon, lat}, [2]float64{lon, lat}, id)
		fortTreeMutex.Unlock()
		fortTreeSnapshot.Store(nil)
	})

	req := &pb.FortScanRequest{
		Min: &pb.LatLon{Lat: lat - 0.01, Lon: lon - 0.01},
		Max: &pb.LatLon{Lat: lat + 0.01, Lon: lon + 0.01},
	}
	resp := GrpcScanGyms(req, db.DbDetails{})
	if len(resp.Gyms) != 1 {
		t.Fatalf("got %d gyms, want 1 (examined %d skipped %d)", len(resp.Gyms), resp.Examined, resp.Skipped)
	}
	if got := resp.Gyms[0]; got.Id != id.String() || got.GetName() != "Live Gym" || got.Lat != lat || got.Lon != lon || got.Updated != 42 {
		t.Errorf("gym = %+v", got)
	}
	if resp.Examined < 1 || resp.Total < 1 || resp.LimitReached {
		t.Errorf("counts = %+v", resp)
	}

	// The same box through the combined scan, gyms group only.
	combined := GrpcScanForts(&pb.FortCombinedScanRequest{Min: req.Min, Max: req.Max, Gyms: &pb.FortTypeScanGroup{}}, db.DbDetails{})
	if len(combined.Gyms) != 1 || len(combined.Pokestops) != 0 || len(combined.Stations) != 0 {
		t.Errorf("combined = %d gyms, %d pokestops, %d stations; want 1/0/0", len(combined.Gyms), len(combined.Pokestops), len(combined.Stations))
	}
	if combined.GetGymsStats().GetExamined() < 1 || combined.GetGymsStats().GetLimitReached() {
		t.Errorf("gyms_stats = %+v, want examined >= 1 and limit_reached false", combined.GetGymsStats())
	}
	if combined.GetPokestopsStats().GetExamined() != 0 || combined.GetStationsStats().GetExamined() != 0 {
		t.Errorf("excluded types must report zero examined: pokestops %+v stations %+v", combined.GetPokestopsStats(), combined.GetStationsStats())
	}
	// Pokestop and station scans of the same box see nothing.
	if r := GrpcScanPokestops(req, db.DbDetails{}); len(r.Pokestops) != 0 {
		t.Errorf("pokestop scan returned %d results for a gym-only box", len(r.Pokestops))
	}
	if r := GrpcScanStations(req, db.DbDetails{}); len(r.Stations) != 0 {
		t.Errorf("station scan returned %d results for a gym-only box", len(r.Stations))
	}
}
