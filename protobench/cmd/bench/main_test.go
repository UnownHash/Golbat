package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

func TestRunSmoke(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ENCOUNTER"), 0o755); err != nil {
		t.Fatal(err)
	}
	enc := pogo.EncounterOutProto_builder{
		Pokemon: pogo.WildPokemonProto_builder{EncounterId: 9}.Build(),
	}.Build()
	raw, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ENCOUNTER", "1_10.bin"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := run(runConfig{
		corpusDir: dir,
		workers:   4,
		duration:  200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.decodes == 0 {
		t.Fatal("no decodes performed")
	}
	if rep.gcCPUShare < 0 || rep.gcCPUShare > 1 {
		t.Fatalf("gcCPUShare out of range: %v", rep.gcCPUShare)
	}
}
