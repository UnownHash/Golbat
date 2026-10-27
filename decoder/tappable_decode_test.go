package decoder

import (
	"context"
	"testing"

	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/pogo"
)

func decodeTappableLoot(t *testing.T, ta *Tappable, items ...*pogo.LootItemProto) {
	t.Helper()
	out := &pogo.ProcessTappableOutProto{
		Reward: []*pogo.LootProto{{LootItem: items}},
	}
	req := &pogo.ProcessTappableProto{EncounterId: 42, TappableTypeId: "TAPPABLE_TYPE_BREAKFAST"}
	ta.updateFromProcessTappableProto(context.Background(), db.DbDetails{}, out, req, 1700000000000)
}

func TestTappableDecodeRewards(t *testing.T) {
	for name, tc := range map[string]struct {
		item                     *pogo.LootItemProto
		rewardType               null.String
		itemId, pokemonId, count null.Int
	}{
		// "item" and "candy" match payloads seen in traffic
		"item": {
			item:       &pogo.LootItemProto{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_RARE_CANDY}, Count: 1},
			rewardType: null.StringFrom("item"),
			itemId:     null.IntFrom(int64(pogo.Item_ITEM_RARE_CANDY)),
			count:      null.IntFrom(1),
		},
		"candy": {
			item:       &pogo.LootItemProto{Type: &pogo.LootItemProto_PokemonCandy{PokemonCandy: pogo.HoloPokemonId_PIKACHU}, Count: 6},
			rewardType: null.StringFrom("pokemon_candy"),
			pokemonId:  null.IntFrom(int64(pogo.HoloPokemonId_PIKACHU)),
			count:      null.IntFrom(6),
		},
		"xl candy": {
			item:       &pogo.LootItemProto{Type: &pogo.LootItemProto_XlCandy{XlCandy: pogo.HoloPokemonId_PIKACHU}, Count: 3},
			rewardType: null.StringFrom("xl_candy"),
			pokemonId:  null.IntFrom(int64(pogo.HoloPokemonId_PIKACHU)),
			count:      null.IntFrom(3),
		},
		"mega energy": {
			item:       &pogo.LootItemProto{Type: &pogo.LootItemProto_MegaEnergyPokemonId{MegaEnergyPokemonId: pogo.HoloPokemonId_PIKACHU}, Count: 3},
			rewardType: null.StringFrom("mega_energy_pokemon_id"),
			pokemonId:  null.IntFrom(int64(pogo.HoloPokemonId_PIKACHU)),
			count:      null.IntFrom(3),
		},
		"temp evo branch energy": {
			item: &pogo.LootItemProto{Type: &pogo.LootItemProto_TempEvoPokemonBranch{TempEvoPokemonBranch: &pogo.TempEvoPokemonBranch{
				PokedexId: pogo.HoloPokemonId_CHARIZARD,
				TempEvoId: pogo.HoloTemporaryEvolutionId_TEMP_EVOLUTION_MEGA_X,
			}}, Count: 3},
			rewardType: null.StringFrom("temp_evo_pokemon_branch"),
			pokemonId:  null.IntFrom(int64(pogo.HoloPokemonId_CHARIZARD)),
			count:      null.IntFrom(3),
		},
		"sticker": {
			item:       &pogo.LootItemProto{Type: &pogo.LootItemProto_StickerId{StickerId: "sticker_1"}, Count: 2},
			rewardType: null.StringFrom("sticker_id"),
			count:      null.IntFrom(2),
		},
		"stardust": {
			item:       &pogo.LootItemProto{Type: &pogo.LootItemProto_Stardust{Stardust: true}, Count: 3},
			rewardType: null.StringFrom("stardust"),
			count:      null.IntFrom(3),
		},
	} {
		t.Run(name, func(t *testing.T) {
			ta := &Tappable{}
			decodeTappableLoot(t, ta, tc.item)
			if ta.RewardType != tc.rewardType || ta.ItemId != tc.itemId || ta.RewardPokemonId != tc.pokemonId {
				t.Errorf("got type=%v item=%v pokemon=%v, want type=%v item=%v pokemon=%v",
					ta.RewardType, ta.ItemId, ta.RewardPokemonId, tc.rewardType, tc.itemId, tc.pokemonId)
			}
			if ta.Count != tc.count {
				t.Errorf("count = %v, want %v", ta.Count, tc.count)
			}
		})
	}
}

func TestTappableDecodeRewardChangeClearsStaleFields(t *testing.T) {
	ta := &Tappable{}
	decodeTappableLoot(t, ta, &pogo.LootItemProto{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_POKE_BALL}, Count: 1})
	decodeTappableLoot(t, ta, &pogo.LootItemProto{Type: &pogo.LootItemProto_PokemonCandy{PokemonCandy: pogo.HoloPokemonId_PIKACHU}, Count: 2})
	if ta.ItemId.Valid {
		t.Errorf("item_id = %v, want null after switch to candy", ta.ItemId)
	}
	if ta.RewardPokemonId != null.IntFrom(int64(pogo.HoloPokemonId_PIKACHU)) {
		t.Errorf("reward_pokemon_id = %v, want pikachu", ta.RewardPokemonId)
	}
}

// TestTappableRewardTypeNamesPinned pins the stored reward_type strings to
// their LootItemProto field numbers. Names come from the proto distributor,
// not the wire format; a rename changes the DB/API values. Fails on a proto
// swap that renames a field, so consumers can be warned or a mapping added.
func TestTappableRewardTypeNamesPinned(t *testing.T) {
	want := map[int32]string{
		1:  "item",
		2:  "stardust",
		3:  "pokecoin",
		4:  "pokemon_candy",
		6:  "experience",
		7:  "pokemon_egg",
		8:  "avatar_template_id",
		9:  "sticker_id",
		10: "mega_energy_pokemon_id",
		11: "xl_candy",
		12: "follower_pokemon",
		13: "neutral_avatar_template_id",
		14: "neutral_avatar_item_template",
		15: "neutral_avatar_item_display",
		16: "temp_evo_pokemon_branch",
	}
	fields := lootItemTypeOneof.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		name, ok := want[int32(f.Number())]
		if !ok {
			t.Errorf("new LootItemProto reward field %d %q: add it here", f.Number(), f.Name())
			continue
		}
		if string(f.Name()) != name {
			t.Errorf("LootItemProto field %d renamed %q -> %q: stored reward_type values change", f.Number(), name, f.Name())
		}
	}
}
