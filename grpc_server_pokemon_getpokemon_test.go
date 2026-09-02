package main

import (
	"context"
	"testing"

	pb "golbat/grpc"
)

// A request for a pokemon this instance has never seen yields no result, not an
// empty placeholder: callers match answers to requests by PokemonResult.id.
func TestGetPokemonReturnsNothingForUnknownEncounter(t *testing.T) {
	s := &grpcPokemonServer{}

	resp, err := s.GetPokemon(context.Background(), &pb.GetPokemonRequest{
		Items: []*pb.GetPokemonItem{
			{EncounterId: 0xDEADBEEFDEADBEEF, PokemonId: 25, Form: 0, Weather: 0},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(resp.GetResults()) != 0 {
		t.Fatalf("expected no results for an unknown encounter, got %d", len(resp.GetResults()))
	}
}

// An empty request must not panic and must round-trip cleanly.
func TestGetPokemonHandlesEmptyRequest(t *testing.T) {
	s := &grpcPokemonServer{}

	resp, err := s.GetPokemon(context.Background(), &pb.GetPokemonRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(resp.GetResults()) != 0 {
		t.Fatalf("expected no results, got %d", len(resp.GetResults()))
	}
}
