package main

import (
	"context"
	"time"

	"golbat/config"
	"golbat/decoder"
	pb "golbat/grpc"

	log "github.com/sirupsen/logrus"
	_ "google.golang.org/grpc/encoding/gzip" // Install the gzip compressor
	"google.golang.org/grpc/metadata"
)

// server is used to implement helloworld.GreeterServer.
type grpcPokemonServer struct {
	pb.UnimplementedPokemonServer
}

func (s *grpcPokemonServer) Search(ctx context.Context, in *pb.PokemonScanRequest) (*pb.PokemonScanResponse, error) {
	// Check for authorisation
	if config.Config.ApiSecret != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.ApiSecret {
			return &pb.PokemonScanResponse{}, nil
		}
	}

	log.Infof("Received request %+v", in)

	return &pb.PokemonScanResponse{
		Status:  pb.PokemonScanResponse_SUCCESS,
		Pokemon: decoder.GrpcGetPokemonInArea2(in),
	}, nil
}

// GetPokemon answers a peer's batched question about pokemon this instance
// has already seen. Authorisation and transport live here; the answer for
// each item is decoder.AnswerPeerLookup, which reads ONLY local in-memory
// caches (no database fallback) and never forwards to this instance's own
// peers, so a lookup cannot loop between instances — loop prevention by
// construction, not by a depth counter.
func (s *grpcPokemonServer) GetPokemon(ctx context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
	// Check for authorisation
	if config.Config.ApiSecret != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.ApiSecret {
			return &pb.GetPokemonResponse{}, nil
		}
	}

	now := time.Now()
	results := make([]*pb.PokemonResult, 0, len(in.GetItems()))
	for _, item := range in.GetItems() {
		// Misses are omitted, not returned as placeholders: a nil in a
		// repeated message field marshals as an empty message, which is
		// indistinguishable from an all-default record.
		if result := decoder.AnswerPeerLookup(item, now); result != nil {
			results = append(results, result)
		}
	}

	return &pb.GetPokemonResponse{Results: results}, nil
}

func (s *grpcPokemonServer) SearchV3(ctx context.Context, in *pb.PokemonScanRequestV3) (*pb.PokemonScanResponseV3, error) {
	// Check for authorisation
	if config.Config.ApiSecret != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.ApiSecret {
			return &pb.PokemonScanResponseV3{}, nil
		}
	}

	log.Infof("Received V3 request %+v", in)
	pokemon, examined, skipped, total := decoder.GrpcGetPokemonInArea3(in)

	return &pb.PokemonScanResponseV3{
		Status:   pb.PokemonScanResponseV3_SUCCESS,
		Pokemon:  pokemon,
		Examined: int32(examined),
		Skipped:  int32(skipped),
		Total:    int32(total),
	}, nil
}
