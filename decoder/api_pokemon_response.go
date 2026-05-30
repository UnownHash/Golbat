package decoder

import (
	"time"

	"github.com/UnownHash/gohbem"
)

// PvpEntry mirrors gohbem.PokemonEntry for the documented API response.
type PvpEntry struct {
	Pokemon    int     `json:"pokemon" doc:"Pokemon ID for this PVP ranking entry"`
	Form       int     `json:"form,omitempty" doc:"Form ID for this PVP ranking entry"`
	Cap        float64 `json:"cap,omitempty" doc:"Level cap used for this ranking"`
	Value      float64 `json:"value,omitempty" doc:"Stat product value"`
	Level      float64 `json:"level" doc:"Level at which this ranking is achieved"`
	Cp         int     `json:"cp,omitempty" doc:"CP at this ranking"`
	Percentage float64 `json:"percentage" doc:"Stat product percentage relative to the best possible"`
	Rank       int16   `json:"rank" doc:"PVP rank (1 = best)"`
	Capped     bool    `json:"capped,omitempty" doc:"Whether the level was capped"`
	Evolution  int     `json:"evolution,omitempty" doc:"Evolution ID if this ranking is for an evolved form"`
}

// PvpRankings holds the PVP rankings per league.
//
// The league fields use omitempty so a league with no ranking is dropped from
// the JSON, matching the legacy wire format: the old endpoint emitted a dynamic
// map[string][]gohbem.PokemonEntry from QueryPvPRank that only included a league
// key when that league had entries. So a pokemon ranked only in Great serializes
// as `"pvp":{"great":[...]}` and a pokemon with no rankings as `"pvp":{}`. Using
// a fixed struct (rather than a map) keeps a stable, documentable OpenAPI schema
// that still lists all three leagues. (The only remaining difference from legacy
// is the fully-disabled case: legacy emitted `"pvp":null` when ohbem was nil,
// where this emits `"pvp":{}`.)
type PvpRankings struct {
	Little []PvpEntry `json:"little,omitempty" doc:"PVP rankings for the Little league (CP cap 500)"`
	Great  []PvpEntry `json:"great,omitempty" doc:"PVP rankings for the Great league (CP cap 1500)"`
	Ultra  []PvpEntry `json:"ultra,omitempty" doc:"PVP rankings for the Ultra league (CP cap 2500)"`
}

// PokemonResult is the documented, pointer-based equivalent of ApiPokemonResult.
// Nullable database columns are represented as pointers (nil => JSON null) without
// omitempty so every key is always present, matching the legacy wire format.
// Field declaration order mirrors ApiPokemonResult for diff-friendly comparison.
//
// The `pvp` field intentionally diverges from the legacy wire format; see the
// PvpRankings doc comment for the rationale and exact differences.
type PokemonResult struct {
	Id                      string      `json:"id" doc:"Encounter ID of the pokemon"`
	PokestopId              *string     `json:"pokestop_id" doc:"ID of the pokestop the pokemon was seen near, if any"`
	SpawnId                 *int64      `json:"spawn_id" doc:"Spawnpoint ID for this pokemon, if known"`
	Lat                     float64     `json:"lat" doc:"Latitude of the pokemon"`
	Lon                     float64     `json:"lon" doc:"Longitude of the pokemon"`
	Weight                  *float64    `json:"weight" doc:"Weight of the pokemon"`
	Size                    *int64      `json:"size" doc:"Size value of the pokemon"`
	Height                  *float64    `json:"height" doc:"Height of the pokemon"`
	ExpireTimestamp         *int64      `json:"expire_timestamp" doc:"Unix timestamp when the pokemon despawns"`
	Updated                 *int64      `json:"updated" doc:"Unix timestamp when the record was last updated"`
	PokemonId               int16       `json:"pokemon_id" doc:"Pokedex ID of the pokemon"`
	Move1                   *int64      `json:"move_1" doc:"Fast move ID"`
	Move2                   *int64      `json:"move_2" doc:"Charge move ID"`
	Gender                  *int64      `json:"gender" doc:"Gender of the pokemon"`
	Cp                      *int64      `json:"cp" doc:"Combat power of the pokemon"`
	AtkIv                   *int64      `json:"atk_iv" doc:"Attack individual value"`
	DefIv                   *int64      `json:"def_iv" doc:"Defense individual value"`
	StaIv                   *int64      `json:"sta_iv" doc:"Stamina individual value"`
	Iv                      *float64    `json:"iv" doc:"Overall IV percentage"`
	Form                    *int64      `json:"form" doc:"Form ID of the pokemon"`
	Level                   *int64      `json:"level" doc:"Level of the pokemon"`
	Weather                 *int64      `json:"weather" doc:"Weather boost ID affecting the pokemon"`
	Costume                 *int64      `json:"costume" doc:"Costume ID of the pokemon"`
	FirstSeenTimestamp      int64       `json:"first_seen_timestamp" doc:"Unix timestamp when the pokemon was first seen"`
	Changed                 int64       `json:"changed" doc:"Unix timestamp when the pokemon last changed"`
	CellId                  *int64      `json:"cell_id" doc:"S2 cell ID the pokemon belongs to"`
	ExpireTimestampVerified bool        `json:"expire_timestamp_verified" doc:"Whether the despawn timestamp is verified"`
	DisplayPokemonId        *int64      `json:"display_pokemon_id" doc:"Displayed pokemon ID (e.g. for Ditto disguises)"`
	DisplayPokemonForm      *int64      `json:"display_pokemon_form" doc:"Displayed pokemon form"`
	IsDitto                 bool        `json:"is_ditto" doc:"Whether the pokemon is a disguised Ditto"`
	SeenType                *string     `json:"seen_type" doc:"How the pokemon was seen (wild, encounter, nearby_stop, nearby_cell)"`
	Shiny                   *bool       `json:"shiny" doc:"Whether the pokemon is shiny"`
	Username                *string     `json:"username" doc:"Username of the account that reported the pokemon"`
	Capture1                *float64    `json:"capture_1" doc:"Base capture rate with one ball"`
	Capture2                *float64    `json:"capture_2" doc:"Base capture rate with two balls"`
	Capture3                *float64    `json:"capture_3" doc:"Base capture rate with three balls"`
	Pvp                     PvpRankings `json:"pvp" doc:"PVP rankings for the pokemon"`
	IsEvent                 int8        `json:"is_event" doc:"Whether the pokemon is part of an event"`
}

// buildPokemonResult builds a PokemonResult whose ~35 scalar (non-pvp) fields are
// JSON wire-identical to the legacy buildApiPokemonResult. The `pvp` field is built
// from a fixed-league PvpRankings struct (for a documentable schema) but, thanks to
// omitempty, serializes the same league keys as the legacy dynamic map — see the
// PvpRankings doc comment for the one edge-case difference (ohbem disabled).
//
// PARITY: like buildApiPokemonResult, Capture1, Capture2, Capture3 and IsEvent are
// intentionally left unset. The legacy builder never populated these, so today's
// JSON emits capture_1/2/3: null and is_event: 0. Replicating that here preserves
// wire compatibility. Do not populate them without coordinating a wire change.
func buildPokemonResult(pokemon *Pokemon) PokemonResult {
	return PokemonResult{
		Id:                      pokemon.Id.String(),
		PokestopId:              pokemon.PokestopId.Ptr(),
		SpawnId:                 pokemon.SpawnId.Ptr(),
		Lat:                     pokemon.Lat,
		Lon:                     pokemon.Lon,
		Weight:                  pokemon.Weight.Ptr(),
		Size:                    pokemon.Size.Ptr(),
		Height:                  pokemon.Height.Ptr(),
		ExpireTimestamp:         pokemon.ExpireTimestamp.Ptr(),
		Updated:                 pokemon.Updated.Ptr(),
		PokemonId:               pokemon.PokemonId,
		Move1:                   pokemon.Move1.Ptr(),
		Move2:                   pokemon.Move2.Ptr(),
		Gender:                  pokemon.Gender.Ptr(),
		Cp:                      pokemon.Cp.Ptr(),
		AtkIv:                   pokemon.AtkIv.Ptr(),
		DefIv:                   pokemon.DefIv.Ptr(),
		StaIv:                   pokemon.StaIv.Ptr(),
		Iv:                      pokemon.Iv.Ptr(),
		Form:                    pokemon.Form.Ptr(),
		Level:                   pokemon.Level.Ptr(),
		Weather:                 pokemon.Weather.Ptr(),
		Costume:                 pokemon.Costume.Ptr(),
		FirstSeenTimestamp:      pokemon.FirstSeenTimestamp,
		Changed:                 pokemon.Changed,
		CellId:                  pokemon.CellId.Ptr(),
		ExpireTimestampVerified: pokemon.ExpireTimestampVerified,
		DisplayPokemonId:        pokemon.DisplayPokemonId.Ptr(),
		DisplayPokemonForm:      pokemon.DisplayPokemonForm.Ptr(),
		IsDitto:                 pokemon.IsDitto,
		SeenType:                pokemon.SeenType.Ptr(),
		Shiny:                   pokemon.Shiny.Ptr(),
		Username:                pokemon.Username.Ptr(),
		// Capture1/Capture2/Capture3/IsEvent intentionally left unset for parity
		// with buildApiPokemonResult (see function doc comment).
		Pvp: buildPvpRankings(pokemon),
	}
}

// buildPvpRankings queries ohbem for PVP rankings, mirroring the legacy Pvp logic.
// Returns a zero value when PVP is disabled (ohbem == nil) or on query error.
func buildPvpRankings(pokemon *Pokemon) PvpRankings {
	if ohbem == nil {
		return PvpRankings{}
	}
	pvp, err := ohbem.QueryPvPRank(int(pokemon.PokemonId),
		int(pokemon.Form.ValueOrZero()),
		int(pokemon.Costume.ValueOrZero()),
		int(pokemon.Gender.ValueOrZero()),
		int(pokemon.AtkIv.ValueOrZero()),
		int(pokemon.DefIv.ValueOrZero()),
		int(pokemon.StaIv.ValueOrZero()),
		float64(pokemon.Level.ValueOrZero()))
	if err != nil {
		return PvpRankings{}
	}
	// The hardcoded little/great/ultra keys correspond to the leagues configured
	// in the ohbem init in decoder/main.go (~line 209). Adding a league there must
	// also be reflected here (and in the PvpRankings struct).
	return PvpRankings{
		Little: convertPvpEntries(pvp["little"]),
		Great:  convertPvpEntries(pvp["great"]),
		Ultra:  convertPvpEntries(pvp["ultra"]),
	}
}

// PokemonScanResultV3 is the v3-only response envelope wrapping the matched
// pokemon together with the spatial-index candidate counts.
type PokemonScanResultV3 struct {
	Pokemon  []PokemonResult `json:"pokemon" doc:"Matched pokemon"`
	Examined int             `json:"examined" doc:"Candidates examined from the spatial index"`
	Skipped  int             `json:"skipped" doc:"Candidates skipped (expired or filtered)"`
	Total    int             `json:"total" doc:"Total candidates in the bounding box"`
}

// GetPokemonInArea2Clean runs the v2 rtree/DNF search and returns a bare array of
// PokemonResult, discarding the candidate counts (matching the legacy v2 shape).
func GetPokemonInArea2Clean(req ApiPokemonScan2) []PokemonResult {
	keys, _, _, _ := internalGetPokemonInArea2(req)
	return collectPokemonResults(keys, "API.ScanPokemon.v2.clean")
}

// GetPokemonInArea3Clean runs the v3 rtree/DNF search and returns the matched
// pokemon together with the candidate counts in the v3 envelope.
func GetPokemonInArea3Clean(req ApiPokemonScan3) *PokemonScanResultV3 {
	keys, examined, skipped, total := internalGetPokemonInArea3(req)
	return &PokemonScanResultV3{
		Pokemon:  collectPokemonResults(keys, "API.ScanPokemon.v3.clean"),
		Examined: examined,
		Skipped:  skipped,
		Total:    total,
	}
}

// collectPokemonResults peeks each pokemon by encounter ID and builds PokemonResult
// values, filtering out expired pokemon exactly as the legacy builders do.
func collectPokemonResults(keys []uint64, caller string) []PokemonResult {
	results := make([]PokemonResult, 0, len(keys))
	nowUnix := time.Now().Unix()
	for _, key := range keys {
		pokemon, unlock, _ := peekPokemonRecordReadOnly(key, caller)
		if pokemon != nil {
			if pokemon.ExpireTimestamp.ValueOrZero() > nowUnix {
				results = append(results, buildPokemonResult(pokemon))
			}
			unlock()
		}
	}
	return results
}

// convertPvpEntries maps gohbem entries to PvpEntry, preserving nil for nil input.
func convertPvpEntries(entries []gohbem.PokemonEntry) []PvpEntry {
	if entries == nil {
		return nil
	}
	result := make([]PvpEntry, len(entries))
	for i, e := range entries {
		result[i] = PvpEntry{
			Pokemon:    e.Pokemon,
			Form:       e.Form,
			Cap:        e.Cap,
			Value:      e.Value,
			Level:      e.Level,
			Cp:         e.Cp,
			Percentage: e.Percentage,
			Rank:       e.Rank,
			Capped:     e.Capped,
			Evolution:  e.Evolution,
		}
	}
	return result
}
