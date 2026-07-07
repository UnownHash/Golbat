package decoder

import (
	"context"
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/db"
	"golbat/pogo"
	"golbat/pogoshim"
)

// hyperpbWrapTappableOut/hyperpbWrapTappableReq mirror the established
// hyperpbWrap<Root> convention (routes_shim_test.go, quest_shim_test.go):
// marshal in, parse through a fresh hyperpb Shared, return the shim + the
// Shared the caller must Free once done.
func hyperpbWrapTappableOut(t *testing.T, in *pogo.ProcessTappableOutProto) (pogoshim.ProcessTappableOutProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.ProcessTappableOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsProcessTappableOutProto(msg.ProtoReflect()), shared
}

func hyperpbWrapTappableReq(t *testing.T, in *pogo.ProcessTappableProto) (pogoshim.ProcessTappableProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.ProcessTappableProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsProcessTappableProto(msg.ProtoReflect()), shared
}

// TestUpdateFromProcessTappableProto_ItemReward locks in Wave 3 Task 4
// behavior for the tappable reward path: LootItemProto's 14-way "Type"
// oneof (item/stardust/pokecoin/...) is read through the generator's new
// Has<Field>() accessors (cmd/pogoshimgen/main.go was taught to emit these
// for scalar/enum oneof members, not just message ones -- see that file's
// comment) in the same order as the pre-shim type switch. Uses a
// FortId-based location (not SpawnpointId) so setExpireTimestamp never
// touches the spawnpoint cache/DB.
func TestUpdateFromProcessTappableProto_ItemReward(t *testing.T) {
	build := func() (*pogo.ProcessTappableOutProto, *pogo.ProcessTappableProto) {
		out := &pogo.ProcessTappableOutProto{
			Status: pogo.ProcessTappableOutProto_SUCCESS,
			Reward: []*pogo.LootProto{{
				LootItem: []*pogo.LootItemProto{
					{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_POKE_BALL}, Count: 3},
				},
			}},
		}
		req := &pogo.ProcessTappableProto{
			Location:        &pogo.TappableLocation{LocationId: &pogo.TappableLocation_FortId{FortId: "FORT1"}},
			TappableTypeId:  "tappable_type_1",
			EncounterId:     999,
			LocationHintLat: 12.5,
			LocationHintLng: -45.5,
		}
		return out, req
	}

	check := func(name string, out pogoshim.ProcessTappableOutProto, req pogoshim.ProcessTappableProto) {
		ta := &Tappable{}
		ta.updateFromProcessTappableProto(context.Background(), db.DbDetails{}, out, req, 1_700_000_000_000)

		if ta.Id != 999 {
			t.Errorf("%s: Id = %d, want 999", name, ta.Id)
		}
		if !ta.FortId.Valid || ta.FortId.String != "FORT1" {
			t.Errorf("%s: FortId = %+v, want FORT1", name, ta.FortId)
		}
		if ta.SpawnId.Valid {
			t.Errorf("%s: SpawnId should be unset for a fort-based location, got %+v", name, ta.SpawnId)
		}
		if ta.Type != "tappable_type_1" {
			t.Errorf("%s: Type = %q", name, ta.Type)
		}
		if ta.Lat != 12.5 || ta.Lon != -45.5 {
			t.Errorf("%s: Lat/Lon = %v/%v", name, ta.Lat, ta.Lon)
		}
		if ta.Encounter.Valid {
			t.Errorf("%s: Encounter should be unset for a reward (non-encounter) tappable, got %+v", name, ta.Encounter)
		}
		if !ta.ItemId.Valid || ta.ItemId.Int64 != int64(pogo.Item_ITEM_POKE_BALL) {
			t.Errorf("%s: ItemId = %+v, want %d", name, ta.ItemId, pogo.Item_ITEM_POKE_BALL)
		}
		if !ta.Count.Valid || ta.Count.Int64 != 3 {
			t.Errorf("%s: Count = %+v, want 3", name, ta.Count)
		}
	}

	stdOut, stdReq := build()
	check("std", pogoshim.AsProcessTappableOutProto(stdOut.ProtoReflect()), pogoshim.AsProcessTappableProto(stdReq.ProtoReflect()))

	hyperOut, hyperReq := build()
	outShim, outShared := hyperpbWrapTappableOut(t, hyperOut)
	defer outShared.Free()
	reqShim, reqShared := hyperpbWrapTappableReq(t, hyperReq)
	defer reqShared.Free()
	check("hyperpb", outShim, reqShim)
}

// TestUpdateFromProcessTappableProto_OtherRewardTypes exercises every
// non-Item Type oneof member once, proving Has<Field>() correctly
// disambiguates all 14 (no false match on a sibling member, and no reward
// columns set for any of them) across both engines. This is exactly the
// oneof shape that motivated the generator fix: without per-field Has(),
// GetItem()==0 could not be told apart from "some other Type member set".
func TestUpdateFromProcessTappableProto_OtherRewardTypes(t *testing.T) {
	variants := []struct {
		name string
		item *pogo.LootItemProto
	}{
		{"stardust", &pogo.LootItemProto{Type: &pogo.LootItemProto_Stardust{Stardust: true}}},
		{"pokecoin", &pogo.LootItemProto{Type: &pogo.LootItemProto_Pokecoin{Pokecoin: true}}},
		{"pokemon_candy", &pogo.LootItemProto{Type: &pogo.LootItemProto_PokemonCandy{PokemonCandy: pogo.HoloPokemonId_BULBASAUR}}},
		{"experience", &pogo.LootItemProto{Type: &pogo.LootItemProto_Experience{Experience: true}}},
		{"pokemon_egg", &pogo.LootItemProto{Type: &pogo.LootItemProto_PokemonEgg{PokemonEgg: &pogo.PokemonProto{Id: 1}}}},
		{"avatar_template_id", &pogo.LootItemProto{Type: &pogo.LootItemProto_AvatarTemplateId{AvatarTemplateId: "AV1"}}},
		{"sticker_id", &pogo.LootItemProto{Type: &pogo.LootItemProto_StickerId{StickerId: "STICK1"}}},
		{"mega_energy_pokemon_id", &pogo.LootItemProto{Type: &pogo.LootItemProto_MegaEnergyPokemonId{MegaEnergyPokemonId: pogo.HoloPokemonId_CHARIZARD}}},
		{"xl_candy", &pogo.LootItemProto{Type: &pogo.LootItemProto_XlCandy{XlCandy: pogo.HoloPokemonId_SQUIRTLE}}},
		{"follower_pokemon", &pogo.LootItemProto{Type: &pogo.LootItemProto_FollowerPokemon{FollowerPokemon: &pogo.FollowerPokemonProto{}}}},
		{"neutral_avatar_template_id", &pogo.LootItemProto{Type: &pogo.LootItemProto_NeutralAvatarTemplateId{NeutralAvatarTemplateId: "NAT1"}}},
		{"neutral_avatar_item_template", &pogo.LootItemProto{Type: &pogo.LootItemProto_NeutralAvatarItemTemplate{NeutralAvatarItemTemplate: &pogo.NeutralAvatarLootItemTemplateProto{}}}},
		{"neutral_avatar_item_display", &pogo.LootItemProto{Type: &pogo.LootItemProto_NeutralAvatarItemDisplay{NeutralAvatarItemDisplay: &pogo.NeutralAvatarLootItemDisplayProto{}}}},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			build := func() (*pogo.ProcessTappableOutProto, *pogo.ProcessTappableProto) {
				return &pogo.ProcessTappableOutProto{
						Status: pogo.ProcessTappableOutProto_SUCCESS,
						Reward: []*pogo.LootProto{{LootItem: []*pogo.LootItemProto{v.item}}},
					}, &pogo.ProcessTappableProto{
						Location:    &pogo.TappableLocation{LocationId: &pogo.TappableLocation_FortId{FortId: "FORT1"}},
						EncounterId: 1,
					}
			}

			check := func(name string, out pogoshim.ProcessTappableOutProto, req pogoshim.ProcessTappableProto) {
				ta := &Tappable{}
				ta.updateFromProcessTappableProto(context.Background(), db.DbDetails{}, out, req, 1000)
				if ta.ItemId.Valid || ta.Count.Valid {
					t.Errorf("%s: expected no item/count set for %s reward, got ItemId=%+v Count=%+v", name, v.name, ta.ItemId, ta.Count)
				}
			}

			stdOut, stdReq := build()
			check("std", pogoshim.AsProcessTappableOutProto(stdOut.ProtoReflect()), pogoshim.AsProcessTappableProto(stdReq.ProtoReflect()))

			hyperOut, hyperReq := build()
			outShim, outShared := hyperpbWrapTappableOut(t, hyperOut)
			defer outShared.Free()
			reqShim, reqShared := hyperpbWrapTappableReq(t, hyperReq)
			defer reqShared.Free()
			check("hyperpb", outShim, reqShim)
		})
	}
}

// TestUpdatePokemonFromTappableEncounterProtoShim covers the pokemon side of
// the tappable encounter chain: setPokemonDisplay/addEncounterPokemon now
// read straight off encounterData.GetPokemon() (a pogoshim.PokemonProto)
// instead of the pre-shim bridge
// pogoshim.AsPokemonDisplayProto(encounterData.Pokemon.PokemonDisplay.ProtoReflect())
// / pogoshim.AsPokemonProto(encounterData.Pokemon.ProtoReflect()) -- the
// typed-nil landmine documented in progress.md. Uses a FortId-based location
// (SeenType_TappableLureEncounter path) so no spawnpoint cache/DB touch is
// needed.
func TestUpdatePokemonFromTappableEncounterProtoShim(t *testing.T) {
	const encounterId = uint64(555)

	build := func() (*pogo.ProcessTappableProto, *pogo.TappableEncounterProto) {
		req := &pogo.ProcessTappableProto{
			Location:        &pogo.TappableLocation{LocationId: &pogo.TappableLocation_FortId{FortId: "FORT9"}},
			EncounterId:     encounterId,
			LocationHintLat: 1.1,
			LocationHintLng: 2.2,
		}
		enc := &pogo.TappableEncounterProto{
			Result: pogo.TappableEncounterProto_TAPPABLE_ENCOUNTER_SUCCESS,
			Pokemon: &pogo.PokemonProto{
				Id:        encounterId,
				PokemonId: pogo.HoloPokemonId_PIKACHU,
				Cp:        500,
				PokemonDisplay: &pogo.PokemonDisplayProto{
					Form:  pogo.PokemonDisplayProto_FORM_UNSET,
					Shiny: true,
				},
			},
		}
		return req, enc
	}

	check := func(name string, req pogoshim.ProcessTappableProto, enc pogoshim.TappableEncounterProto) {
		pokemon := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encounterId)}}
		pokemon.newRecord = true

		pokemon.updatePokemonFromTappableEncounterProto(context.Background(), db.DbDetails{}, req, enc, "misty", 1_700_000_000_000)

		if pokemon.Lat != 1.1 || pokemon.Lon != 2.2 {
			t.Errorf("%s: Lat/Lon = %v/%v", name, pokemon.Lat, pokemon.Lon)
		}
		if !pokemon.PokestopId.Valid || pokemon.PokestopId.String != "FORT9" {
			t.Errorf("%s: PokestopId = %+v, want FORT9", name, pokemon.PokestopId)
		}
		if pokemon.SeenType.ValueOrZero() != SeenType_TappableLureEncounter {
			t.Errorf("%s: SeenType = %q, want %q", name, pokemon.SeenType.ValueOrZero(), SeenType_TappableLureEncounter)
		}
		if !pokemon.Username.Valid || pokemon.Username.String != "misty" {
			t.Errorf("%s: Username = %+v, want misty", name, pokemon.Username)
		}
		if pokemon.PokemonId != int16(pogo.HoloPokemonId_PIKACHU) {
			t.Errorf("%s: PokemonId = %d, want %d", name, pokemon.PokemonId, pogo.HoloPokemonId_PIKACHU)
		}
		if !pokemon.Cp.Valid || pokemon.Cp.Int64 != 500 {
			t.Errorf("%s: Cp = %+v, want 500", name, pokemon.Cp)
		}
		if !pokemon.Shiny.Valid || !pokemon.Shiny.Bool {
			t.Errorf("%s: Shiny = %+v, want true", name, pokemon.Shiny)
		}
	}

	stdReq, stdEnc := build()
	check("std", pogoshim.AsProcessTappableProto(stdReq.ProtoReflect()), pogoshim.AsTappableEncounterProto(stdEnc.ProtoReflect()))

	hyperReq, hyperEnc := build()
	reqShim, reqShared := hyperpbWrapTappableReq(t, hyperReq)
	defer reqShared.Free()
	raw, err := proto.Marshal(hyperEnc)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.TappableEncounterProto)(nil).ProtoReflect().Descriptor())
	encShared := new(hyperpb.Shared)
	msg := encShared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		encShared.Free()
		t.Fatal(err)
	}
	defer encShared.Free()
	check("hyperpb", reqShim, pogoshim.AsTappableEncounterProto(msg.ProtoReflect()))
}
