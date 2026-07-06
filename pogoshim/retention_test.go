package pogoshim_test

import (
	"fmt"
	"runtime"
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// buildLargeGmoPayload builds a GetMapObjectsOutProto with many forts, each
// carrying a long dummy ImageUrl, totalling roughly targetBytes of wire data.
// Every fort's ImageUrl is large; only fort 0's short FortId is ever read by
// the retention loop below. Because hyperpb backs every string getter with
// an unsafe.String view into the same per-Shared payload copy, retaining
// even that one small string is enough to keep the *entire* copy reachable
// if the getter doesn't clone -- regardless of which field the string came
// from.
func buildLargeGmoPayload(t *testing.T, targetBytes int) []byte {
	t.Helper()
	const dummyLen = 1500
	dummy := make([]byte, dummyLen)
	for i := range dummy {
		dummy[i] = byte('a' + i%26)
	}

	numForts := targetBytes / dummyLen
	if numForts < 1 {
		numForts = 1
	}

	forts := make([]*pogo.PokemonFortProto, 0, numForts)
	for i := 0; i < numForts; i++ {
		forts = append(forts, &pogo.PokemonFortProto{
			FortId:    fmt.Sprintf("FORT%06d", i), // exactly 10 bytes
			Latitude:  1.23,
			Longitude: 4.56,
			ImageUrl:  string(dummy),
		})
	}

	gmo := &pogo.GetMapObjectsOutProto{
		Status: pogo.GetMapObjectsOutProto_SUCCESS,
		MapCell: []*pogo.ClientMapCellProto{
			{S2CellId: 1, Fort: forts},
		},
	}
	raw, err := proto.Marshal(gmo)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if len(raw) < targetBytes {
		t.Fatalf("payload smaller than requested: got %d want >= %d", len(raw), targetBytes)
	}
	return raw
}

// TestStringGetterDoesNotPinArenaPayload is a regression test for the
// zero-copy string retention bug: hyperpb's protoreflect Value.String() for
// a StringKind field returns an unsafe.String view directly into the
// arena's per-parse payload copy (buf.build/go/hyperpb's internal
// Shared.Src, populated fresh on every Unmarshal call). Before the fix,
// pogoshim's generated getters returned that view as-is, so retaining any
// getter string anywhere in the codebase (fort tracker IDs, entity Set*
// fields, cache keys) transitively pinned the *entire* payload copy for as
// long as the retained string lived -- even though only a handful of bytes
// were actually wanted.
//
// This test parses a ~150KB synthetic GMO payload 500 times through fresh
// hyperpb Shared arenas, each time reading only fort 0's 10-byte FortId via
// the pogoshim getter, appending the returned string to a slice, and then
// freeing the arena. If getters don't clone, none of those 500 parses' ~150KB
// copies can ever be collected: heap growth should approach 500*150KB =
// ~75MB. If getters do clone (strings.Clone, this fix), only ~500*10 bytes
// of real payload survive, plus modest allocator/bookkeeping overhead.
//
// The 30MB bound is deliberately generous (a straight 2x+ margin over any
// plausible post-fix overhead, but far below the ~75MB pre-fix pin) to keep
// this test robust against GC scheduling and allocator slack rather than
// chasing an exact number.
func TestStringGetterDoesNotPinArenaPayload(t *testing.T) {
	const iterations = 500
	const targetPayloadBytes = 150_000
	const maxHeapGrowthBytes = 30 << 20 // 30MB

	payload := buildLargeGmoPayload(t, targetPayloadBytes)
	t.Logf("payload size: %d bytes, iterations: %d, naive pin would be ~%dMB",
		len(payload), iterations, len(payload)*iterations/(1<<20))

	ty := hyperpb.CompileMessageDescriptor((*pogo.GetMapObjectsOutProto)(nil).ProtoReflect().Descriptor())

	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	retained := make([]string, 0, iterations)
	for i := 0; i < iterations; i++ {
		shared := new(hyperpb.Shared)
		msg := shared.NewMessage(ty)
		if err := msg.Unmarshal(payload); err != nil {
			t.Fatalf("iteration %d: unmarshal: %v", i, err)
		}

		g := pogoshim.AsGetMapObjectsOutProto(msg.ProtoReflect())
		cells := g.GetMapCell()
		if cells.Len() == 0 {
			t.Fatalf("iteration %d: no map cells", i)
		}
		forts := cells.At(0).GetFort()
		if forts.Len() == 0 {
			t.Fatalf("iteration %d: no forts", i)
		}

		id := forts.At(0).GetFortId()
		if len(id) != 10 {
			t.Fatalf("iteration %d: expected a 10-byte fort id, got %q (%d bytes)", i, id, len(id))
		}
		retained = append(retained, id)

		shared.Free()
	}

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	var delta int64
	if after.HeapInuse > before.HeapInuse {
		delta = int64(after.HeapInuse - before.HeapInuse)
	}
	t.Logf("HeapInuse before=%d after=%d delta=%d (%.2fMB); bound=%dMB",
		before.HeapInuse, after.HeapInuse, delta, float64(delta)/(1<<20), maxHeapGrowthBytes/(1<<20))

	if delta > maxHeapGrowthBytes {
		t.Fatalf("heap grew by %.2fMB retaining %d small strings -- exceeds %dMB bound; "+
			"getters are likely aliasing the arena payload instead of cloning",
			float64(delta)/(1<<20), iterations, maxHeapGrowthBytes/(1<<20))
	}

	// Sanity: the retained strings must still be correct/live (and this
	// keeps `retained` reachable through the measurement above).
	if retained[0] != "FORT000000" || retained[iterations-1] != "FORT000000" {
		t.Fatalf("unexpected retained values: first=%q last=%q", retained[0], retained[iterations-1])
	}
	runtime.KeepAlive(retained)
}
