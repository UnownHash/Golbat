package main

import (
	"context"
	"golbat/config"
	pb "golbat/grpc"

	_ "google.golang.org/grpc/encoding/gzip" // Install the gzip compressor
	"google.golang.org/grpc/metadata"
)

// server is used to implement helloworld.GreeterServer.
type grpcRawServer struct {
	pb.UnimplementedRawProtoServer
}

func (s *grpcRawServer) SubmitRawProto(ctx context.Context, in *pb.RawProtoRequest) (*pb.RawProtoResponse, error) {
	// Check for authorisation
	if config.Config.RawBearer != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.RawBearer {
			return &pb.RawProtoResponse{Message: "Incorrect authorisation received"}, nil
		}
	}

	protoData := rawProtoDecoder.GetProtoDataFromGRPC(in)
	// Process each proto in a packet in sequence, but in a go-routine
	go rawProtoDecoder.Decode(context.Background(), protoData, decode)

	deviceTracker.UpdateDeviceLocation(protoData.Uuid, protoData.Lat(), protoData.Lon(), protoData.ScanContext)

	return &pb.RawProtoResponse{Message: "Processed"}, nil
}
