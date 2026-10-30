package decoder

import (
	"github.com/puzpuzpuz/xsync/v4"
)

// Maintained max-expiry availability index for tappables, as
// fort_availability.go: observe on every index update, read emits unexpired
// options and prunes the rest.

type tappableRewardKey struct {
	RewardType      int8
	ItemId          int16
	RewardPokemonId int16
}

var (
	tappableRewardExpiry    *xsync.Map[tappableRewardKey, int64]
	tappableEncounterExpiry *xsync.Map[int16, int64]
)

func initTappableAvailability() {
	tappableRewardExpiry = xsync.NewMap[tappableRewardKey, int64]()
	tappableEncounterExpiry = xsync.NewMap[int16, int64]()
}

// tappableNoExpiryWindow matches the cache TTL of a tappable without an expiry.
const tappableNoExpiryWindow = 60 * 60

func observeTappable(l TappableLookup, now int64) {
	expiry := int64(l.ExpireTimestamp)
	if expiry == 0 {
		expiry = now + tappableNoExpiryWindow
	}
	if l.RewardType >= 0 {
		observeExpiry(tappableRewardExpiry, tappableRewardKey{l.RewardType, l.ItemId, l.RewardPokemonId}, expiry, now)
	}
	if l.PokemonId >= 0 {
		observeExpiry(tappableEncounterExpiry, l.PokemonId, expiry, now)
	}
}

type ApiTappableRewardAvailable struct {
	RewardType      string `json:"reward_type" doc:"reward_type (LootItemProto field name)"`
	ItemId          *int16 `json:"item_id" doc:"item_id for item rewards, else null"`
	RewardPokemonId *int16 `json:"reward_pokemon_id" doc:"Species for candy / XL candy / mega energy / temp evo branch rewards, else null"`
}

type ApiTappableEncounterAvailable struct {
	PokemonId int16 `json:"pokemon_id" doc:"Encounter species"`
}

type ApiTappableAvailable struct {
	Rewards    []ApiTappableRewardAvailable    `json:"rewards" doc:"Distinct live reward options (first loot item); each can be sent back as a scan filter clause"`
	Encounters []ApiTappableEncounterAvailable `json:"encounters" doc:"Distinct live encounter species"`
}

func optLookupInt16(v int16) *int16 {
	if v < 0 {
		return nil
	}
	return &v
}

func GetAvailableTappables(now int64) *ApiTappableAvailable {
	return &ApiTappableAvailable{
		Rewards: readAvailable(tappableRewardExpiry, now, func(k tappableRewardKey) ApiTappableRewardAvailable {
			return ApiTappableRewardAvailable{
				RewardType:      lootRewardTypeNames[k.RewardType],
				ItemId:          optLookupInt16(k.ItemId),
				RewardPokemonId: optLookupInt16(k.RewardPokemonId),
			}
		}),
		Encounters: readAvailable(tappableEncounterExpiry, now, func(id int16) ApiTappableEncounterAvailable {
			return ApiTappableEncounterAvailable{PokemonId: id}
		}),
	}
}
