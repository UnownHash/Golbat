package decoder

import (
	"encoding/json"
	"strconv"
)

// TappableWebhook is the "tappable" webhook payload: reward tappables only
// (encounters go out as pokemon webhooks). Nullable fields are pointers.
type TappableWebhook struct {
	Id                      string           `json:"id"`
	Latitude                float64          `json:"latitude"`
	Longitude               float64          `json:"longitude"`
	Type                    string           `json:"type"`
	FortId                  *string          `json:"fort_id"`
	SpawnId                 *int64           `json:"spawn_id"`
	RewardType              *string          `json:"reward_type"`
	ItemId                  *int64           `json:"item_id"`
	RewardPokemonId         *int64           `json:"reward_pokemon_id"`
	Count                   *int64           `json:"count"`
	Rewards                 *json.RawMessage `json:"rewards"`
	ExpireTimestamp         *int64           `json:"expire_timestamp"`
	ExpireTimestampVerified bool             `json:"expire_timestamp_verified"`
	Updated                 int64            `json:"updated"`
}

func buildTappableWebhook(t *Tappable) TappableWebhook {
	return TappableWebhook{
		Id:                      strconv.FormatUint(t.Id, 10),
		Latitude:                t.Lat,
		Longitude:               t.Lon,
		Type:                    t.Type,
		FortId:                  t.FortId.Ptr(),
		SpawnId:                 t.SpawnId.Ptr(),
		RewardType:              t.RewardType.Ptr(),
		ItemId:                  t.ItemId.Ptr(),
		RewardPokemonId:         t.RewardPokemonId.Ptr(),
		Count:                   t.Count.Ptr(),
		Rewards:                 jsonRaw(t.Rewards),
		ExpireTimestamp:         t.ExpireTimestamp.Ptr(),
		ExpireTimestampVerified: t.ExpireTimestampVerified,
		Updated:                 t.Updated,
	}
}
