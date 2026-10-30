package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
)

func hasReward(a *ApiTappableAvailable, rewardType string, itemId, rewardPokemonId *int16) bool {
	for _, r := range a.Rewards {
		if r.RewardType == rewardType && eqInt16Ptr(r.ItemId, itemId) && eqInt16Ptr(r.RewardPokemonId, rewardPokemonId) {
			return true
		}
	}
	return false
}

func hasEncounter(a *ApiTappableAvailable, pokemonId int16) bool {
	for _, e := range a.Encounters {
		if e.PokemonId == pokemonId {
			return true
		}
	}
	return false
}

func eqInt16Ptr(a, b *int16) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func i16(v int16) *int16 { return &v }

func TestTappableAvailabilityObserveRead(t *testing.T) {
	now := time.Now().Unix()
	observeTappable(TappableLookup{RewardType: 0, ItemId: 7001, RewardPokemonId: -1, PokemonId: -1, ExpireTimestamp: uint32(now + 300)}, now)
	observeTappable(TappableLookup{RewardType: 3, ItemId: -1, RewardPokemonId: 7002, PokemonId: -1, ExpireTimestamp: uint32(now + 300)}, now)
	observeTappable(TappableLookup{RewardType: 1, ItemId: -1, RewardPokemonId: -1, PokemonId: -1, ExpireTimestamp: uint32(now + 300)}, now)
	observeTappable(TappableLookup{RewardType: -1, ItemId: -1, RewardPokemonId: -1, PokemonId: 7003, ExpireTimestamp: uint32(now + 300)}, now)

	a := GetAvailableTappables(now)
	if !hasReward(a, "item", i16(7001), nil) || !hasReward(a, "pokemon_candy", nil, i16(7002)) || !hasReward(a, "stardust", nil, nil) {
		t.Errorf("rewards = %+v, want item/7001, pokemon_candy/7002, stardust", a.Rewards)
	}
	if !hasEncounter(a, 7003) {
		t.Errorf("encounters = %+v, want 7003", a.Encounters)
	}
	for _, r := range a.Rewards {
		if r.RewardType == "" {
			t.Errorf("encounter leaked into rewards: %+v", r)
		}
	}
}

func TestTappableAvailabilityExpiry(t *testing.T) {
	now := time.Now().Unix()
	observeTappable(TappableLookup{RewardType: 0, ItemId: 7011, RewardPokemonId: -1, PokemonId: -1, ExpireTimestamp: uint32(now - 1)}, now)
	observeTappable(TappableLookup{RewardType: 0, ItemId: 7012, RewardPokemonId: -1, PokemonId: -1, ExpireTimestamp: uint32(now + 100)}, now)
	if hasReward(GetAvailableTappables(now), "item", i16(7011), nil) {
		t.Error("expired observation reported")
	}
	if hasReward(GetAvailableTappables(now+101), "item", i16(7012), nil) {
		t.Error("option reported past its expiry")
	}
}

func TestTappableAvailabilityNoExpiryLives60Min(t *testing.T) {
	now := time.Now().Unix()
	observeTappable(TappableLookup{RewardType: 0, ItemId: 7021, RewardPokemonId: -1, PokemonId: -1}, now)
	if !hasReward(GetAvailableTappables(now+3599), "item", i16(7021), nil) {
		t.Error("no-expiry option missing within 60 min")
	}
	if hasReward(GetAvailableTappables(now+3601), "item", i16(7021), nil) {
		t.Error("no-expiry option reported after 60 min")
	}
}

func TestTappableAvailabilityFromSaveAndPreload(t *testing.T) {
	now := time.Now().Unix()
	saved := newIndexedTappable(930101, 41.001, 51.001)
	saved.ItemId = null.IntFrom(7031)
	tappableRtreeUpdateOnSave(saved)
	pre := newIndexedTappable(930102, 41.002, 51.002)
	pre.ItemId = null.IntFrom(7032)
	tappablePreloadInsert(pre)
	a := GetAvailableTappables(now)
	if !hasReward(a, "item", i16(7031), nil) || !hasReward(a, "item", i16(7032), nil) {
		t.Errorf("rewards = %+v, want item/7031 (save) and item/7032 (preload)", a.Rewards)
	}
}
