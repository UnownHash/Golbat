package decoder

import (
	"testing"

	"buf.build/go/hyperpb"
	"github.com/jellydator/ttlcache/v3"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// TestMapFortCacheFlow_SetViaGetMapForts_ConsumeViaFortDetails locks in Wave 3
// Task 3's flip of GET_MAP_FORTS onto the proto engine end-to-end at the
// cache layer: extract a mapFortSummary from a GetMapFortsOutProto_FortProto
// shim exactly like UpdateFortRecordWithGetMapFortsOutProto's cache-miss
// branch does, Set it into getMapFortsCache, free the arena backing the
// shim (simulating decodeWithArena returning), and then consume it through
// updateGymGetMapFortCache/updatePokestopGetMapFortCache -- the same
// cache-check every FORT_DETAILS/GYM_GET_INFO update runs. The summary must
// survive the arena's lifetime (it is a plain-value struct, never the shim
// or its underlying proto -- see mapFortSummary's doc comment in
// decoder/main.go), and both consumers must read the cached fields back
// correctly regardless of which engine produced the shim.
func TestMapFortCacheFlow_SetViaGetMapForts_ConsumeViaFortDetails(t *testing.T) {
	build := func(id string) *pogo.GetMapFortsOutProto_FortProto {
		return &pogo.GetMapFortsOutProto_FortProto{
			Id:        id,
			Name:      "Cached Fort",
			Latitude:  10.5,
			Longitude: -20.5,
			Image:     []*pogo.GetMapFortsOutProto_Image{{Url: "https://example.com/cached.png"}},
		}
	}

	runGym := func(name, id string, shim pogoshim.GetMapFortsOutProto_FortProto) {
		// Mirrors UpdateFortRecordWithGetMapFortsOutProto's caching branch:
		// extract while the shim (and any arena backing it) is alive, then
		// store only the plain-value summary.
		summary := mapFortSummaryFromShim(shim)
		getMapFortsCache.Set(id, summary, ttlcache.DefaultTTL)

		gym := &Gym{GymData: GymData{Id: id}}
		updateGymGetMapFortCache(gym, false)

		if got, want := gym.Name.ValueOrZero(), "Cached Fort"; got != want {
			t.Errorf("%s: gym.Name = %q, want %q", name, got, want)
		}
		if got, want := gym.Url.ValueOrZero(), "https://example.com/cached.png"; got != want {
			t.Errorf("%s: gym.Url = %q, want %q", name, got, want)
		}
		if got, want := gym.Lat, 10.5; got != want {
			t.Errorf("%s: gym.Lat = %v, want %v", name, got, want)
		}
		if got, want := gym.Lon, -20.5; got != want {
			t.Errorf("%s: gym.Lon = %v, want %v", name, got, want)
		}
		// updateGymGetMapFortCache deletes the entry once consumed.
		if item := getMapFortsCache.Get(id); item != nil {
			t.Errorf("%s: getMapFortsCache entry for %s should be consumed/deleted", name, id)
		}
	}

	runPokestop := func(name, id string, shim pogoshim.GetMapFortsOutProto_FortProto) {
		summary := mapFortSummaryFromShim(shim)
		getMapFortsCache.Set(id, summary, ttlcache.DefaultTTL)

		stop := &Pokestop{PokestopData: PokestopData{Id: id}}
		updatePokestopGetMapFortCache(stop)

		if got, want := stop.Name.ValueOrZero(), "Cached Fort"; got != want {
			t.Errorf("%s: pokestop.Name = %q, want %q", name, got, want)
		}
		if got, want := stop.Url.ValueOrZero(), "https://example.com/cached.png"; got != want {
			t.Errorf("%s: pokestop.Url = %q, want %q", name, got, want)
		}
		if item := getMapFortsCache.Get(id); item != nil {
			t.Errorf("%s: getMapFortsCache entry for %s should be consumed/deleted", name, id)
		}
	}

	// std
	stdIn := build("GYM_STD")
	runGym("std", "GYM_STD", pogoshim.AsGetMapFortsOutProto_FortProto(stdIn.ProtoReflect()))
	stdIn2 := build("STOP_STD")
	runPokestop("std", "STOP_STD", pogoshim.AsGetMapFortsOutProto_FortProto(stdIn2.ProtoReflect()))

	// hyperpb -- the shim (and its arena) is explicitly freed BEFORE the
	// cache is consumed, proving the summary struct doesn't retain it.
	ty := hyperpb.CompileMessageDescriptor((*pogo.GetMapFortsOutProto_FortProto)(nil).ProtoReflect().Descriptor())

	hyperIn := build("GYM_HYPER")
	raw, err := proto.Marshal(hyperIn)
	if err != nil {
		t.Fatal(err)
	}
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	summary := mapFortSummaryFromShim(pogoshim.AsGetMapFortsOutProto_FortProto(msg.ProtoReflect()))
	shared.Free() // arena freed BEFORE the cache Set/consume below
	getMapFortsCache.Set("GYM_HYPER", summary, ttlcache.DefaultTTL)
	gym := &Gym{GymData: GymData{Id: "GYM_HYPER"}}
	updateGymGetMapFortCache(gym, false)
	if got, want := gym.Name.ValueOrZero(), "Cached Fort"; got != want {
		t.Errorf("hyperpb (post-free): gym.Name = %q, want %q", got, want)
	}
	if got, want := gym.Url.ValueOrZero(), "https://example.com/cached.png"; got != want {
		t.Errorf("hyperpb (post-free): gym.Url = %q, want %q", got, want)
	}

	hyperIn2 := build("STOP_HYPER")
	raw2, err := proto.Marshal(hyperIn2)
	if err != nil {
		t.Fatal(err)
	}
	shared2 := new(hyperpb.Shared)
	msg2 := shared2.NewMessage(ty)
	if err := msg2.Unmarshal(raw2); err != nil {
		t.Fatal(err)
	}
	runPokestop("hyperpb", "STOP_HYPER", pogoshim.AsGetMapFortsOutProto_FortProto(msg2.ProtoReflect()))
	shared2.Free()
}
