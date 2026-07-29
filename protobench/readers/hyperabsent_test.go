package readers

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

// Fused Get-only presence detection is safe iff Get on an absent message
// field returns an invalid (empty read-only) view without allocating.
func TestHyperGetAbsentMessageSemantics(t *testing.T) {
	fort := pogo.PokemonFortProto_builder{FortId: "f"}.Build() // no raid_info
	raw, _ := proto.Marshal(fort)
	ty := hyperpb.CompileMessageDescriptor(fortMD)
	shared := new(hyperpb.Shared)
	defer shared.Free()
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	if msg.Has(fdFortRaidInfo) {
		t.Fatal("raid_info unexpectedly present")
	}
	v := msg.Get(fdFortRaidInfo).Message()
	t.Logf("absent message: IsValid=%v", v.IsValid())
	allocs := testing.AllocsPerRun(100, func() {
		m := msg.Get(fdFortRaidInfo).Message()
		if m.IsValid() {
			t.Fatal("became valid")
		}
	})
	t.Logf("allocs per absent Get+IsValid: %.1f", allocs)
}
