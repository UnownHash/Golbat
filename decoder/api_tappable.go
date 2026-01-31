package decoder

import "github.com/guregu/null/v6"

type ApiTappableResult struct {
	Id                      uint64      `json:"id"`
	Lat                     float64     `json:"lat"`
	Lon                     float64     `json:"lon"`
	FortId                  null.String `json:"fort_id"`
	SpawnId                 null.Int    `json:"spawn_id"`
	Type                    string      `json:"type"`
	Encounter               null.Int    `json:"pokemon_id"`
	ItemId                  null.Int    `json:"item_id"`
	Count                   null.Int    `json:"count"`
	ExpireTimestamp         null.Int    `json:"expire_timestamp"`
	ExpireTimestampVerified bool        `json:"expire_timestamp_verified"`
	Updated                 int64       `json:"updated"`
}

func buildTappableResult(tappable *Tappable) ApiTappableResult {
	return ApiTappableResult{
		Id:                      tappable.Id,
		Lat:                     tappable.Lat,
		Lon:                     tappable.Lon,
		FortId:                  tappable.FortId,
		SpawnId:                 tappable.SpawnId,
		Type:                    tappable.Type,
		Encounter:               tappable.Encounter,
		ItemId:                  tappable.ItemId,
		Count:                   tappable.Count,
		ExpireTimestamp:         tappable.ExpireTimestamp,
		ExpireTimestampVerified: tappable.ExpireTimestampVerified,
		Updated:                 tappable.Updated,
	}
}

func BuildTappableResult(tappable *Tappable) ApiTappableResult {
	return buildTappableResult(tappable)
}
