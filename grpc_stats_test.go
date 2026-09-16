package main

import (
	"context"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"google.golang.org/grpc/stats"

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

// The logger is a pure function of the stats events it receives: fed one
// RPC's events directly it emits exactly one debug line with the method,
// both timings and the sizes, and ignores RPCs outside the API service.
func TestGrpcRPCLoggerFormatsOneLineFromEvents(t *testing.T) {
	withLogLevel(t, log.DebugLevel)
	hook := logtest.NewGlobal()
	defer hook.Reset()

	begin := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	var h grpcRPCLogger
	ctx := h.TagRPC(context.Background(), &stats.RPCTagInfo{FullMethodName: "/golbat_api.GolbatApi/ScanGyms"})
	h.HandleRPC(ctx, &stats.InHeader{Compression: "gzip"})
	h.HandleRPC(ctx, &stats.InPayload{WireLength: 27})
	h.HandleRPC(ctx, &stats.OutPayload{Length: 1000, WireLength: 405, SentTime: begin.Add(3 * time.Millisecond)})
	h.HandleRPC(ctx, &stats.End{BeginTime: begin, EndTime: begin.Add(5 * time.Millisecond)})

	// A non-API RPC gets no timing state and therefore no line.
	raw := h.TagRPC(context.Background(), &stats.RPCTagInfo{FullMethodName: "/raw_receiver.RawProto/SubmitRawProto"})
	h.HandleRPC(raw, &stats.End{BeginTime: begin, EndTime: begin.Add(time.Millisecond)})

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("got %d log entries, want exactly 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Level != log.DebugLevel {
		t.Errorf("level = %s, want debug", e.Level)
	}
	want := `[GRPC_RPC] /golbat_api.GolbatApi/ScanGyms total=5ms handler+marshal=3ms req_wire=27 resp_bytes=1000 resp_wire=405 compression="gzip" err=<nil>`
	if e.Message != want {
		t.Errorf("line = %q\nwant   %q", e.Message, want)
	}
}

// At debug level the handler is installed on the real server: one line per
// completed API RPC, none for raw ingest.
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
