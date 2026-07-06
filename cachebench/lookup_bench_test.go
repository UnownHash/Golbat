package cachebench

// Benchmarks for the PokemonLookup de-pointer proposal: the production scan
// path does pokemonLookupCache.Load(id) per candidate (15-20M/s measured,
// 14% of CPU flat) where the map value holds TWO POINTERS, so each
// candidate costs up to three dependent DRAM misses (map node -> lookup
// deref -> pvp deref). Inlining both structs by value should cut that to
// one node miss and remove 2 heap objects per entry (20M at production
// scale). These benches measure both the per-candidate scan cost and the
// GC mark-time delta.

import (
	"math/rand/v2"
	"runtime"
	"testing"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
)

// Mirrors decoder.PokemonLookup / PokemonPvpLookup exactly.
type lkPokemon struct {
	PokemonId          int16
	Form               int16
	HasEncounterValues bool
	Weather            int8
	Atk, Def, Sta      int8
	Level              int8
	Cp                 int16
	Gender             int8
	Xxs, Xxl           bool
	Iv                 int8
	Size               int8
}

type lkPvp struct{ Little, Great, Ultra int16 }

// Current layout: two pointers.
type ptrItem struct {
	L *lkPokemon
	P *lkPvp
}

// Proposed layout: inline values + validity flags.
type valItem struct {
	L      lkPokemon
	P      lkPvp
	HasPvp bool
}

const lkN = 10_000_000

func fillLk(r *rand.Rand) lkPokemon {
	return lkPokemon{
		PokemonId: int16(r.IntN(1000)), Form: int16(r.IntN(5)),
		HasEncounterValues: true, Weather: int8(r.IntN(8)),
		Atk: int8(r.IntN(16)), Def: int8(r.IntN(16)), Sta: int8(r.IntN(16)),
		Level: int8(r.IntN(35)), Cp: int16(r.IntN(4000)),
		Gender: int8(r.IntN(3)), Iv: int8(r.IntN(101)), Size: int8(r.IntN(5)),
	}
}

// simulate the DNF pre-filter read pattern: id/form probe + a few field reads
func matchPtr(it ptrItem) bool {
	l := it.L
	if l == nil {
		return false
	}
	if l.Iv < 90 && l.Cp < 2500 && l.Level < 30 {
		return false
	}
	if it.P != nil && it.P.Great > 0 && it.P.Great <= 100 {
		return true
	}
	return l.Iv >= 90
}

func matchVal(it valItem) bool {
	l := &it.L
	if l.Iv < 90 && l.Cp < 2500 && l.Level < 30 {
		return false
	}
	if it.HasPvp && it.P.Great > 0 && it.P.Great <= 100 {
		return true
	}
	return l.Iv >= 90
}

func buildLookupMaps() (*xsync.Map[uint64, ptrItem], *xsync.Map[uint64, valItem], []uint64) {
	r := rand.New(rand.NewPCG(1, 2))
	keys := make([]uint64, lkN)
	pm := xsync.NewMap[uint64, ptrItem](xsync.WithPresize(lkN))
	vm := xsync.NewMap[uint64, valItem](xsync.WithPresize(lkN))
	// Two passes, mirroring production: PokemonLookup is allocated at first
	// save; PokemonPvpLookup arrives LATER (on encounter) via a separate
	// update. Allocating them in one loop iteration puts the pair on one
	// cache line (bump allocator) and flatters the pointer layout with
	// locality production does not have.
	for i := range keys {
		k := r.Uint64() // encounter ids are random uint64s
		keys[i] = k
		l := fillLk(r)
		pm.Store(k, ptrItem{L: &l})
		vm.Store(k, valItem{L: l})
	}
	for _, k := range keys {
		p := lkPvp{Little: int16(r.IntN(500)), Great: int16(r.IntN(500)), Ultra: int16(r.IntN(500))}
		if it, ok := pm.Load(k); ok {
			it.P = &p
			pm.Store(k, it)
		}
		if it, ok := vm.Load(k); ok {
			it.P = p
			it.HasPvp = true
			vm.Store(k, it)
		}
	}
	// shuffle scan order so access is random like production encounter ids
	r.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	return pm, vm, keys
}

var lkPM *xsync.Map[uint64, ptrItem]
var lkVM *xsync.Map[uint64, valItem]
var lkKeys []uint64

func ensureLookupMaps(b *testing.B) {
	if lkPM == nil {
		b.Logf("building %d-entry maps...", lkN)
		lkPM, lkVM, lkKeys = buildLookupMaps()
	}
}

func BenchmarkLookupScanPtr(b *testing.B) {
	ensureLookupMaps(b)
	b.ResetTimer()
	matched := 0
	for i := 0; i < b.N; i++ {
		it, ok := lkPM.Load(lkKeys[i%len(lkKeys)])
		if ok && matchPtr(it) {
			matched++
		}
	}
	_ = matched
}

func BenchmarkLookupScanVal(b *testing.B) {
	ensureLookupMaps(b)
	b.ResetTimer()
	matched := 0
	for i := 0; i < b.N; i++ {
		it, ok := lkVM.Load(lkKeys[i%len(lkKeys)])
		if ok && matchVal(it) {
			matched++
		}
	}
	_ = matched
}

// Parallel variants: production scans run concurrently with decode.
func BenchmarkLookupScanPtrParallel(b *testing.B) {
	ensureLookupMaps(b)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := rand.IntN(len(lkKeys))
		for pb.Next() {
			it, ok := lkPM.Load(lkKeys[i%len(lkKeys)])
			if ok {
				matchPtr(it)
			}
			i++
		}
	})
}

func BenchmarkLookupScanValParallel(b *testing.B) {
	ensureLookupMaps(b)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := rand.IntN(len(lkKeys))
		for pb.Next() {
			it, ok := lkVM.Load(lkKeys[i%len(lkKeys)])
			if ok {
				matchVal(it)
			}
			i++
		}
	})
}

// GC mark cost: the ptr layout adds 2 heap objects per entry (20M total).
func TestLookupGCMarkDelta(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	measure := func(build func()) time.Duration {
		runtime.GC()
		build()
		runtime.GC() // settle
		start := time.Now()
		for i := 0; i < 3; i++ {
			runtime.GC()
		}
		return time.Since(start) / 3
	}

	r := rand.New(rand.NewPCG(3, 4))
	var pm *xsync.Map[uint64, ptrItem]
	ptrGC := measure(func() {
		pm = xsync.NewMap[uint64, ptrItem](xsync.WithPresize(lkN))
		for i := 0; i < lkN; i++ {
			l := fillLk(r)
			p := lkPvp{Great: int16(r.IntN(500))}
			pm.Store(r.Uint64(), ptrItem{L: &l, P: &p})
		}
	})
	t.Logf("GC cycle with ptr layout (10M entries + 20M pointees): %v", ptrGC)
	runtime.KeepAlive(pm)
	pm = nil
	runtime.GC()

	var vm *xsync.Map[uint64, valItem]
	valGC := measure(func() {
		vm = xsync.NewMap[uint64, valItem](xsync.WithPresize(lkN))
		for i := 0; i < lkN; i++ {
			vm.Store(r.Uint64(), valItem{L: fillLk(r), HasPvp: true})
		}
	})
	t.Logf("GC cycle with val layout (10M entries, zero pointees): %v", valGC)
	runtime.KeepAlive(vm)
	t.Logf("delta per GC cycle: %v (%.1fx)", ptrGC-valGC, float64(ptrGC)/float64(valGC))
}
