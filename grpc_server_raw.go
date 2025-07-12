package main

import (
	"context"
	"time"

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
	dataReceivedTimestamp := time.Now().UnixMilli()
	// Check for authorisation
	if config.Config.RawBearer != "" {
		md, _ := metadata.FromIncomingContext(ctx)

		if auth := md.Get("authorization"); len(auth) == 0 || auth[0] != config.Config.RawBearer {
			return &pb.RawProtoResponse{Message: "Incorrect authorisation received"}, nil
		}
	}

	uuid := in.DeviceId
	account := in.Username
	level := int(in.TrainerLevel)
	scanContext := ""
	if in.ScanContext != nil {
		scanContext = *in.ScanContext
	}

	if in.Timestamp > 0 {
		dataReceivedTimestamp = in.Timestamp
	}

	latTarget, lonTarget := float64(in.LatTarget), float64(in.LonTarget)
	globalHaveAr := in.HaveAr
	var protoData []ProtoData

	for _, v := range in.Contents {
		inboundRawData := ProtoData{
			Method:      int(v.Method),
			Account:     account,
			Level:       level,
			ScanContext: scanContext,
			Lat:         latTarget,
			Lon:         lonTarget,
			Data:        v.ResponsePayload,
			Request:     v.RequestPayload,
			Uuid:        uuid,
			HaveAr: func() *bool {
				if v.HaveAr != nil {
					return v.HaveAr
				}
				return globalHaveAr
			}(),
			TimestampMs: dataReceivedTimestamp,
		}

		protoData = append(protoData, inboundRawData)
	}

	// Process each proto in a packet in sequence, but in a go-routine
	go func() {
		timeout := 5 * time.Second
		if config.Config.Tuning.ExtendedTimeout {
			timeout = 30 * time.Second
		}

		for _, entry := range protoData {
			// provide independent cancellation contexts for each proto decode
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			decode(ctx, entry.Method, &entry)
			cancel()
		}
	}()

	if latTarget != 0 && lonTarget != 0 && uuid != "" {
		UpdateDeviceLocation(uuid, latTarget, lonTarget, scanContext)
	}

	return &pb.RawProtoResponse{Message: "Processed"}, nil
}
