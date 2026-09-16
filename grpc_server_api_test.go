package main

import (
	"context"
	"net"
	"testing"

	"golbat/config"
	pb "golbat/grpc"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// startGrpcTestServer serves the production server construction
// (newGrpcServer) over an in-memory listener and returns a connected client
// connection. No database: every scan hits an empty in-memory index.
// srvMetrics is passed straight through to newGrpcServer; nil (as every
// caller but the Prometheus-chain test passes) means Prometheus is disabled.
func startGrpcTestServer(t *testing.T, srvMetrics *grpcprom.ServerMetrics) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := newGrpcServer(srvMetrics)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	})
	return conn
}

func withTestConfig(t *testing.T, secret string, fortInMemory bool) {
	t.Helper()
	prevSecret, prevFim := config.Config.ApiSecret, config.Config.FortInMemory
	prevMaxP, prevMaxF := config.Config.Tuning.MaxPokemonResults, config.Config.Tuning.MaxFortResults
	config.Config.ApiSecret, config.Config.FortInMemory = secret, fortInMemory
	config.Config.Tuning.MaxPokemonResults, config.Config.Tuning.MaxFortResults = 3000, 9000
	t.Cleanup(func() {
		config.Config.ApiSecret, config.Config.FortInMemory = prevSecret, prevFim
		config.Config.Tuning.MaxPokemonResults, config.Config.Tuning.MaxFortResults = prevMaxP, prevMaxF
	})
}

var testBox = struct{ min, max *pb.LatLon }{&pb.LatLon{Lat: 0, Lon: 0}, &pb.LatLon{Lat: 1, Lon: 1}}

func TestGrpcApiAuthEndToEnd(t *testing.T) {
	withTestConfig(t, "topsecret", true)
	client := pb.NewGolbatApiClient(startGrpcTestServer(t, nil))
	req := &pb.PokemonScanRequest{Min: testBox.min, Max: testBox.max}

	cases := []struct {
		name string
		md   metadata.MD
		code codes.Code
	}{
		{"no metadata", nil, codes.Unauthenticated},
		{"wrong secret", metadata.Pairs("x-golbat-secret", "nope"), codes.Unauthenticated},
		{"x-golbat-secret", metadata.Pairs("x-golbat-secret", "topsecret"), codes.OK},
		{"authorization bare", metadata.Pairs("authorization", "topsecret"), codes.OK},
		{"authorization bearer", metadata.Pairs("authorization", "Bearer topsecret"), codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.md != nil {
				ctx = metadata.NewOutgoingContext(ctx, tc.md)
			}
			_, err := client.ScanPokemon(ctx, req)
			if got := status.Code(err); got != tc.code {
				t.Errorf("code = %v (%v), want %v", got, err, tc.code)
			}
		})
	}

	t.Run("raw service is not gated by the api secret", func(t *testing.T) {
		raw := pb.NewRawProtoClient(startGrpcTestServer(t, nil))
		resp, err := raw.SubmitRawProto(context.Background(), &pb.RawProtoRequest{})
		if err != nil {
			t.Fatalf("SubmitRawProto without api secret must reach the handler, got %v", err)
		}
		if resp.GetMessage() != "Processed" {
			t.Errorf("raw response = %q, want Processed (raw_bearer is empty)", resp.GetMessage())
		}
	})

	t.Run("auth still applies with the prometheus interceptor in the chain", func(t *testing.T) {
		promClient := pb.NewGolbatApiClient(startGrpcTestServer(t, grpcprom.NewServerMetrics()))
		_, err := promClient.ScanPokemon(context.Background(), req)
		if got := status.Code(err); got != codes.Unauthenticated {
			t.Errorf("code = %v (%v), want Unauthenticated", got, err)
		}
	})
}

func TestGrpcApiScansWithoutSecret(t *testing.T) {
	withTestConfig(t, "", true)
	client := pb.NewGolbatApiClient(startGrpcTestServer(t, nil))
	ctx := context.Background()

	t.Run("pokemon scan on an empty index", func(t *testing.T) {
		resp, err := client.ScanPokemon(ctx, &pb.PokemonScanRequest{Min: testBox.min, Max: testBox.max, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Pokemon) != 0 || resp.LimitReached {
			t.Errorf("resp = %+v, want no pokemon and limit_reached=false", resp)
		}
	})

	t.Run("get pokemon by unknown id is empty", func(t *testing.T) {
		resp, err := client.GetPokemon(ctx, &pb.GetPokemonRequest{EncounterIds: []uint64{1, 2}})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Pokemon) != 0 {
			t.Errorf("got %d pokemon for unknown ids", len(resp.Pokemon))
		}
	})

	t.Run("get pokemon is capped at max_pokemon_results", func(t *testing.T) {
		tooMany := make([]uint64, config.Config.Tuning.MaxPokemonResults+1)
		if _, err := client.GetPokemon(ctx, &pb.GetPokemonRequest{EncounterIds: tooMany}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%d ids: code = %v, want InvalidArgument", len(tooMany), status.Code(err))
		}

		atCap := make([]uint64, config.Config.Tuning.MaxPokemonResults)
		resp, err := client.GetPokemon(ctx, &pb.GetPokemonRequest{EncounterIds: atCap})
		if err != nil {
			t.Fatalf("%d ids (at cap): unexpected error %v", len(atCap), err)
		}
		if len(resp.Pokemon) != 0 {
			t.Errorf("got %d pokemon for unknown ids at cap", len(resp.Pokemon))
		}
	})

	t.Run("fort scans on an empty index", func(t *testing.T) {
		fortReq := &pb.FortScanRequest{Min: testBox.min, Max: testBox.max}
		if resp, err := client.ScanGyms(ctx, fortReq); err != nil || len(resp.Gyms) != 0 || resp.LimitReached {
			t.Errorf("gyms: %v / %+v", err, resp)
		}
		if resp, err := client.ScanPokestops(ctx, fortReq); err != nil || len(resp.Pokestops) != 0 {
			t.Errorf("pokestops: %v / %+v", err, resp)
		}
		if resp, err := client.ScanStations(ctx, fortReq); err != nil || len(resp.Stations) != 0 {
			t.Errorf("stations: %v / %+v", err, resp)
		}
		combined, err := client.ScanForts(ctx, &pb.FortCombinedScanRequest{Min: testBox.min, Max: testBox.max, Gyms: &pb.FortTypeScanGroup{}})
		if err != nil || len(combined.Gyms)+len(combined.Pokestops)+len(combined.Stations) != 0 {
			t.Errorf("combined: %v / %+v", err, combined)
		}
	})

	t.Run("missing bounding box is InvalidArgument", func(t *testing.T) {
		_, err := client.ScanPokemon(ctx, &pb.PokemonScanRequest{Max: testBox.max})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("pokemon: code = %v, want InvalidArgument", status.Code(err))
		}
		_, err = client.ScanGyms(ctx, &pb.FortScanRequest{Min: testBox.min})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("gyms: code = %v, want InvalidArgument", status.Code(err))
		}
		_, err = client.ScanForts(ctx, &pb.FortCombinedScanRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("combined: code = %v, want InvalidArgument", status.Code(err))
		}
	})
}

func TestGrpcApiFortScansRequireFortInMemory(t *testing.T) {
	withTestConfig(t, "", false)
	client := pb.NewGolbatApiClient(startGrpcTestServer(t, nil))
	ctx := context.Background()
	fortReq := &pb.FortScanRequest{Min: testBox.min, Max: testBox.max}

	if _, err := client.ScanGyms(ctx, fortReq); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("gyms: code = %v, want FailedPrecondition", status.Code(err))
	}
	if _, err := client.ScanPokestops(ctx, fortReq); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("pokestops: code = %v, want FailedPrecondition", status.Code(err))
	}
	if _, err := client.ScanStations(ctx, fortReq); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("stations: code = %v, want FailedPrecondition", status.Code(err))
	}
	if _, err := client.ScanForts(ctx, &pb.FortCombinedScanRequest{Min: testBox.min, Max: testBox.max}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("combined: code = %v, want FailedPrecondition", status.Code(err))
	}
	// Pokemon scans do not depend on fort_in_memory.
	if _, err := client.ScanPokemon(ctx, &pb.PokemonScanRequest{Min: testBox.min, Max: testBox.max}); err != nil {
		t.Errorf("pokemon scan must work without fort_in_memory: %v", err)
	}
}
