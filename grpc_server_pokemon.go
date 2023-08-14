package main

import (
	"context"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	pb "golbat/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // Install the gzip compressor
	"google.golang.org/grpc/metadata"
)

// server is used to implement helloworld.GreeterServer.
type grpcPokemonServer struct {
	pb.UnimplementedPokemonServer
}

func (s *grpcPokemonServer) Search(ctx context.Context, in *pb.PokemonSearchRequest) (*pb.PokemonSearchResponse, error) {
	// Check for authorisation
	if config.Config.RawBearer != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.ApiSecret {
			return &pb.PokemonSearchResponse{}, nil
		}
	}

	log.Infof("Received request %+v", in)

	return &pb.PokemonSearchResponse{}, nil
}
