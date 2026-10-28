package decoder

import (
	"encoding/json"
	"strconv"

	"github.com/guregu/null/v6"

	"golbat/webhooks"
)

// TappableWebhook is the "tappable" webhook payload: reward tappables only
// (encounters go out as pokemon webhooks). Shared fields use the pokemon
// hook's names and encodings.
type TappableWebhook struct {
	EncounterId           string          `json:"encounter_id"`
	SpawnpointId          string          `json:"spawnpoint_id"` // hex, "None" when absent
	PokestopId            string          `json:"pokestop_id"`   // "None" when absent
	PokestopName          *string         `json:"pokestop_name"` // null without a stop, "Unknown" if not found
	Latitude              float64         `json:"latitude"`
	Longitude             float64         `json:"longitude"`
	Type                  string          `json:"type"`
	RewardType            null.String     `json:"reward_type"`
	ItemId                null.Int        `json:"item_id"`
	RewardPokemonId       null.Int        `json:"reward_pokemon_id"`
	Count                 null.Int        `json:"count"`
	Rewards               json.RawMessage `json:"rewards"`
	DisappearTime         int64           `json:"disappear_time"` // 0 when unknown
	DisappearTimeVerified bool            `json:"disappear_time_verified"`
	Updated               int64           `json:"updated"`
}

func buildTappableWebhook(t *Tappable, pokestopName *string) TappableWebhook {
	spawnpointId := "None"
	if t.SpawnId.Valid {
		spawnpointId = strconv.FormatUint(uint64(t.SpawnId.Int64), 16)
	}
	pokestopId := "None"
	if t.FortId.Valid() {
		pokestopId = t.FortId.String()
	}
	var rewards json.RawMessage
	if r := jsonRaw(t.Rewards); r != nil {
		rewards = *r
	}
	return TappableWebhook{
		EncounterId:           strconv.FormatUint(t.Id, 10),
		SpawnpointId:          spawnpointId,
		PokestopId:            pokestopId,
		PokestopName:          pokestopName,
		Latitude:              t.Lat,
		Longitude:             t.Lon,
		Type:                  t.Type,
		RewardType:            t.RewardType,
		ItemId:                t.ItemId,
		RewardPokemonId:       t.RewardPokemonId,
		Count:                 t.Count,
		Rewards:               rewards,
		DisappearTime:         t.ExpireTimestamp.ValueOrZero(),
		DisappearTimeVerified: t.ExpireTimestampVerified,
		Updated:               t.Updated,
	}
}

// tappableWebhookOnSave sends the tappable webhook when a reward is first
// recorded (new tappable or reward type changed), then clears rewardChanged.
// Caller holds the tappable lock; pokestopName is resolved before it.
func tappableWebhookOnSave(t *Tappable, isNewRecord bool, pokestopName *string) {
	if t.RewardType.Valid && (isNewRecord || t.rewardChanged) {
		webhooksSender.AddMessage(webhooks.Tappable, buildTappableWebhook(t, pokestopName), MatchStatsGeofence(t.Lat, t.Lon))
	}
	t.rewardChanged = false
}
