package main

import (
	"context"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"

	pb "golbat/grpc"
)

// withLogLevel sets the global logrus level for one test.
func withLogLevel(t *testing.T, level log.Level) {
	t.Helper()
	prev := log.GetLevel()
	log.SetLevel(level)
	t.Cleanup(func() { log.SetLevel(prev) })
}

// At info level the per-RPC stats handler is not installed at all, so
// production servers pay nothing for it and emit no [GRPC_RPC] lines.
func TestGrpcRPCLoggerNotInstalledBelowDebug(t *testing.T) {
	withTestConfig(t, "", false)
	withLogLevel(t, log.InfoLevel)
	hook := logtest.NewGlobal()
	defer hook.Reset()

	if grpcRPCLoggingEnabled() {
		t.Fatal("rpc logging must be disabled at info level")
	}
	conn := startGrpcTestServer(t, nil)
	if _, err := pb.NewGolbatApiClient(conn).ScanPokemon(context.Background(), &pb.PokemonScanRequest{Min: testBox.min, Max: testBox.max}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // give a stray End event time to land
	for _, e := range hook.AllEntries() {
		if strings.Contains(e.Message, "[GRPC_RPC]") {
			t.Fatalf("no [GRPC_RPC] line expected at info level, got: %s", e.Message)
		}
	}
}

// At debug level every completed API RPC produces exactly one [GRPC_RPC]
// debug line naming the method and carrying the response size, so an
// operator can line it up with the scan logs and the caller's own timing.
func TestGrpcRPCLoggerLogsOneLinePerRPC(t *testing.T) {
	withTestConfig(t, "", false)
	withLogLevel(t, log.DebugLevel)
	hook := logtest.NewGlobal()
	defer hook.Reset()

	conn := startGrpcTestServer(t, nil)
	client := pb.NewGolbatApiClient(conn)
	if _, err := client.ScanPokemon(context.Background(), &pb.PokemonScanRequest{Min: testBox.min, Max: testBox.max}); err != nil {
		t.Fatal(err)
	}
	// Raw ingest is high-volume and must not produce a line.
	if _, err := pb.NewRawProtoClient(conn).SubmitRawProto(context.Background(), &pb.RawProtoRequest{}); err != nil {
		t.Fatal(err)
	}

	// The End event fires after the status trailer is written, which can
	// trail the client's return by a scheduling tick.
	deadline := time.Now().Add(2 * time.Second)
	for {
		matches := 0
		for _, e := range hook.AllEntries() {
			if strings.Contains(e.Message, "[GRPC_RPC] /raw_receiver.RawProto/") {
				t.Fatalf("raw ingest must not be logged: %s", e.Message)
			}
			if strings.HasPrefix(e.Message, "[GRPC_RPC] /golbat_api.GolbatApi/ScanPokemon ") {
				matches++
				if e.Level != log.DebugLevel {
					t.Errorf("[GRPC_RPC] must log at debug, got %s", e.Level)
				}
				for _, want := range []string{"total=", "handler+marshal=", "resp_bytes=", "resp_wire=", `compression=""`, "err=<nil>"} {
					if !strings.Contains(e.Message, want) {
						t.Errorf("log line lacks %q: %s", want, e.Message)
					}
				}
			}
		}
		if matches == 1 {
			// Give a stray raw line a moment to appear before declaring victory.
			time.Sleep(50 * time.Millisecond)
			for _, e := range hook.AllEntries() {
				if strings.Contains(e.Message, "[GRPC_RPC] /raw_receiver.RawProto/") {
					t.Fatalf("raw ingest must not be logged: %s", e.Message)
				}
			}
			return
		}
		if matches > 1 {
			t.Fatalf("expected one [GRPC_RPC] line for the call, got %d", matches)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no [GRPC_RPC] line for ScanPokemon within 2s; entries: %d", len(hook.AllEntries()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
