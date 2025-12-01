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
