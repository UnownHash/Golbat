package decoder

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// TestUpdateGymFromFortShim locks in Wave 2a behavior for the hyperpb
// migration: updateGymFromFort must extract identical entity state via
// pogoshim getters as the pre-migration code extracted via direct
// *pogo.PokemonFortProto field access. Build a synthetic gym-variant
// PokemonFortProto (team, raid info + raid pokemon, gym display), wrap it
// exactly the way decodeGMO wraps a fort in its collection loop
// (pogoshim.AsPokemonFortProto(fort.ProtoReflect())), and assert the
// resulting Gym fields.
func TestUpdateGymFromFortShim(t *testing.T) {
	const cellId = uint64(0x89c25a30)
	const timestampMs = int64(1_700_000_000_000)

	fort := &pogo.PokemonFortProto{
		FortId:           "GYM1",
		Latitude:         12.3456789,
		Longitude:        -67.891234,
		Team:             pogo.Team_TEAM_RED,
		FortType:         pogo.FortType_GYM,
		Enabled:          true,
		GuardPokemonId:   pogo.HoloPokemonId_MACHAMP,
		LastModifiedMs:   timestampMs,
		IsExRaidEligible: true,
		ImageUrl:         "https://example.com/gym.png",
		GymDisplay: &pogo.GymDisplayProto{
			SlotsAvailable: 4,
			TotalGymCp:     6000,
		},
		RaidInfo: &pogo.RaidInfoProto{
			RaidSeed:     42,
			RaidSpawnMs:  1_700_000_100_000,
			RaidBattleMs: 1_700_000_200_000,
			RaidEndMs:    1_700_000_300_000,
			RaidLevel:    pogo.RaidLevel_RAID_LEVEL_3,
			RaidPokemon: &pogo.PokemonProto{
				PokemonId: pogo.HoloPokemonId_TYRANITAR,
				Cp:        3600,
				Move1:     pogo.HoloPokemonMove_BITE_FAST,
				Move2:     pogo.HoloPokemonMove_STONE_EDGE,
				PokemonDisplay: &pogo.PokemonDisplayProto{
					Form:                 pogo.PokemonDisplayProto_FORM_UNSET,
					Gender:               pogo.PokemonDisplayProto_MALE,
					Costume:              pogo.PokemonDisplayProto_UNSET,
					CurrentTempEvolution: pogo.HoloTemporaryEvolutionId_TEMP_EVOLUTION_UNSET,
				},
			},
		},
	}

	shimFort := pogoshim.AsPokemonFortProto(fort.ProtoReflect())

	gym := &Gym{}
	gym.updateGymFromFort(shimFort, cellId, timestampMs)

	if got, want := gym.Id, "GYM1"; got != want {
		t.Errorf("Id = %q, want %q", got, want)
	}
	if got, want := gym.TeamId.ValueOrZero(), int64(pogo.Team_TEAM_RED); got != want {
		t.Errorf("TeamId = %d, want %d", got, want)
	}
	if got, want := gym.GuardingPokemonId.ValueOrZero(), int64(pogo.HoloPokemonId_MACHAMP); got != want {
		t.Errorf("GuardingPokemonId = %d, want %d", got, want)
	}
	if got, want := gym.AvailableSlots.ValueOrZero(), int64(4); got != want {
		t.Errorf("AvailableSlots = %d, want %d", got, want)
	}
	if got, want := gym.TotalCp.ValueOrZero(), int64(6000); got != want {
		t.Errorf("TotalCp = %d, want %d", got, want)
	}
	if got, want := gym.RaidSeed.ValueOrZero(), int64(42); got != want {
		t.Errorf("RaidSeed = %d, want %d", got, want)
	}
	if got, want := gym.RaidSpawnTimestamp.ValueOrZero(), int64(1_700_000_100); got != want {
		t.Errorf("RaidSpawnTimestamp = %d, want %d", got, want)
	}
	if got, want := gym.RaidBattleTimestamp.ValueOrZero(), int64(1_700_000_200); got != want {
		t.Errorf("RaidBattleTimestamp = %d, want %d", got, want)
	}
	if got, want := gym.RaidEndTimestamp.ValueOrZero(), int64(1_700_000_300); got != want {
		t.Errorf("RaidEndTimestamp = %d, want %d", got, want)
	}
	if got, want := gym.RaidLevel.ValueOrZero(), int64(pogo.RaidLevel_RAID_LEVEL_3); got != want {
		t.Errorf("RaidLevel = %d, want %d", got, want)
	}
	if got, want := gym.RaidPokemonId.ValueOrZero(), int64(pogo.HoloPokemonId_TYRANITAR); got != want {
		t.Errorf("RaidPokemonId = %d, want %d", got, want)
	}
	if got, want := gym.RaidPokemonCp.ValueOrZero(), int64(3600); got != want {
		t.Errorf("RaidPokemonCp = %d, want %d", got, want)
	}
	if got, want := gym.RaidPokemonMove1.ValueOrZero(), int64(pogo.HoloPokemonMove_BITE_FAST); got != want {
		t.Errorf("RaidPokemonMove1 = %d, want %d", got, want)
	}
	if got, want := gym.RaidPokemonMove2.ValueOrZero(), int64(pogo.HoloPokemonMove_STONE_EDGE); got != want {
		t.Errorf("RaidPokemonMove2 = %d, want %d", got, want)
	}
	if got, want := gym.CellId.ValueOrZero(), int64(cellId); got != want {
		t.Errorf("CellId = %d, want %d", got, want)
	}
	if got, want := gym.Lat, 12.3456789; got != want {
		t.Errorf("Lat = %v, want %v", got, want)
	}
	if got, want := gym.Lon, -67.891234; got != want {
		t.Errorf("Lon = %v, want %v", got, want)
	}
	if got, want := gym.Url.ValueOrZero(), "https://example.com/gym.png"; got != want {
		t.Errorf("Url = %q, want %q", got, want)
	}
}

// TestUpdatePokestopFromFortShim mirrors the gym test for the pokestop
// path: lure detection from the repeated active_fort_modifier scalar field,
// and the plural pokestop_displays repeated message field carrying incident
// ids through to UpdateFortBatch's per-incident loop.
func TestUpdatePokestopFromFortShim(t *testing.T) {
	const cellId = uint64(0x89c25a30)
	const lastModifiedS = int64(1_700_000_000)

	fort := &pogo.PokemonFortProto{
		FortId:             "STOP1",
		Latitude:           45.111111,
		Longitude:          -122.222222,
		FortType:           pogo.FortType_CHECKPOINT,
		LastModifiedMs:     lastModifiedS * 1000,
		ImageUrl:           "https://example.com/stop.png",
		IsArScanEligible:   true,
		ActiveFortModifier: []pogo.Item{pogo.Item_ITEM_TROY_DISK}, // 501: lure
		PokestopDisplays: []*pogo.PokestopIncidentDisplayProto{
			{
				IncidentId:      "incident-abc",
				IncidentStartMs: 1_700_000_000_000,
			},
			{
				IncidentId:      "incident-def",
				IncidentStartMs: 1_700_000_100_000,
			},
		},
	}

	shimFort := pogoshim.AsPokemonFortProto(fort.ProtoReflect())

	stop := &Pokestop{}
	stop.updatePokestopFromFort(shimFort, cellId, lastModifiedS)

	if got, want := stop.Id, "STOP1"; got != want {
		t.Errorf("Id = %q, want %q", got, want)
	}
	if got, want := stop.LureId, int16(pogo.Item_ITEM_TROY_DISK); got != want {
		t.Errorf("LureId = %d, want %d", got, want)
	}
	if got, want := stop.LureExpireTimestamp.ValueOrZero(), lastModifiedS+LureTime; got != want {
		t.Errorf("LureExpireTimestamp = %d, want %d", got, want)
	}
	if got, want := stop.CellId.ValueOrZero(), int64(cellId); got != want {
		t.Errorf("CellId = %d, want %d", got, want)
	}
	if got, want := stop.ArScanEligible.ValueOrZero(), int64(1); got != want {
		t.Errorf("ArScanEligible = %d, want %d", got, want)
	}
	if got, want := stop.Url.ValueOrZero(), "https://example.com/stop.png"; got != want {
		t.Errorf("Url = %q, want %q", got, want)
	}

	// The incident-display list wrap is what UpdateFortBatch iterates to
	// build incidents; verify it survives the wrap with correct ids/order.
	displays := shimFort.GetPokestopDisplays()
	if got, want := displays.Len(), 2; got != want {
		t.Fatalf("PokestopDisplays len = %d, want %d", got, want)
	}
	if got, want := displays.At(0).GetIncidentId(), "incident-abc"; got != want {
		t.Errorf("PokestopDisplays[0].IncidentId = %q, want %q", got, want)
	}
	if got, want := displays.At(1).GetIncidentId(), "incident-def"; got != want {
		t.Errorf("PokestopDisplays[1].IncidentId = %q, want %q", got, want)
	}
}

// TestUpdateFromPokestopIncidentDisplayOneofShim exercises the
// character_display oneof member of PokestopIncidentDisplayProto through
// updateFromPokestopIncidentDisplay. The task brief for this wave flagged
// oneof accessors as a risk ("the generator does NOT emit oneof accessors"),
// but pogoshimgen's message() function iterates MessageDescriptor.Fields()
// without special-casing oneof membership, so GetCharacterDisplay /
// HasCharacterDisplay are already generated using the same Has+Get+IsValid
// idiom as any other message-kind field - protoreflect.Message.Get on a
// oneof member that is unset, or set to a *different* member, returns an
// invalid/zero value exactly like an absent optional message field. This
// test proves that for both the std and hyperpb wraps, so no hand-written
// pogoshim/manual.go accessor is needed for this oneof.
func TestUpdateFromPokestopIncidentDisplayOneofShim(t *testing.T) {
	characterSet := &pogo.PokestopIncidentDisplayProto{
		IncidentId:           "incident-character",
		IncidentStartMs:      1_700_000_000_000,
		IncidentExpirationMs: 1_700_000_600_000,
		IncidentDisplayType:  pogo.IncidentDisplayType_INCIDENT_DISPLAY_TYPE_INVASION_GRUNT,
		MapDisplay: &pogo.PokestopIncidentDisplayProto_CharacterDisplay{
			CharacterDisplay: &pogo.CharacterDisplayProto{
				Style:     pogo.EnumWrapper_POKESTOP_ROCKET_INVASION,
				Character: pogo.EnumWrapper_CHARACTER_GRUNT_MALE,
			},
		},
	}
	// A different oneof member set (contest_display): GetCharacterDisplay
	// must come back zero/invalid rather than resolving to CharacterDisplay
	// state from a completely different arm.
	otherArmSet := &pogo.PokestopIncidentDisplayProto{
		IncidentId: "incident-contest",
		MapDisplay: &pogo.PokestopIncidentDisplayProto_ContestDisplay{
			ContestDisplay: &pogo.ContestDisplayProto{},
		},
	}

	checkCharacterSet := func(name string, shim pogoshim.PokestopIncidentDisplayProto) {
		incident := &Incident{}
		incident.updateFromPokestopIncidentDisplay(shim)
		if got, want := incident.Id, "incident-character"; got != want {
			t.Errorf("%s: Id = %q, want %q", name, got, want)
		}
		if got, want := incident.StartTime, int64(1_700_000_000); got != want {
			t.Errorf("%s: StartTime = %d, want %d", name, got, want)
		}
		if got, want := incident.ExpirationTime, int64(1_700_000_600); got != want {
			t.Errorf("%s: ExpirationTime = %d, want %d", name, got, want)
		}
		if got, want := incident.Style, int16(pogo.EnumWrapper_POKESTOP_ROCKET_INVASION); got != want {
			t.Errorf("%s: Style = %d, want %d", name, got, want)
		}
		if got, want := incident.Character, int16(pogo.EnumWrapper_CHARACTER_GRUNT_MALE); got != want {
			t.Errorf("%s: Character = %d, want %d", name, got, want)
		}
	}
	checkOtherArmSet := func(name string, shim pogoshim.PokestopIncidentDisplayProto) {
		if shim.HasCharacterDisplay() {
			t.Fatalf("%s: HasCharacterDisplay should be false when contest_display is set", name)
		}
		incident := &Incident{}
		incident.updateFromPokestopIncidentDisplay(shim)
		if incident.Style != 0 || incident.Character != 0 {
			t.Fatalf("%s: expected zero Style/Character when a different oneof member is set, got style=%d character=%d",
				name, incident.Style, incident.Character)
		}
	}

	// std wrap
	checkCharacterSet("std", pogoshim.AsPokestopIncidentDisplayProto(characterSet.ProtoReflect()))
	checkOtherArmSet("std", pogoshim.AsPokestopIncidentDisplayProto(otherArmSet.ProtoReflect()))

	// hyperpb wrap. Each parse needs its own Shared: a Shared's arena marks
	// itself in-use for the lifetime of one message tree, so reusing one
	// across two independent Unmarshal calls panics ("in-use Context").
	ty := hyperpb.CompileMessageDescriptor((*pogo.PokestopIncidentDisplayProto)(nil).ProtoReflect().Descriptor())

	raw1, err := proto.Marshal(characterSet)
	if err != nil {
		t.Fatal(err)
	}
	shared1 := new(hyperpb.Shared)
	defer shared1.Free()
	msg1 := shared1.NewMessage(ty)
	if err := msg1.Unmarshal(raw1); err != nil {
		t.Fatal(err)
	}
	checkCharacterSet("hyperpb", pogoshim.AsPokestopIncidentDisplayProto(msg1.ProtoReflect()))

	raw2, err := proto.Marshal(otherArmSet)
	if err != nil {
		t.Fatal(err)
	}
	shared2 := new(hyperpb.Shared)
	defer shared2.Free()
	msg2 := shared2.NewMessage(ty)
	if err := msg2.Unmarshal(raw2); err != nil {
		t.Fatal(err)
	}
	checkOtherArmSet("hyperpb", pogoshim.AsPokestopIncidentDisplayProto(msg2.ProtoReflect()))
}
