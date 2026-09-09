package main

import (
	"context"

	"golbat/config"
	"golbat/decoder"
	pb "golbat/grpc"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip" // Install the gzip compressor
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// grpcApiServer is the gRPC counterpart of the HTTP /api scan endpoints.
// Authentication lives in apiAuthUnaryInterceptor; this type only validates
// the request shape, applies the fort_in_memory gate, and delegates to the
// decoder entry points that the HTTP handlers also use.
type grpcApiServer struct {
	pb.UnimplementedGolbatApiServer
}

// requireBoundingBox mirrors Huma's body validation: min and max are
// required on every scan request.
func requireBoundingBox(minLoc, maxLoc *pb.LatLon) error {
	if minLoc == nil || maxLoc == nil {
		return status.Error(codes.InvalidArgument, "min and max are required")
	}
	return nil
}

// requireFortInMemory mirrors the HTTP 503 on the fort scan routes. It is a
// configuration state, not a transient fault, hence FailedPrecondition
// rather than Unavailable (which tells clients to retry).
func requireFortInMemory() error {
	if !config.Config.FortInMemory {
		return status.Error(codes.FailedPrecondition, "fort_in_memory not enabled")
	}
	return nil
}

func (s *grpcApiServer) ScanPokemon(ctx context.Context, in *pb.PokemonScanRequest) (*pb.PokemonScanResponse, error) {
	if err := requireBoundingBox(in.GetMin(), in.GetMax()); err != nil {
		return nil, err
	}
	return decoder.GrpcScanPokemon(in), nil
}

func (s *grpcApiServer) GetPokemon(ctx context.Context, in *pb.GetPokemonRequest) (*pb.GetPokemonResponse, error) {
	return &pb.GetPokemonResponse{Pokemon: decoder.GrpcGetPokemon(in.GetEncounterIds())}, nil
}

func (s *grpcApiServer) ScanGyms(ctx context.Context, in *pb.FortScanRequest) (*pb.GymScanResponse, error) {
	if err := requireBoundingBox(in.GetMin(), in.GetMax()); err != nil {
		return nil, err
	}
	if err := requireFortInMemory(); err != nil {
		return nil, err
	}
	return decoder.GrpcScanGyms(in, dbDetails), nil
}

func (s *grpcApiServer) ScanPokestops(ctx context.Context, in *pb.FortScanRequest) (*pb.PokestopScanResponse, error) {
	if err := requireBoundingBox(in.GetMin(), in.GetMax()); err != nil {
		return nil, err
	}
	if err := requireFortInMemory(); err != nil {
		return nil, err
	}
	return decoder.GrpcScanPokestops(in, dbDetails), nil
}

func (s *grpcApiServer) ScanStations(ctx context.Context, in *pb.FortScanRequest) (*pb.StationScanResponse, error) {
	if err := requireBoundingBox(in.GetMin(), in.GetMax()); err != nil {
		return nil, err
	}
	if err := requireFortInMemory(); err != nil {
		return nil, err
	}
	return decoder.GrpcScanStations(in, dbDetails), nil
}

func (s *grpcApiServer) ScanForts(ctx context.Context, in *pb.FortCombinedScanRequest) (*pb.FortScanResponse, error) {
	if err := requireBoundingBox(in.GetMin(), in.GetMax()); err != nil {
		return nil, err
	}
	if err := requireFortInMemory(); err != nil {
		return nil, err
	}
	return decoder.GrpcScanForts(in, dbDetails), nil
}

// newGrpcServer builds the one gRPC server Golbat listens on: raw ingest,
// the GolbatApi service, and server reflection (so grpcurl/ghz work without
// the proto files). srvMetrics is nil when Prometheus is disabled. The
// api_secret interceptor runs after the metrics one so rejected calls are
// still counted.
func newGrpcServer(srvMetrics *grpcprom.ServerMetrics) *grpc.Server {
	var opts []grpc.ServerOption
	var unary []grpc.UnaryServerInterceptor
	if srvMetrics != nil {
		unary = append(unary, srvMetrics.UnaryServerInterceptor())
		opts = append(opts, grpc.StreamInterceptor(srvMetrics.StreamServerInterceptor()))
	}
	unary = append(unary, apiAuthUnaryInterceptor)
	opts = append(opts, grpc.ChainUnaryInterceptor(unary...))

	s := grpc.NewServer(opts...)
	pb.RegisterRawProtoServer(s, &grpcRawServer{})
	pb.RegisterGolbatApiServer(s, &grpcApiServer{})
	reflection.Register(s)
	if srvMetrics != nil {
		srvMetrics.InitializeMetrics(s)
	}
	return s
}
