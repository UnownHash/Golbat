package main

import (
	"context"
	"net/http"

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
