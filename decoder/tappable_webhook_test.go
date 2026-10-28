package decoder

import (
	"testing"

	"github.com/guregu/null/v6"

	"golbat/jsonenc"
)

func TestTappableWebhookGolden(t *testing.T) {
	ta := &Tappable{TappableData: TappableData{
		Id: 18446744073709551000, Lat: 47.3769, Lon: 8.5417, Type: "TAPPABLE_TYPE_BREAKFAST",
		SpawnId: null.IntFrom(987654321), RewardType: null.StringFrom("pokemon_candy"),
		RewardPokemonId: null.IntFrom(25), Count: null.IntFrom(6),
		Rewards:         null.StringFrom(`[{"pokemon_candy":25,"count":6}]`),
		ExpireTimestamp: null.IntFrom(1761660000), ExpireTimestampVerified: true, Updated: 1761659400,
	}}
	got, err := jsonenc.Marshal(buildTappableWebhook(ta))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"18446744073709551000","latitude":47.3769,"longitude":8.5417,"type":"TAPPABLE_TYPE_BREAKFAST","fort_id":null,"spawn_id":987654321,"reward_type":"pokemon_candy","item_id":null,"reward_pokemon_id":25,"count":6,"rewards":[{"pokemon_candy":25,"count":6}],"expire_timestamp":1761660000,"expire_timestamp_verified":true,"updated":1761659400}`
	if string(got) != want {
		t.Errorf("payload changed.\n got: %s\nwant: %s", got, want)
	}
}
