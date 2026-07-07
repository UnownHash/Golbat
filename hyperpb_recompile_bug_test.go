package main

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
)

// TestHyperpbRecompileRepeatedStringDuplication is a CANARY for the hyperpb
// v0.1.3 bug (https://github.com/bufbuild/hyperpb-go/issues/39) that
// made proto_engine.pgo default to false: a profile-guided
// Recompile produces a parser that duplicates repeated-string elements
// (image_url decodes with 2 entries instead of 1). Found live by shadow
// verification on fort_details traffic; the baseline compiled parser is
// unaffected.
//
// This test asserts the bug IS PRESENT. When a hyperpb upgrade fixes it,
// the test fails on purpose — that failure is the signal to delete this
// canary and flip the proto_engine.pgo default back to true
// (config/reader.go) for the measured ~4% decode win.
func TestHyperpbRecompileRepeatedStringDuplication(t *testing.T) {
	src := &pogo.FortDetailsOutProto{
		Id:       "FORT_TEST_1",
		Name:     "Test Fort",
		ImageUrl: []string{"https://example.com/one.png"},
		Team:     pogo.Team_TEAM_BLUE,
	}
	raw, err := proto.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	md := (*pogo.FortDetailsOutProto)(nil).ProtoReflect().Descriptor()
	imageFd := md.Fields().ByName("image_url")

	ty := hyperpb.CompileMessageDescriptor(md)

	parseLen := func(ty *hyperpb.MessageType) int {
		shared := new(hyperpb.Shared)
		defer shared.Free()
		msg := shared.NewMessage(ty)
		if err := msg.Unmarshal(raw); err != nil {
			t.Fatal(err)
		}
		return msg.Get(imageFd).List().Len()
	}

	if got := parseLen(ty); got != 1 {
		t.Fatalf("baseline parser broken: image_url len=%d, want 1", got)
	}

	profile := ty.NewProfile()
	shared := new(hyperpb.Shared)
	for i := 0; i < 300; i++ {
		msg := shared.NewMessage(ty)
		_ = msg.Unmarshal(raw, hyperpb.WithRecordProfile(profile, 1.0))
		shared.Free()
	}
	recompiled := ty.Recompile(profile)

	if got := parseLen(recompiled); got == 1 {
		t.Fatal("hyperpb Recompile repeated-string duplication appears FIXED upstream: " +
			"delete this canary and re-enable proto_engine.pgo by default (config/reader.go)")
	} else {
		t.Logf("canary: upstream bug still present (recompiled image_url len=%d, want 1) — proto_engine.pgo stays default-off", got)
	}
}
