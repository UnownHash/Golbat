package main

import (
	"context"

	"golbat/config"
	pb "golbat/grpc"
	"golbat/raw_decoder/grpc_raw_decoder"

	_ "google.golang.org/grpc/encoding/gzip" // Install the gzip compressor
	"google.golang.org/grpc/metadata"
)

// server is used to implement helloworld.GreeterServer.
type grpcRawServer struct {
	pb.UnimplementedRawProtoServer

	rawDecoder *grpc_raw_decoder.GRPCRawDecoder
}

func (s *grpcRawServer) SubmitRawProto(ctx context.Context, in *pb.RawProtoRequest) (*pb.RawProtoResponse, error) {
	// Check for authorisation
	if config.Config.RawBearer != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.RawBearer {
			return &pb.RawProtoResponse{Message: "Incorrect authorisation received"}, nil
		}
	}
	s.rawDecoder.DecodeRaw(ctx, in)
	return &pb.RawProtoResponse{Message: "Processed"}, nil
}
