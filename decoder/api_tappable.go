package decoder

import "encoding/json"

// ApiTappableResult is the API representation of a tappable. Nullable database
// columns are represented as pointers (nil => JSON null) without omitempty so
// every key is always present.
type ApiTappableResult struct {
	Id                      uint64           `json:"id" doc:"Tappable encounter ID"`
	Lat                     float64          `json:"lat" doc:"Latitude of the tappable"`
	Lon                     float64          `json:"lon" doc:"Longitude of the tappable"`
	FortId                  *string          `json:"fort_id" doc:"ID of the fort the tappable belongs to"`
	SpawnId                 *int64           `json:"spawn_id" doc:"ID of the spawnpoint the tappable belongs to"`
	Type                    string           `json:"type" doc:"Type of the tappable"`
	Encounter               *int64           `json:"pokemon_id" doc:"Pokedex ID of the encountered pokemon"`
	RewardType              *string          `json:"reward_type" doc:"Reward kind: LootItemProto field name (item, pokemon_candy, xl_candy, mega_energy_pokemon_id, temp_evo_pokemon_branch, stardust, …)"`
	ItemId                  *int64           `json:"item_id" doc:"ID of the item reward"`
	RewardPokemonId         *int64           `json:"reward_pokemon_id" doc:"Pokedex ID for candy, XL candy, mega energy and temp evo branch rewards"`
	Count                   *int64           `json:"count" doc:"Amount of the reward"`
	Rewards                 *json.RawMessage `json:"rewards" doc:"All loot items as protojson (proto field names, numeric enums), e.g. [{\"pokemon_candy\":25,\"count\":6}]; null for encounters or when no loot item could be stored"`
	ExpireTimestamp         *int64           `json:"expire_timestamp" doc:"Unix timestamp when the tappable expires"`
	ExpireTimestampVerified bool             `json:"expire_timestamp_verified" doc:"Whether the expire timestamp is verified"`
	Updated                 int64            `json:"updated" doc:"Unix timestamp when the record was last updated"`
}

func buildTappableResult(tappable *Tappable) ApiTappableResult {
	fortId := tappable.FortId.Ptr()
	return ApiTappableResult{
		Id:                      tappable.Id,
		Lat:                     tappable.Lat,
		Lon:                     tappable.Lon,
		FortId:                  fortId,
		SpawnId:                 tappable.SpawnId.Ptr(),
		Type:                    tappable.Type,
		Encounter:               tappable.Encounter.Ptr(),
		RewardType:              tappable.RewardType.Ptr(),
		ItemId:                  tappable.ItemId.Ptr(),
		RewardPokemonId:         tappable.RewardPokemonId.Ptr(),
		Count:                   tappable.Count.Ptr(),
		Rewards:                 jsonRaw(tappable.Rewards),
		ExpireTimestamp:         tappable.ExpireTimestamp.Ptr(),
		ExpireTimestampVerified: tappable.ExpireTimestampVerified,
		Updated:                 tappable.Updated,
	}
}

func BuildTappableResult(tappable *Tappable) ApiTappableResult {
	return buildTappableResult(tappable)
}
