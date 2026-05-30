package main

import (
	"context"
	"net/http"

	"golbat/config"
	"golbat/decoder"

	"github.com/danielgtaylor/huma/v2"
)

type pokemonV2ScanInput struct {
	Body decoder.ApiPokemonScan2
}

type pokemonV2ScanOutput struct {
	Body []decoder.ApiPokemonResult
}

type pokemonV3ScanInput struct {
	Body decoder.ApiPokemonScan3
}

type pokemonV3ScanOutput struct {
	Body decoder.ApiPokemonScanResultV3
}

// registerHumaRoutes registers all Huma-backed operations on the given API.
func registerHumaRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "scan-pokemon-v2",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/v2/scan",
		Summary:       "Search pokemon in a bounding box (v2, DNF filters)",
		Description:   "Returns pokemon within [min,max] matching any DNF filter clause. Clauses are OR'd; conditions within a clause are AND'd. Returns a bare array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonV2ScanInput) (*pokemonV2ScanOutput, error) {
		return &pokemonV2ScanOutput{Body: decoder.GetPokemonInArea2Clean(in.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "scan-pokemon-v3",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/v3/scan",
		Summary:       "Search pokemon in a bounding box (v3, DNF filters)",
		Description:   "Returns pokemon within [min,max] matching any DNF filter clause. Clauses are OR'd; conditions within a clause are AND'd. Returns counts plus the matched array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonV3ScanInput) (*pokemonV3ScanOutput, error) {
		return &pokemonV3ScanOutput{Body: *decoder.GetPokemonInArea3Clean(in.Body)}, nil
	})
}

type pokemonSearchInput struct{ Body decoder.ApiPokemonSearch }
type pokemonSearchOutput struct{ Body []*decoder.ApiPokemonResult }

type pokemonByIdInput struct {
	PokemonId uint64 `path:"pokemon_id" doc:"Encounter ID of the pokemon"`
}
type pokemonByIdOutput struct{ Body decoder.ApiPokemonResult }

// registerPokemonReadRoutes registers the pokemon search and by-id read operations.
func registerPokemonReadRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "search-pokemon",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/search",
		Summary:       "Search pokemon by id within a bounding box",
		Description:   "Returns pokemon within [min,max] whose id is in searchIds, ordered by distance from center. Returns a bare array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonSearchInput) (*pokemonSearchOutput, error) {
		res, err := decoder.SearchPokemon(in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &pokemonSearchOutput{Body: res}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "get-pokemon",
		Method:        http.MethodGet,
		Path:          "/api/pokemon/id/{pokemon_id}",
		Summary:       "Get a single pokemon by encounter id",
		Description:   "Returns the pokemon with the given encounter id, or 404 if not present in the cache.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonByIdInput) (*pokemonByIdOutput, error) {
		res := decoder.GetOnePokemon(in.PokemonId)
		if res == nil {
			return nil, huma.Error404NotFound("pokemon not found")
		}
		return &pokemonByIdOutput{Body: *res}, nil
	})
}

type gymScanInput struct{ Body decoder.ApiFortScan }
type gymScanOutput struct{ Body decoder.ApiGymScanResult }

type pokestopScanInput struct{ Body decoder.ApiFortScan }
type pokestopScanOutput struct{ Body decoder.ApiPokestopScanResult }

type stationScanInput struct{ Body decoder.ApiFortScan }
type stationScanOutput struct{ Body decoder.ApiStationScanResult }

type fortScanInput struct{ Body decoder.ApiFortScan }
type fortScanOutput struct{ Body decoder.ApiFortCombinedScanResult }

// registerFortScanRoutes registers the four in-memory fort scan operations.
// These are gated by config.Config.FortInMemory and return 503 when disabled.
func registerFortScanRoutes(api huma.API) {
	gymOp := huma.Operation{
		OperationID:   "scan-gyms",
		Method:        http.MethodPost,
		Path:          "/api/gym/scan",
		Summary:       "Search gyms in a bounding box (DNF filters)",
		Description:   "Returns gyms within [min,max] matching any DNF filter clause.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&gymOp)
	huma.Register(api, gymOp, func(ctx context.Context, in *gymScanInput) (*gymScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &gymScanOutput{Body: *decoder.GymScanEndpoint(in.Body, dbDetails)}, nil
	})

	pokestopOp := huma.Operation{
		OperationID:   "scan-pokestops",
		Method:        http.MethodPost,
		Path:          "/api/pokestop/scan",
		Summary:       "Search pokestops in a bounding box (DNF filters)",
		Description:   "Returns pokestops within [min,max] matching any DNF filter clause.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&pokestopOp)
	huma.Register(api, pokestopOp, func(ctx context.Context, in *pokestopScanInput) (*pokestopScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &pokestopScanOutput{Body: *decoder.PokestopScanEndpoint(in.Body, dbDetails)}, nil
	})

	stationOp := huma.Operation{
		OperationID:   "scan-stations",
		Method:        http.MethodPost,
		Path:          "/api/station/scan",
		Summary:       "Search stations in a bounding box (DNF filters)",
		Description:   "Returns stations within [min,max] matching any DNF filter clause.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&stationOp)
	huma.Register(api, stationOp, func(ctx context.Context, in *stationScanInput) (*stationScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &stationScanOutput{Body: *decoder.StationScanEndpoint(in.Body, dbDetails)}, nil
	})

	fortOp := huma.Operation{
		OperationID:   "scan-forts",
		Method:        http.MethodPost,
		Path:          "/api/fort/scan",
		Summary:       "Search all fort types in a bounding box (DNF filters)",
		Description:   "Returns gyms, pokestops, and stations within [min,max] matching any DNF filter clause, in a single rtree traversal.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&fortOp)
	huma.Register(api, fortOp, func(ctx context.Context, in *fortScanInput) (*fortScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &fortScanOutput{Body: *decoder.FortCombinedScanEndpoint(in.Body, dbDetails)}, nil
	})
}
