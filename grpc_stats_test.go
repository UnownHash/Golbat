package main

import (
	"context"
	"strings"
	"testing"
	"time"

	logtest "github.com/sirupsen/logrus/hooks/test"

	pb "golbat/grpc"
)

// Every completed RPC produces exactly one [GRPC_RPC] line naming the method
// and carrying the response size, so an operator can line it up with the
// scan logs and the caller's own timing.
func TestGrpcRPCLoggerLogsOneLinePerRPC(t *testing.T) {
	withTestConfig(t, "", false)
	hook := logtest.NewGlobal()
	defer hook.Reset()

	client := pb.NewGolbatApiClient(startGrpcTestServer(t, nil))
	if _, err := client.ScanPokemon(context.Background(), &pb.PokemonScanRequest{Min: testBox.min, Max: testBox.max}); err != nil {
		t.Fatal(err)
	}

	// The End event fires after the status trailer is written, which can
	// trail the client's return by a scheduling tick.
	deadline := time.Now().Add(2 * time.Second)
	for {
		matches := 0
		for _, e := range hook.AllEntries() {
			if strings.HasPrefix(e.Message, "[GRPC_RPC] /golbat_api.GolbatApi/ScanPokemon ") {
				matches++
				for _, want := range []string{"total=", "handler+marshal=", "resp_bytes=", "resp_wire=", `compression=""`, "err=<nil>"} {
					if !strings.Contains(e.Message, want) {
						t.Errorf("log line lacks %q: %s", want, e.Message)
					}
				}
			}
		}
		if matches == 1 {
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
