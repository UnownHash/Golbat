package decoder

import (
    "github.com/UnownHash/gohbem"
)

func buildApiPokemonResult(pokemon *Pokemon) ApiPokemonResult {
    return ApiPokemonResult{
        Id:                      pokemon.Id,
        PokestopId:              pokemon.PokestopId,
        SpawnId:                 pokemon.SpawnId,
        Lat:                     pokemon.Lat,
        Lon:                     pokemon.Lon,
        Weight:                  pokemon.Weight,
        Size:                    pokemon.Size,
        Height:                  pokemon.Height,
        ExpireTimestamp:         pokemon.ExpireTimestamp,
        Updated:                 pokemon.Updated,
        PokemonId:               pokemon.PokemonId,
        Move1:                   pokemon.Move1,
        Move2:                   pokemon.Move2,
        Gender:                  pokemon.Gender,
        Cp:                      pokemon.Cp,
        AtkIv:                   pokemon.AtkIv,
        DefIv:                   pokemon.DefIv,
        StaIv:                   pokemon.StaIv,
        Iv:                      pokemon.Iv,
        Form:                    pokemon.Form,
        Level:                   pokemon.Level,
        Weather:                 pokemon.Weather,
        Costume:                 pokemon.Costume,
        FirstSeenTimestamp:      pokemon.FirstSeenTimestamp,
        Changed:                 pokemon.Changed,
        CellId:                  pokemon.CellId,
        ExpireTimestampVerified: pokemon.ExpireTimestampVerified,
        DisplayPokemonId:        pokemon.DisplayPokemonId,
        IsDitto:                 pokemon.IsDitto,
        SeenType:                pokemon.SeenType,
        Shiny:                   pokemon.Shiny,
        Username:                pokemon.Username,
        Pvp: func() map[string][]gohbem.PokemonEntry {
            if ohbem != nil {
                pvp, err := ohbem.QueryPvPRank(int(pokemon.PokemonId),
                    int(pokemon.Form.ValueOrZero()),
                    int(pokemon.Costume.ValueOrZero()),
                    int(pokemon.Gender.ValueOrZero()),
                    int(pokemon.AtkIv.ValueOrZero()),
                    int(pokemon.DefIv.ValueOrZero()),
                    int(pokemon.StaIv.ValueOrZero()),
                    float64(pokemon.Level.ValueOrZero()))
                if err != nil {
                    return nil
                }
                return pvp
            }
            return nil
        }(),
    }
}


