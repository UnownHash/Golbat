package main

import (
	"context"
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

// GetPokemon answers a peer's batched question about pokemon this instance has
// already seen. It reads ONLY local caches and the database via
// decoder.GetOnePokemon: it never forwards to this instance's own peers, so a
// lookup cannot loop between instances — loop prevention by construction,
// not by a depth counter.
func (s *grpcPokemonServer) GetPokemon(ctx context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
	// Check for authorisation
	if config.Config.ApiSecret != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.ApiSecret {
			return &pb.GetPokemonResponse{}, nil
		}
	}

	results := make([]*pb.PokemonResult, 0, len(in.GetItems()))
	for _, item := range in.GetItems() {
		api := decoder.GetOnePokemon(item.GetEncounterId())
		if api == nil {
			continue
		}

		// Encounter ids are reused when the server mutates a spawn, so an id
		// alone does not identify a sighting: confirm the cached record
		// actually matches the pokemon (and form, when known) being asked
		// about before answering with it.
		if int32(api.PokemonId) != item.GetPokemonId() {
			continue
		}
		if api.Form != nil && int32(*api.Form) != item.GetForm() {
			continue
		}

		results = append(results, decoder.PokemonResultFromApi(api, item.GetEncounterId()))
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
