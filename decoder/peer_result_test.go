package decoder

import (
	"testing"
)

func TestPokemonResultFromApiCarriesStatsAndExpiry(t *testing.T) {
	atk, def, sta := int64(15), int64(14), int64(13)
	level, cp := int64(20), int64(1234)
	expire := int64(1786952280)

	const encounterId = uint64(12345678901234567890)

	api := &ApiPokemonResult{
		Id:                      Uint64Str(encounterId).String(),
		PokemonId:               25,
		AtkIv:                   &atk,
		DefIv:                   &def,
		StaIv:                   &sta,
		Level:                   &level,
		Cp:                      &cp,
		ExpireTimestamp:         &expire,
		ExpireTimestampVerified: true,
	}

	got := PokemonResultFromApi(api, encounterId)

	// High-bit encounter ids must survive: they are uniformly distributed over
	// the full 64-bit range, so anything narrower would corrupt them.
	if got.GetId() != encounterId {
		t.Fatalf("id: got %d want %d", got.GetId(), encounterId)
	}
	if got.GetPokemonId() != 25 {
		t.Fatalf("pokemon_id: got %d want 25", got.GetPokemonId())
	}
	if got.GetAtkIv() != 15 || got.GetDefIv() != 14 || got.GetStaIv() != 13 {
		t.Fatalf("ivs: got %d/%d/%d want 15/14/13", got.GetAtkIv(), got.GetDefIv(), got.GetStaIv())
	}
	if got.GetLevel() != 20 {
		t.Fatalf("level: got %d want 20", got.GetLevel())
	}
	if got.GetExpireTimestamp() != expire {
		t.Fatalf("expire_timestamp: got %d want %d", got.GetExpireTimestamp(), expire)
	}
	if !got.GetExpireTimestampVerified() {
		t.Fatal("expire_timestamp_verified must survive conversion")
	}
}

// Nil pointers must stay unset, not become zero values: a peer with no IVs is
// materially different from a peer reporting 0/0/0.
func TestPokemonResultFromApiLeavesMissingFieldsUnset(t *testing.T) {
	api := &ApiPokemonResult{Id: "1", PokemonId: 1}

	got := PokemonResultFromApi(api, 1)

	if got.AtkIv != nil {
		t.Fatal("absent atk_iv must stay unset")
	}
	if got.Cp != nil {
		t.Fatal("absent cp must stay unset")
	}
	if got.ExpireTimestamp != nil {
		t.Fatal("absent expire_timestamp must stay unset")
	}
}
