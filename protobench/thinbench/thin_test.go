package thinbench

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
	"protobench/pogothin"
)

var opts = proto.UnmarshalOptions{DiscardUnknown: true}

func corpus(tb testing.TB) [][]byte {
	dir := os.Getenv("CORPUS")
	if dir == "" {
		dir = "../../corpus-frozen/GET_MAP_OBJECTS"
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		tb.Skipf("no corpus at %s: %v", dir, err)
	}
	var out [][]byte
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".bin") {
			b, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				tb.Fatal(err)
			}
			out = append(out, b)
		}
	}
	return out
}

// digestFull / digestThin walk the SAME read-field set readers.go reads,
// against the full and thinned schemas respectively. If digestThin compiles,
// every read field survived thinning; if the digests match per payload, the
// thinned schema decodes those fields to identical values.
func digestFull(m *pogo.GetMapObjectsOutProto) uint64 {
	h := fnv.New64a()
	var b [8]byte
	put := func(v uint64) {
		b[0] = byte(v)
		b[1] = byte(v >> 8)
		b[2] = byte(v >> 16)
		b[3] = byte(v >> 24)
		b[4] = byte(v >> 32)
		b[5] = byte(v >> 40)
		b[6] = byte(v >> 48)
		b[7] = byte(v >> 56)
		h.Write(b[:])
	}
	for _, cell := range m.GetMapCell() {
		put(cell.GetS2CellId())
		put(uint64(len(cell.GetFort())))
		for _, f := range cell.GetFort() {
			h.Write([]byte(f.GetFortId()))
			put(uint64(int64(f.GetLatitude() * 1e5)))
			put(uint64(f.GetTeam()))
			put(uint64(f.GetGuardPokemonId()))
			if ri := f.GetRaidInfo(); ri != nil {
				put(uint64(ri.GetRaidEndMs()))
				put(uint64(ri.GetRaidLevel()))
				if rp := ri.GetRaidPokemon(); rp != nil {
					put(uint64(rp.GetPokemonId()))
				}
			}
		}
		for _, w := range cell.GetWildPokemon() {
			put(w.GetEncounterId())
			if p := w.GetPokemon(); p != nil {
				put(uint64(p.GetCp()))
				put(uint64(p.GetIndividualAttack()))
				if d := p.GetPokemonDisplay(); d != nil {
					put(uint64(d.GetForm()))
				}
			}
		}
	}
	for _, cw := range m.GetClientWeather() {
		put(uint64(cw.GetS2CellId()))
		if gw := cw.GetGameplayWeather(); gw != nil {
			put(uint64(gw.GetGameplayCondition()))
		}
	}
	return h.Sum64()
}

func digestThin(m *pogothin.GetMapObjectsOutProto) uint64 {
	h := fnv.New64a()
	var b [8]byte
	put := func(v uint64) {
		b[0] = byte(v)
		b[1] = byte(v >> 8)
		b[2] = byte(v >> 16)
		b[3] = byte(v >> 24)
		b[4] = byte(v >> 32)
		b[5] = byte(v >> 40)
		b[6] = byte(v >> 48)
		b[7] = byte(v >> 56)
		h.Write(b[:])
	}
	for _, cell := range m.GetMapCell() {
		put(cell.GetS2CellId())
		put(uint64(len(cell.GetFort())))
		for _, f := range cell.GetFort() {
			h.Write([]byte(f.GetFortId()))
			put(uint64(int64(f.GetLatitude() * 1e5)))
			put(uint64(f.GetTeam()))
			put(uint64(f.GetGuardPokemonId()))
			if ri := f.GetRaidInfo(); ri != nil {
				put(uint64(ri.GetRaidEndMs()))
				put(uint64(ri.GetRaidLevel()))
				if rp := ri.GetRaidPokemon(); rp != nil {
					put(uint64(rp.GetPokemonId()))
				}
			}
		}
		for _, w := range cell.GetWildPokemon() {
			put(w.GetEncounterId())
			if p := w.GetPokemon(); p != nil {
				put(uint64(p.GetCp()))
				put(uint64(p.GetIndividualAttack()))
				if d := p.GetPokemonDisplay(); d != nil {
					put(uint64(d.GetForm()))
				}
			}
		}
	}
	for _, cw := range m.GetClientWeather() {
		put(uint64(cw.GetS2CellId()))
		if gw := cw.GetGameplayWeather(); gw != nil {
			put(uint64(gw.GetGameplayCondition()))
		}
	}
	return h.Sum64()
}

func TestThinPreservesReadFields(t *testing.T) {
	c := corpus(t)
	for i, p := range c {
		var full pogo.GetMapObjectsOutProto
		if err := opts.Unmarshal(p, &full); err != nil {
			t.Fatalf("full %d: %v", i, err)
		}
		var thin pogothin.GetMapObjectsOutProto
		if err := opts.Unmarshal(p, &thin); err != nil {
			t.Fatalf("thin %d: %v", i, err)
		}
		if df, dt := digestFull(&full), digestThin(&thin); df != dt {
			t.Fatalf("payload %d: read-field digest differs full=%x thin=%x", i, df, dt)
		}
	}
	t.Logf("OK: thinned schema decodes identical read-field values on %d GMO payloads", len(c))
}

func BenchmarkGMOFull(b *testing.B) {
	c := corpus(b)
	b.ReportAllocs()
	var sink uint64
	for i := 0; i < b.N; i++ {
		var m pogo.GetMapObjectsOutProto
		if err := opts.Unmarshal(c[i%len(c)], &m); err != nil {
			b.Fatal(err)
		}
		sink += digestFull(&m)
	}
	_ = sink
}

func BenchmarkGMOThin(b *testing.B) {
	c := corpus(b)
	b.ReportAllocs()
	var sink uint64
	for i := 0; i < b.N; i++ {
		var m pogothin.GetMapObjectsOutProto
		if err := opts.Unmarshal(c[i%len(c)], &m); err != nil {
			b.Fatal(err)
		}
		sink += digestThin(&m)
	}
	_ = sink
}

// BenchmarkGMOThinNoDiscard proves thinning and DiscardUnknown are a pair:
// without DiscardUnknown, protobuf-go retains each skipped (now-unknown) blob
// in the message's unknown-fields buffer — reintroducing the allocation
// thinning was meant to remove.
var optsKeepUnknown = proto.UnmarshalOptions{DiscardUnknown: false}

func BenchmarkGMOThinNoDiscard(b *testing.B) {
	c := corpus(b)
	b.ReportAllocs()
	var sink uint64
	for i := 0; i < b.N; i++ {
		var m pogothin.GetMapObjectsOutProto
		if err := optsKeepUnknown.Unmarshal(c[i%len(c)], &m); err != nil {
			b.Fatal(err)
		}
		sink += digestThin(&m)
	}
	_ = sink
}
