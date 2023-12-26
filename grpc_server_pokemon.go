package main

import (
	"context"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/decoder"
	pb "golbat/grpc"
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
