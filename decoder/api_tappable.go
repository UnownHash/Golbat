package decoder

// ApiTappableResult is the API representation of a tappable. Nullable database
// columns are represented as pointers (nil => JSON null) without omitempty so
// every key is always present.
type ApiTappableResult struct {
	Id                      uint64  `json:"id" doc:"Tappable encounter ID"`
	Lat                     float64 `json:"lat" doc:"Latitude of the tappable"`
	Lon                     float64 `json:"lon" doc:"Longitude of the tappable"`
	FortId                  *string `json:"fort_id" doc:"ID of the fort the tappable belongs to"`
	SpawnId                 *int64  `json:"spawn_id" doc:"ID of the spawnpoint the tappable belongs to"`
	Type                    string  `json:"type" doc:"Type of the tappable"`
	Encounter               *int64  `json:"pokemon_id" doc:"Pokedex ID of the encountered pokemon"`
	ItemId                  *int64  `json:"item_id" doc:"ID of the item reward"`
	Count                   *int64  `json:"count" doc:"Count of the item reward"`
	ExpireTimestamp         *int64  `json:"expire_timestamp" doc:"Unix timestamp when the tappable expires"`
	ExpireTimestampVerified bool    `json:"expire_timestamp_verified" doc:"Whether the expire timestamp is verified"`
	Updated                 int64   `json:"updated" doc:"Unix timestamp when the record was last updated"`
}

func buildTappableResult(tappable *Tappable) ApiTappableResult {
	return ApiTappableResult{
		Id:                      tappable.Id,
		Lat:                     tappable.Lat,
		Lon:                     tappable.Lon,
		FortId:                  tappable.FortId.Ptr(),
		SpawnId:                 tappable.SpawnId.Ptr(),
		Type:                    tappable.Type,
		Encounter:               tappable.Encounter.Ptr(),
		ItemId:                  tappable.ItemId.Ptr(),
		Count:                   tappable.Count.Ptr(),
		ExpireTimestamp:         tappable.ExpireTimestamp.Ptr(),
		ExpireTimestampVerified: tappable.ExpireTimestampVerified,
		Updated:                 tappable.Updated,
	}
}

func BuildTappableResult(tappable *Tappable) ApiTappableResult {
	return buildTappableResult(tappable)
}
