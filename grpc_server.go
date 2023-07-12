package main

import (
	"context"
	"golbat/config"
	pb "golbat/grpc"
	"time"
)

// server is used to implement helloworld.GreeterServer.
type grpcServer struct {
	pb.UnimplementedRawProtoServer
}

func (s *grpcServer) SubmitRawProto(ctx context.Context, in *pb.RawProtoRequest) (*pb.RawProtoResponse, error) {

	//authHeader := r.Header.Get("Authorization")
	//if config.Config.RawBearer != "" {
	//	if authHeader != "Bearer "+config.Config.RawBearer {
	//		log.Errorf("Raw: Incorrect authorisation received (%s)", authHeader)
	//		return
	//	}
	//}

	uuid := in.DeviceId
	account := in.Username
	level := int(in.TrainerLevel)
	scanContext := ""
	if in.ScanContext != nil {
		scanContext = *in.ScanContext
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
