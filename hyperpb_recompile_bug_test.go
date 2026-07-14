package main

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
)

// TestHyperpbRecompileRepeatedStringNoDuplication is a permanent regression
// guard for https://github.com/bufbuild/hyperpb-go/issues/39: a profile-guided
// Recompile once produced a parser that duplicated repeated-string field
// elements (image_url decoded with a spurious leading empty entry). Shadow
// verification caught it live on fort_details traffic; it was fixed upstream
// in hyperpb PR #40. This test asserts the recompiled parser stays correct, so
// a hyperpb downgrade or regression re-breaks CI rather than corrupting data
// (which would only surface as proto_engine.pgo=true silently duplicating
// strings under the recompiled parser).
func TestHyperpbRecompileRepeatedStringNoDuplication(t *testing.T) {
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

	if got := parseLen(recompiled); got != 1 {
		t.Fatalf("hyperpb Recompile repeated-string duplication REGRESSED "+
			"(image_url len=%d, want 1; issue #39): a hyperpb downgrade/regression "+
			"has re-broken profile-guided decoding — do NOT run proto_engine.pgo=true", got)
	}
}
