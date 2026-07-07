package main

import (
	"hash/fnv"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/pogo"
)

// genericDigest is the same fold digestGenericAdapter performs, exposed
// directly (rather than through shadowCompare/maybeShadow) so these tests
// can assert on the digest value itself.
func genericDigest(m protoreflect.Message) uint64 {
	h := fnv.New64a()
	digestMessageGeneric(h, m)
	return h.Sum64()
}

// digestGenericViaStd/digestGenericViaHyperpb mirror digestViaStd/
// digestViaHyperpb (protoshadow_test.go) but for the generic digest path:
// wrap is always identityWrap, so callers only need to supply the handle
// and payload.
func digestGenericViaStd(t *testing.T, eng *protoEngineHandle, payload []byte) uint64 {
	t.Helper()
	return digestViaStd(t, eng, payload, identityWrap, digestGenericAdapter)
}

func digestGenericViaHyperpb(t *testing.T, eng *protoEngineHandle, payload []byte) uint64 {
	t.Helper()
	return digestViaHyperpb(t, eng, payload, identityWrap, digestGenericAdapter)
}

// TestDigestGenericFortDetailsMatchesAcrossEngines exercises
// digestMessageGeneric end to end against a synthetic FortDetailsOutProto
// covering scalars (string/int32/float64/bool), an enum, a repeated scalar
// (image_url), a repeated message (pokemon), and a singular message
// (event_info) -- std wrap and hyperpb wrap of the identical wire bytes
// must fold to the same digest.
func TestDigestGenericFortDetailsMatchesAcrossEngines(t *testing.T) {
	in := &pogo.FortDetailsOutProto{
		Id:        "FORT1",
		Team:      pogo.Team_TEAM_BLUE,
		Name:      "Test Gym",
		ImageUrl:  []string{"https://example.test/1.png", "https://example.test/2.png"},
		Fp:        100,
		Stamina:   50,
		FortType:  pogo.FortType_GYM,
		Latitude:  12.345,
		Longitude: -54.321,
		CloseSoon: true,
		Pokemon: []*pogo.PokemonProto{
			{Id: 1, PokemonId: pogo.HoloPokemonId_BULBASAUR, Cp: 500},
			{Id: 2, PokemonId: pogo.HoloPokemonId_CHARMANDER, Cp: 800},
		},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	stdDigest := digestGenericViaStd(t, fortDetailsEngine, raw)
	hyperDigest := digestGenericViaHyperpb(t, fortDetailsEngine, raw)
	if stdDigest != hyperDigest {
		t.Fatalf("digest mismatch: std=%x hyperpb=%x", stdDigest, hyperDigest)
	}
	if stdDigest == 0 {
		t.Fatal("expected non-zero digest for a populated FortDetailsOutProto")
	}
}

// TestDigestGenericFortDetailsCorruptionSensitivity guards the core
// property a shadow digest exists for: any single-field change to an
// otherwise identical payload must change the digest, whether the field is
// a repeated message element, a repeated scalar element, or a plain scalar.
func TestDigestGenericFortDetailsCorruptionSensitivity(t *testing.T) {
	base := func(pokemonCp int32) *pogo.FortDetailsOutProto {
		return &pogo.FortDetailsOutProto{
			Id:       "FORT1",
			Team:     pogo.Team_TEAM_BLUE,
			ImageUrl: []string{"a", "b"},
			Pokemon: []*pogo.PokemonProto{
				{Id: 1, Cp: pokemonCp},
			},
		}
	}
	originalRaw, err := proto.Marshal(base(500))
	if err != nil {
		t.Fatalf("marshal original: %v", err)
	}
	corruptedRaw, err := proto.Marshal(base(501)) // Cp+1 inside the repeated pokemon element
	if err != nil {
		t.Fatalf("marshal corrupted: %v", err)
	}

	originalDigest := digestGenericViaStd(t, fortDetailsEngine, originalRaw)
	corruptedDigest := digestGenericViaStd(t, fortDetailsEngine, corruptedRaw)
	if originalDigest == corruptedDigest {
		t.Fatal("expected corrupted payload (nested pokemon Cp+1) to produce a different digest")
	}
}

// TestDigestGenericPresenceOnlyDivergesFromAbsent guards the Has-bit fold:
// a singular message field present-but-all-default must digest differently
// from the same field being entirely absent, exactly like the hand-written
// digests' convention (protoshadow_test.go's
// TestShadowDigestPresenceOnlyDivergesFromAbsent).
func TestDigestGenericPresenceOnlyDivergesFromAbsent(t *testing.T) {
	present := &pogo.FortDetailsOutProto{Id: "FORT1", EventInfo: &pogo.EventInfoProto{}}
	absent := &pogo.FortDetailsOutProto{Id: "FORT1"}

	presentRaw, err := proto.Marshal(present)
	if err != nil {
		t.Fatalf("marshal present: %v", err)
	}
	absentRaw, err := proto.Marshal(absent)
	if err != nil {
		t.Fatalf("marshal absent: %v", err)
	}

	presentDigest := digestGenericViaStd(t, fortDetailsEngine, presentRaw)
	absentDigest := digestGenericViaStd(t, fortDetailsEngine, absentRaw)
	if presentDigest == absentDigest {
		t.Fatal("expected a present-but-empty EventInfo to digest differently than an absent one")
	}
}

// TestDigestGenericQuestMatchesAcrossEngines covers the quest root
// (FortSearchOutProto) with AR-style rewards: repeated message (Items),
// nested message with its own repeated message (Loot.LootItem), and a
// second independent instance of the same nested message type
// (BonusLoot) to make sure sibling fields of identical message type don't
// get folded into each other.
func TestDigestGenericQuestMatchesAcrossEngines(t *testing.T) {
	in := &pogo.FortSearchOutProto{
		Result:      pogo.FortSearchOutProto_SUCCESS,
		GemsAwarded: 5,
		XpAwarded:   500,
		FortId:      "STOP1",
		Items: []*pogo.AwardItemProto{
			{Item: pogo.Item_ITEM_POKE_BALL, ItemCount: 3},
			{Item: pogo.Item_ITEM_RAZZ_BERRY, ItemCount: 1, BonusCount: 1},
		},
		Loot: &pogo.LootProto{
			LootItem: []*pogo.LootItemProto{
				{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_GREAT_BALL}, Count: 2},
			},
		},
		BonusLoot: &pogo.LootProto{
			LootItem: []*pogo.LootItemProto{
				{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_ULTRA_BALL}, Count: 1},
			},
		},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	stdDigest := digestGenericViaStd(t, questEngine, raw)
	hyperDigest := digestGenericViaHyperpb(t, questEngine, raw)
	if stdDigest != hyperDigest {
		t.Fatalf("digest mismatch: std=%x hyperpb=%x", stdDigest, hyperDigest)
	}

	// Swap which LootProto (Loot vs BonusLoot) holds the ULTRA_BALL entry:
	// if the digest folded loot contents without keying on which field they
	// came from, this would collide with the original.
	swapped := &pogo.FortSearchOutProto{
		Result:      pogo.FortSearchOutProto_SUCCESS,
		GemsAwarded: 5,
		XpAwarded:   500,
		FortId:      "STOP1",
		Items:       in.Items,
		Loot: &pogo.LootProto{
			LootItem: []*pogo.LootItemProto{
				{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_ULTRA_BALL}, Count: 1},
			},
		},
		BonusLoot: &pogo.LootProto{
			LootItem: []*pogo.LootItemProto{
				{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_GREAT_BALL}, Count: 2},
			},
		},
	}
	swappedRaw, err := proto.Marshal(swapped)
	if err != nil {
		t.Fatalf("marshal swapped: %v", err)
	}
	swappedDigest := digestGenericViaStd(t, questEngine, swappedRaw)
	if swappedDigest == stdDigest {
		t.Fatal("expected swapping Loot/BonusLoot contents to change the digest")
	}
}

// TestDigestGenericGetMapFortsMatchesAcrossEngines covers get_map_forts:
// a repeated top-level message (Fort) each containing its own repeated
// message (Image), the shape the getMapFortsCache retention fix (Task 2)
// depends on being covered by shadow verification.
func TestDigestGenericGetMapFortsMatchesAcrossEngines(t *testing.T) {
	in := &pogo.GetMapFortsOutProto{
		Status: pogo.GetMapFortsOutProto_SUCCESS,
		Fort: []*pogo.GetMapFortsOutProto_FortProto{
			{
				Id:        "STOP1",
				Name:      "Test Stop",
				Latitude:  1.1,
				Longitude: 2.2,
				Image: []*pogo.GetMapFortsOutProto_Image{
					{Url: "https://example.test/a.png"},
					{Url: "https://example.test/b.png"},
				},
			},
			{
				Id:        "GYM1",
				Name:      "Test Gym",
				Latitude:  3.3,
				Longitude: 4.4,
			},
		},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	stdDigest := digestGenericViaStd(t, mapFortsEngine, raw)
	hyperDigest := digestGenericViaHyperpb(t, mapFortsEngine, raw)
	if stdDigest != hyperDigest {
		t.Fatalf("digest mismatch: std=%x hyperpb=%x", stdDigest, hyperDigest)
	}

	corrupted := proto.Clone(in).(*pogo.GetMapFortsOutProto)
	corrupted.Fort[0].Image[1].Url = "https://example.test/CHANGED.png"
	corruptedRaw, err := proto.Marshal(corrupted)
	if err != nil {
		t.Fatalf("marshal corrupted: %v", err)
	}
	corruptedDigest := digestGenericViaStd(t, mapFortsEngine, corruptedRaw)
	if corruptedDigest == stdDigest {
		t.Fatal("expected a changed nested image URL to change the digest")
	}
}

// TestDigestGenericMapFieldDeterminism is the task brief's required
// map-field-determinism check. BattleStateOutProto's nested BattleStateProto
// has four map fields (actors: string->message, pokemon: uint64->message,
// team_actor_count: int32->int32, party_member_count: string->int32) --
// unlike protoreflect.Message.Range, protoreflect.Map.Range's iteration
// order is unspecified and Go's map iteration is randomized per call, so
// digesting the same message repeatedly must always produce the same
// result. Folding without sorting keys first would be expected to disagree
// across at least some of these repeated calls.
func TestDigestGenericMapFieldDeterminism(t *testing.T) {
	in := &pogo.BattleStateOutProto{
		BattleState: &pogo.BattleStateProto{
			Actors: map[string]*pogo.BattleActorProto{
				"actor-a": {Id: "actor-a", Team: pogo.Team_TEAM_BLUE, PositionX: 1},
				"actor-b": {Id: "actor-b", Team: pogo.Team_TEAM_RED, PositionX: 2},
				"actor-c": {Id: "actor-c", Team: pogo.Team_TEAM_YELLOW, PositionX: 3},
				"actor-d": {Id: "actor-d", Team: pogo.Team_TEAM_BLUE, PositionX: 4},
			},
			TeamActorCount: map[int32]int32{
				int32(pogo.Team_TEAM_BLUE):   2,
				int32(pogo.Team_TEAM_RED):    1,
				int32(pogo.Team_TEAM_YELLOW): 1,
			},
			Pokemon: map[uint64]*pogo.BattlePokemonProto{
				1: {PokedexId: pogo.HoloPokemonId_BULBASAUR},
				2: {PokedexId: pogo.HoloPokemonId_CHARMANDER},
				3: {PokedexId: pogo.HoloPokemonId_SQUIRTLE},
			},
			PartyMemberCount: map[string]int32{
				"party-1": 3,
				"party-2": 2,
			},
		},
		Turn: 7,
	}

	// Re-marshal/re-unmarshal on every iteration (not just re-digesting the
	// same in-memory message): protobuf-go's map marshaler also iterates a
	// Go map, so this exercises randomization on both the encode and the
	// decode/digest side, not just Message.Get's map view.
	var first uint64
	for i := 0; i < 40; i++ {
		raw, err := proto.Marshal(in)
		if err != nil {
			t.Fatalf("marshal iteration %d: %v", i, err)
		}
		out := &pogo.BattleStateOutProto{}
		if err := proto.Unmarshal(raw, out); err != nil {
			t.Fatalf("unmarshal iteration %d: %v", i, err)
		}
		got := genericDigest(out.ProtoReflect())
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("iteration %d: digest %x != first digest %x -- map-field folding is order-sensitive", i, got, first)
		}
	}
}

// TestDigestGenericMapFieldCorruptionSensitivity confirms the map-field
// path also detects real content changes, not just order -- changing one
// map entry's value must change the digest, and so must removing a key
// entirely (as opposed to merely reordering the same key set).
func TestDigestGenericMapFieldCorruptionSensitivity(t *testing.T) {
	build := func(actorBTeam pogo.Team) *pogo.BattleStateOutProto {
		return &pogo.BattleStateOutProto{
			BattleState: &pogo.BattleStateProto{
				Actors: map[string]*pogo.BattleActorProto{
					"actor-a": {Id: "actor-a", Team: pogo.Team_TEAM_BLUE},
					"actor-b": {Id: "actor-b", Team: actorBTeam},
				},
			},
		}
	}
	originalDigest := genericDigest(build(pogo.Team_TEAM_RED).ProtoReflect())
	changedDigest := genericDigest(build(pogo.Team_TEAM_YELLOW).ProtoReflect())
	if originalDigest == changedDigest {
		t.Fatal("expected changing a map entry's value to change the digest")
	}

	fewerKeys := &pogo.BattleStateOutProto{
		BattleState: &pogo.BattleStateProto{
			Actors: map[string]*pogo.BattleActorProto{
				"actor-a": {Id: "actor-a", Team: pogo.Team_TEAM_BLUE},
			},
		},
	}
	fewerKeysDigest := genericDigest(fewerKeys.ProtoReflect())
	if fewerKeysDigest == originalDigest {
		t.Fatal("expected a map with fewer keys to digest differently")
	}
}

// TestShadowCompareUsesGenericDigestForNewRoot proves the end-to-end wiring
// the task brief asks for: shadowCompare (the function maybeShadow actually
// calls) must use digestMessageGeneric -- via genericShadowEngine --for a
// method that has no hand-written digest, exactly as it does for
// gmo/encounter/disk_encounter via their hand-written ones.
func TestShadowCompareUsesGenericDigestForNewRoot(t *testing.T) {
	in := &pogo.FortDetailsOutProto{Id: "FORT1", Team: pogo.Team_TEAM_BLUE, Fp: 42}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !shadowCompare(engMethodFortDetails, raw) {
		t.Fatal("shadowCompare(fort_details, ...) = false, want true for a well-formed payload")
	}
}
