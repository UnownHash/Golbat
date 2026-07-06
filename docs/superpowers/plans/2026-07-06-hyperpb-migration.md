# Hyperpb Decode Engine Migration (Waves 0–2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decode GMO, Encounter, and DiskEncounter client protos with hyperpb (arena-reused, PGO-recompiled) behind generated typed shims, on by default with std fallback and sampled shadow verification — on branch `perf/hyperpb-decode` for handoff to a production user.

**Architecture:** One accessor surface: decoder functions take `pogoshim.X` wrapper types (generated, protoc-getter-style, backed by protoreflect). The hyperpb engine wraps arena-parsed messages; the std fallback wraps ordinary `pogo` structs via `.ProtoReflect()` — same call graph either way, so partial migration is always compile-green (unmigrated callers wrap their pogo values at the call site). Engine choice, PGO warmup, and shadow sampling are config.

**Tech Stack:** Go 1.26, `buf.build/go/hyperpb` (pin the version from `git show perf/proto-decode-phase0:protobench/go.mod`), `google.golang.org/protobuf` (already present), existing `stats_collector` metrics pattern.

## Global Constraints

- Work ONLY in `/Users/james/GolandProjects/golbat-wt/hyperpb-decode` (branch `perf/hyperpb-decode`). The proto-opaque-gc worktree and other branches are read-only references (`git show perf/proto-decode-phase0:<path>` works from this worktree).
- Spec of record: `git show perf/proto-decode-phase0:docs/superpowers/specs/2026-07-06-hyperpb-migration-design.md`, with three approved deltas: (1) cherry-pick foundation only — no protobench module, no capture hook on this branch; (2) engine defaults ON (`gmo`/`encounter`/`disk_encounter` = `"hyperpb"`); (3) `diskEncounterCache` retention fixed by caching raw payload bytes.
- Client protos are read-only. **No `pogoshim` value and no hyperpb message may be stored in a struct field, package var, cache, channel, or captured by a goroutine that outlives the decode call.** Every task touching decoder code re-checks this.
- Arena lifetime: freed exactly when the top-level decode helper returns; all batch processing stays synchronous inside it.
- hyperpb glue files carry `//go:build amd64 || arm64`; a stub file (`//go:build !amd64 && !arm64`) forces std.
- `pogoshim/` generated code IS committed (like `pogo/vbase.pb.go`).
- Config defaults: `proto_engine.gmo = "hyperpb"`, `proto_engine.encounter = "hyperpb"`, `proto_engine.disk_encounter = "hyperpb"`, `proto_engine.shadow_sample_rate = 0.01`, `proto_engine.pgo = true`, PGO warmup sample = 256 packets/method.
- `gofmt -l` empty on changed files and `go vet ./...` clean before every commit; `go build ./... && go test ./...` green at every task end.
- Baseline: `ed50bf8` builds and tests clean (verified).

## Migration surface inventory (from branch recon — authoritative for Tasks 4–7)

**Encounter path:** `decode.go:378 decodeEncounter`, `decode.go:397 decodeDiskEncounter` → `decoder/pokemon_process.go:15 UpdatePokemonRecordWithEncounterProto`, `:38 UpdatePokemonRecordWithDiskEncounterProto` → `decoder/pokemon_decode.go:776 updatePokemonFromEncounterProto`, `:796 updatePokemonFromDiskEncounterProto`, `:104 addWildPokemon`, `:701 addEncounterPokemon`, `:833 setPokemonDisplay`.
**Retention (Wave 1):** `decoder/main.go:72 diskEncounterCache ttlcache.Cache[uint64, *pogo.DiskEncounterOutProto]`, set `pokemon_process.go:56`, consumed `gmo_decode.go:189-193`.
**GMO path:** `decode.go:465 decodeGMO`, `:584 isCellNotEmpty`; Raw carriers `decoder/main.go:25-52`; batches `gmo_decode.go:13 UpdateFortBatch`, `:114 UpdateStationBatch`, `:129 UpdatePokemonBatch`, `:202 UpdateClientWeatherBatch`; transitive: `gym_decode.go:24 calculatePowerUpPoints`, `:48 updateGymFromFort`; `pokestop_decode.go:18 updatePokestopFromFort`; `incident_decode.go:10 updateFromPokestopIncidentDisplay`; `pokemon_decode.go:123 wildSignificantUpdate`, `:139 nearbySignificantUpdate`, `:167 updateFromWild`, `:179 updateFromMap`, `:238 updateFromNearby`; `spawnpoint.go:252 spawnpointUpdateFromWild`; `station_decode.go:13 updateFromStationProto`; `station_battle.go:161 syncStationBattlesFromProto`, `:175 stationBattleFromProto`; `weather.go:304 updateWeatherFromClientWeatherProto`; `weather_consensus.go:48 applyObservation` (+ retained `LastObsByCondition map[int32]*pogo.ClientWeatherProto`).
**Not pogo-message-typed (no change):** `weather map[int64]pogo.GameplayWeatherProto_WeatherCondition` enum maps (`gmo_decode.go:130`, `weather_iv.go:100`); `FortTrackerGMOContents` (strings/ints); `WeatherUpdate`.
**Out of scope (Wave 3):** `getMapFortsCache`, tappables, battle-state, push-gateway paths.
**Mutation:** none anywhere on these paths (verified).

---

### Task 1: `pogoshim` generator + generated package

**Files:**
- Create: `cmd/pogoshimgen/main.go` (adapted copy)
- Create: `scripts/genshim.sh`
- Create: `pogoshim/pogoshim.gen.go` (generated, committed)
- Test: `pogoshim/roundtrip_test.go`

**Interfaces:**
- Produces: package `pogoshim` — for every message in the closure of the roots: `type <M> struct{ m protoreflect.Message }`, `As<M>(protoreflect.Message) <M>`, `(x <M>) IsZero() bool`, protoc-named getters (`GetFortId() string`, enums typed as `pogo.<Enum>`, float32 fields return `float32`), `Has<F>()` for message fields, message getters returning zero shims when absent (single Get+IsValid call), repeated-message getters returning `<Elem>List` with `Len()/At(i)/All() iter.Seq[<Elem>]`, repeated scalars returning `ScalarList`.
- Roots: `GetMapObjectsOutProto,EncounterOutProto,DiskEncounterOutProto`.

- [ ] **Step 1: Copy the v2 generator from the sibling branch**

```bash
cd /Users/james/GolandProjects/golbat-wt/hyperpb-decode
mkdir -p cmd/pogoshimgen pogoshim scripts
git show perf/proto-decode-phase0:protobench/cmd/hyperpbgen/main.go > cmd/pogoshimgen/main.go
```

- [ ] **Step 2: Adapt it to golbat**

Three changes, everything else stays:

1. Root resolution — replace the hardcoded `rootDescs` map with registry lookup (roots live in `POGOProtos.Rpc`, registered by importing `golbat/pogo`):

```go
import (
	// ...existing imports...
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "golbat/pogo"
)

func rootDescriptor(name string) (protoreflect.MessageDescriptor, error) {
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName("POGOProtos.Rpc." + name))
	if err != nil {
		return nil, err
	}
	md, ok := d.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a message", name)
	}
	return md, nil
}
```

and in `main()` resolve each `-roots` entry through `rootDescriptor`, erroring out with the name on failure.

2. Defaults: `-out pogoshim/pogoshim.gen.go`, `-pkg pogoshim`, `-descpkg golbat/pogo`, `-roots GetMapObjectsOutProto,EncounterOutProto,DiskEncounterOutProto`.

3. The generated file's fd-var initializers reference `(*pogo.<M>)(nil).ProtoReflect().Descriptor()` — that works only for TOP-LEVEL messages of the descriptor package. Nested messages (Go name `Parent_Child`) have no top-level Go type guarantee under v1.31 codegen? They do — protoc-gen-go emits `pogo.Parent_Child` types for nested messages, so the existing emission pattern is correct as-is. Verify during generation; if any nested type fails to compile, switch the fd-var emission to resolve via the parent: `mustFD(mdOf("POGOProtos.Rpc.Parent.Child"), "field")` with a registry-based `mdOf` helper in the generated prologue.

- [ ] **Step 3: genshim script + generate**

```bash
cat > scripts/genshim.sh <<'EOF'
#!/bin/bash
# Regenerates pogoshim (typed accessors over protoreflect/hyperpb) from the
# committed golbat/pogo descriptors. Rerun after every vbase.pb.go update.
set -euo pipefail
cd "$(dirname "$0")/.."
go run ./cmd/pogoshimgen
gofmt -w pogoshim/pogoshim.gen.go
EOF
chmod +x scripts/genshim.sh
go get buf.build/go/hyperpb@$(git show perf/proto-decode-phase0:protobench/go.mod | grep hyperpb | awk '{print $2}')
./scripts/genshim.sh
go build ./pogoshim/
```

Expected: generator prints message/list counts (roughly 216+ messages — DiskEncounterOutProto adds a few beyond the GMO+Encounter closure); package compiles.

- [ ] **Step 4: Round-trip test (std-wrapped and hyperpb-wrapped through the same shims)**

```go
// pogoshim/roundtrip_test.go
package pogoshim_test

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

func TestShimOverStdAndHyperpb(t *testing.T) {
	enc := &pogo.EncounterOutProto{
		Pokemon: &pogo.WildPokemonProto{
			EncounterId:  7,
			SpawnPointId: "ABCD",
			Pokemon: &pogo.PokemonProto{
				Cp:             500,
				CpMultiplier:   0.79,
				PokemonDisplay: &pogo.PokemonDisplayProto{Shiny: true},
			},
		},
	}
	raw, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}

	check := func(name string, e pogoshim.EncounterOutProto) {
		w := e.GetPokemon()
		if w.IsZero() || w.GetEncounterId() != 7 || w.GetSpawnPointId() != "ABCD" {
			t.Fatalf("%s: wild mismatch", name)
		}
		p := w.GetPokemon()
		if p.GetCp() != 500 || p.GetCpMultiplier() != float32(0.79) {
			t.Fatalf("%s: pokemon mismatch", name)
		}
		if !p.GetPokemonDisplay().GetShiny() {
			t.Fatalf("%s: shiny lost", name)
		}
		if e.GetActiveItem() != pogo.Item_ITEM_UNKNOWN { // absent enum -> zero, typed as pogo enum
			t.Fatalf("%s: absent enum not zero", name)
		}
	}

	// std wrap
	var back pogo.EncounterOutProto
	if err := proto.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	check("std", pogoshim.AsEncounterOutProto(back.ProtoReflect()))

	// hyperpb wrap
	ty := hyperpb.CompileMessageDescriptor((*pogo.EncounterOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	defer shared.Free()
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	check("hyperpb", pogoshim.AsEncounterOutProto(msg.ProtoReflect()))
}
```

- [ ] **Step 5: Run tests** — `go test ./pogoshim/ -v` PASS; `go test ./...` green.

- [ ] **Step 6: Commit** — `git add cmd/pogoshimgen scripts/genshim.sh pogoshim go.mod go.sum && git commit -m "feat: pogoshim typed accessors over protoreflect/hyperpb"`

---

### Task 2: Engine runtime + config

**Files:**
- Create: `protoengine.go` (package main)
- Create: `protoengine_hyperpb.go` (`//go:build amd64 || arm64`)
- Create: `protoengine_stub.go` (`//go:build !amd64 && !arm64`)
- Modify: `config/config.go`, `config/reader.go`
- Test: `protoengine_test.go`

**Interfaces:**
- Consumes: `pogoshim` from Task 1.
- Produces (used by Tasks 4 & 7):
  - `config.Config.ProtoEngine.{Gmo, Encounter, DiskEncounter string; ShadowSampleRate float64; Pgo bool}` (koanf `proto_engine.{gmo,encounter,disk_encounter,shadow_sample_rate,pgo}`)
  - `func initProtoEngines()` — called from `main()` after config load
  - `func engineFor(method string) string` — resolved config ("std" when hyperpb unavailable on platform)
  - `func decodeWithArena[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error)` — hyperpb path: arena parse → wrap → process → free; std path: `proto.Unmarshal` into the right `pogo` struct → wrap `ProtoReflect()` → process. Returns unmarshal error without calling process.
  - Method keys: `"gmo"`, `"encounter"`, `"disk_encounter"`.

- [ ] **Step 1: Config plumbing**

`config/config.go` — add to `configDefinition` (after `Tuning`): `ProtoEngine protoEngine \`koanf:"proto_engine"\`` and:

```go
// protoEngine selects the client-proto decode engine per method and the
// shadow-verification sampling rate. "hyperpb" = arena decoding via
// buf.build/go/hyperpb behind pogoshim accessors; "std" = protobuf-go.
type protoEngine struct {
	Gmo              string  `koanf:"gmo"`
	Encounter        string  `koanf:"encounter"`
	DiskEncounter    string  `koanf:"disk_encounter"`
	ShadowSampleRate float64 `koanf:"shadow_sample_rate"`
	Pgo              bool    `koanf:"pgo"`
}
```

`config/reader.go` defaults block:

```go
		ProtoEngine: protoEngine{
			Gmo:              "hyperpb",
			Encounter:        "hyperpb",
			DiskEncounter:    "hyperpb",
			ShadowSampleRate: 0.01,
			Pgo:              true,
		},
```

- [ ] **Step 2: Engine core (`protoengine.go`)**

```go
package main

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/config"
	"golbat/pogo"
)

// Engine method keys.
const (
	engMethodGmo           = "gmo"
	engMethodEncounter     = "encounter"
	engMethodDiskEncounter = "disk_encounter"
)

// stdPrototype returns a fresh pogo struct for the std engine per method.
func stdPrototype(method string) proto.Message {
	switch method {
	case engMethodGmo:
		return &pogo.GetMapObjectsOutProto{}
	case engMethodEncounter:
		return &pogo.EncounterOutProto{}
	case engMethodDiskEncounter:
		return &pogo.DiskEncounterOutProto{}
	}
	panic("unknown proto engine method " + method)
}

func engineFor(method string) string {
	if !hyperpbSupported {
		return "std"
	}
	var v string
	switch method {
	case engMethodGmo:
		v = config.Config.ProtoEngine.Gmo
	case engMethodEncounter:
		v = config.Config.ProtoEngine.Encounter
	case engMethodDiskEncounter:
		v = config.Config.ProtoEngine.DiskEncounter
	}
	if v == "hyperpb" {
		return "hyperpb"
	}
	return "std"
}

// decodeStd is the fallback path: protobuf-go unmarshal, wrapped into the
// same pogoshim surface the hyperpb path uses.
func decodeStd[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	m := stdPrototype(method)
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, m); err != nil {
		return "", err
	}
	return process(wrap(m.ProtoReflect())), nil
}

func decodeWithArena[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	if engineFor(method) == "hyperpb" {
		return decodeHyperpb(method, payload, wrap, process)
	}
	return decodeStd(method, payload, wrap, process)
}
```

(`stdPrototype`'s panic uses string concatenation, so no `fmt` import is needed in this file — the compiler is the gate.)

- [ ] **Step 3: hyperpb glue (`protoengine_hyperpb.go`, build-tagged)**

```go
//go:build amd64 || arm64

package main

import (
	"sync"
	"sync/atomic"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/config"
	"golbat/pogo"
	log "github.com/sirupsen/logrus"
)

const hyperpbSupported = true

const pgoWarmupSamples = 256

type hyperEngine struct {
	ty      atomic.Pointer[hyperpb.MessageType]
	arenas  sync.Pool // *hyperpb.Shared
	profile struct {
		mu      sync.Mutex
		pending *hyperpb.Profile
		seen    int
		done    bool
	}
}

var hyperEngines = map[string]*hyperEngine{}

func initProtoEngines() {
	mds := map[string]protoreflect.MessageDescriptor{
		engMethodGmo:           (*pogo.GetMapObjectsOutProto)(nil).ProtoReflect().Descriptor(),
		engMethodEncounter:     (*pogo.EncounterOutProto)(nil).ProtoReflect().Descriptor(),
		engMethodDiskEncounter: (*pogo.DiskEncounterOutProto)(nil).ProtoReflect().Descriptor(),
	}
	for method, md := range mds {
		e := &hyperEngine{}
		e.ty.Store(hyperpb.CompileMessageDescriptor(md))
		if config.Config.ProtoEngine.Pgo {
			e.profile.pending = e.ty.Load().NewProfile()
		}
		e.arenas.New = func() any { return new(hyperpb.Shared) }
		hyperEngines[method] = e
		log.Infof("[PROTO_ENGINE] %s: hyperpb type compiled (engine=%s)", method, engineFor(method))
	}
}

// recordPGO feeds warmup packets into the profile; after pgoWarmupSamples it
// recompiles once and swaps the optimized type in.
func (e *hyperEngine) recordPGO(payload []byte) {
	e.profile.mu.Lock()
	defer e.profile.mu.Unlock()
	if e.profile.done || e.profile.pending == nil {
		return
	}
	ty := e.ty.Load()
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	_ = msg.Unmarshal(payload, hyperpb.WithRecordProfile(e.profile.pending, 1.0))
	shared.Free()
	e.profile.seen++
	if e.profile.seen >= pgoWarmupSamples {
		e.ty.Store(ty.Recompile(e.profile.pending))
		e.profile.pending = nil
		e.profile.done = true
		log.Infof("[PROTO_ENGINE] PGO recompile complete after %d samples", e.profile.seen)
	}
}

func decodeHyperpb[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	e := hyperEngines[method]
	if e == nil {
		return decodeStd(method, payload, wrap, process)
	}
	if !e.profile.done && config.Config.ProtoEngine.Pgo {
		e.recordPGO(payload)
	}
	shared := e.arenas.Get().(*hyperpb.Shared)
	defer func() {
		shared.Free()
		e.arenas.Put(shared)
	}()
	msg := shared.NewMessage(e.ty.Load())
	if err := msg.Unmarshal(payload); err != nil {
		return "", err
	}
	return process(wrap(msg.ProtoReflect())), nil
}
```

- [ ] **Step 4: Stub (`protoengine_stub.go`)**

```go
//go:build !amd64 && !arm64

package main

import "google.golang.org/protobuf/reflect/protoreflect"

const hyperpbSupported = false

func initProtoEngines() {}

func decodeHyperpb[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	return decodeStd(method, payload, wrap, process)
}
```

- [ ] **Step 5: Wire `initProtoEngines()` in `main()`** — immediately after config load / logger setup (same region where other post-config init happens; find the "Golbat starting" log line).

- [ ] **Step 6: Tests (`protoengine_test.go`)** — table test: for each method key and each engine value ("std", "hyperpb"), `decodeWithArena` over a marshaled synthetic proto (struct literals) must invoke `process` with a shim whose digest fields match the input; malformed payload returns error without calling process. Force engine via directly setting `config.Config.ProtoEngine` fields in the test (note: `initProtoEngines` must be called once in TestMain after setting config; follow the existing `init_test.go` pattern in the repo root if present, else create TestMain).

- [ ] **Step 7: Run** — `go build ./... && go test ./... ` green; `go vet ./...` clean.

- [ ] **Step 8: Commit** — `git commit -m "feat: per-method proto decode engine (hyperpb arenas + PGO, std fallback)"`

---

### Task 3: Shadow verification

**Files:**
- Create: `protoshadow.go` (package main)
- Modify: `stats_collector/stats_collector.go`, `stats_collector/prometheus.go`, `stats_collector/noop.go`
- Test: `protoshadow_test.go`

**Interfaces:**
- Consumes: `decodeStd`, shim types, `engineFor`.
- Produces: `func maybeShadow(method string, payload []byte)` — with probability `ShadowSampleRate` (and only when the live engine is hyperpb), decodes the payload with BOTH engines, digests each through the same shim digest walk, compares; on mismatch increments `statsCollector.IncProtoShadow(method, "mismatch")` and logs at Error with payload length + method; on match increments `(method, "match")`.
- Produces: `func digestGmo(g pogoshim.GetMapObjectsOutProto) uint64`, `digestEncounter(...)`, `digestDiskEncounter(...)` — FNV-1a folds over the exact field set the decoder reads (per the inventory: fort core fields + raid/gym display/incident displays, wild/nearby/map pokemon + display + IVs, weather condition + display levels + alerts, station core + battle details, cell ids/timestamps; encounter: wild chain + capture probabilities; disk encounter: pokemon chain + display).
- Stats: add `IncProtoShadow(method string, result string)` to the `StatsCollector` interface, the prometheus impl (`CounterVec` name `golbat_proto_shadow_total`, labels `method,result` — follow the exact pattern of `decodeMethods` at `stats_collector/prometheus.go:29`), and the noop impl.

Digest rules (make divergence detectable, not just crash-freedom): fold field NUMBER tags with values; fold float32 fields via `math.Float32bits(x.GetHeightM())` (bit-exact, no arithmetic drift); fold strings via their bytes; fold list lengths before elements. Both engines run the identical digest function over the shim surface — any decode divergence (missing field, wrong value, wrong list size) changes the fold.

- [ ] **Step 1: Failing test** — synthetic GMO/Encounter/DiskEncounter payloads (struct literals covering forts with raid+gym display+incidents, wilds with IVs+display, nearby, catchable, weather with alerts, stations with battle details): `digestX(std-wrapped)` == `digestX(hyperpb-wrapped)`; a corrupted payload (flip one varint field value, e.g. rebuild the proto with Cp+1) produces a DIFFERENT digest. `maybeShadow` with rate 1.0 forced: no mismatch counted on good payloads (assert via a test seam — export `shadowCompare(method, payload) bool` used by `maybeShadow`, test that directly).
- [ ] **Step 2: Run to verify failure** — `go test ./ -run TestShadow -v` fails compile (undefined symbols).
- [ ] **Step 3: Implement** per interfaces above. `maybeShadow` uses `rand.Float64() < config.Config.ProtoEngine.ShadowSampleRate`; runs INLINE on the decode goroutine (bounded by raw-processing concurrency; ~1% of packets decode twice — acceptable by design).
- [ ] **Step 4: Tests pass; full suite green; vet clean.**
- [ ] **Step 5: Commit** — `git commit -m "feat: sampled shadow verification of hyperpb vs std decode"`

---

### Task 4: Wave 1 — Encounter + DiskEncounter on the engine

**Files:**
- Modify: `decode.go:378-420` (decodeEncounter, decodeDiskEncounter)
- Modify: `decoder/pokemon_process.go` (both Update functions + new bytes-cache API)
- Modify: `decoder/pokemon_decode.go` (`updatePokemonFromEncounterProto`, `updatePokemonFromDiskEncounterProto`, `addWildPokemon`, `addEncounterPokemon`, `setPokemonDisplay`)
- Modify: `decoder/main.go:72` (diskEncounterCache type)
- Modify: `decoder/gmo_decode.go:189-193` (cache consumption)
- Test: `decoder/pokemon_encounter_shim_test.go`

**Interfaces:**
- Consumes: `decodeWithArena`, `maybeShadow`, `pogoshim`.
- Produces (Wave-2 tasks call these with std-wrapped shims until Task 7 flips GMO):
  - `UpdatePokemonRecordWithEncounterProto(ctx, db, encounter pogoshim.EncounterOutProto, username string, timestamp int64) string`
  - `UpdatePokemonRecordWithDiskEncounterProto(ctx, db, encounter pogoshim.DiskEncounterOutProto, username string) string` — on no-match now caches BYTES: new signature carries `payload []byte`; becomes `UpdatePokemonRecordWithDiskEncounterProto(ctx, db, encounter pogoshim.DiskEncounterOutProto, payload []byte, username string) string`
  - `diskEncounterCache *ttlcache.Cache[uint64, []byte]` — values are the raw DISK_ENCOUNTER payloads (fresh buffers owned by decode; safe to retain)
  - `(pokemon *Pokemon) addWildPokemon(ctx, db, wildPokemon pogoshim.WildPokemonProto, timestampMs int64, trustworthyTimestamp bool)`
  - `(pokemon *Pokemon) addEncounterPokemon(ctx, db, proto pogoshim.PokemonProto, username string)`
  - `setPokemonDisplay(pokemonId int16, display pogoshim.PokemonDisplayProto)`

Transformation rules (mechanical, apply to every listed function):
- Parameter `*pogo.X` → `pogoshim.X`; `nil` checks → `.IsZero()`; direct field reads → getters (`w.Pokemon` → `w.GetPokemon()`, already-getter calls unchanged); `for _, y := range x.Field` → `for y := range x.GetField().All()` (or indexed `Len/At`); repeated-scalar `len(x.Field)` → `x.GetField().Len()`; scalar `float32` semantics are preserved by the shim getters — do NOT widen to float64 anywhere.
- decode.go entry pattern (decodeEncounter shown; decodeDiskEncounter mirrors with its method key, stats calls, and payload pass-through):

```go
func decodeEncounter(ctx context.Context, sDec []byte, username string, timestampMs int64) string {
	maybeShadow(engMethodEncounter, sDec)
	res, err := decodeWithArena(engMethodEncounter, sDec,
		pogoshim.AsEncounterOutProto,
		func(enc pogoshim.EncounterOutProto) string {
			if enc.GetStatus() != pogo.EncounterOutProto_ENCOUNTER_SUCCESS {
				statsCollector.IncDecodeEncounter("error", "non_success")
				return fmt.Sprintf(...)  // keep existing message/format
			}
			return decoder.UpdatePokemonRecordWithEncounterProto(ctx, dbDetails, enc, username, timestampMs)
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeEncounter("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}
```

(Keep every existing statsCollector call and result string byte-identical — only the decode/access mechanics change.)
- Cache consumption (`gmo_decode.go:189-193`): `Get` returns `[]byte`; decode it via `decodeWithArena(engMethodDiskEncounter, payloadBytes, pogoshim.AsDiskEncounterOutProto, func(...)...)` and call `updatePokemonFromDiskEncounterProto` inside the process func — the arena must not outlive that call.

- [ ] **Step 1: Write the differential test first** (`decoder/pokemon_encounter_shim_test.go`): build a synthetic `*pogo.EncounterOutProto` (full: wild+pokemon+IVs+display+capture probability), wrap via `pogoshim.AsEncounterOutProto((&enc).ProtoReflect())`, run `updatePokemonFromEncounterProto` against a fresh Pokemon record (follow existing test setup patterns in decoder tests / init_test.go for cache init), assert the entity fields (Cp, AtkIv/DefIv/StaIv, Move1/2, Weight, Height, display fields) match the proto values. This test survives the migration as the Wave-1 behavior lock.
- [ ] **Step 2: Verify it fails to compile** (signatures still pogo) — then apply the transformation to the listed functions bottom-up (`setPokemonDisplay` → `addEncounterPokemon` → `addWildPokemon` → the two updatePokemonFrom* → pokemon_process pair → cache type + consumption → decode.go entries).
- [ ] **Step 3: Full suite green, vet clean, gofmt clean.**
- [ ] **Step 4: Commit** — `git commit -m "feat: Encounter/DiskEncounter on the proto engine (Wave 1)"`

---

### Task 5: Wave 2a — fort/station/incident decoder cascade to shims

**Files:**
- Modify: `decoder/gmo_decode.go` (`UpdateFortBatch`, `UpdateStationBatch` bodies; Raw struct field reads)
- Modify: `decoder/main.go:25-52` (`RawFortData.Data pogoshim.PokemonFortProto`, `RawStationData.Data pogoshim.StationProto`)
- Modify: `decoder/gym_decode.go` (`calculatePowerUpPoints`, `updateGymFromFort`)
- Modify: `decoder/pokestop_decode.go` (`updatePokestopFromFort`)
- Modify: `decoder/incident_decode.go:10` (`updateFromPokestopIncidentDisplay`)
- Modify: `decoder/station_decode.go` (`updateFromStationProto`), `decoder/station_battle.go` (`syncStationBattlesFromProto`, `stationBattleFromProto`)
- Modify: `decode.go` GMO fort/station collection loops — decodeGMO still std-decodes `*pogo.GetMapObjectsOutProto` in this task; it wraps each sub-proto when building Raw slices: `Data: pogoshim.AsPokemonFortProto(fort.ProtoReflect())`
- Test: `decoder/station_battle_test.go` (adapt constructors: wrap protos via `pogoshim.As...(x.ProtoReflect())`), plus new `decoder/fort_shim_test.go`

**Interfaces:**
- Produces: `updateGymFromFort(fortData pogoshim.PokemonFortProto, cellId uint64, timestampMs int64) *Gym`; `updatePokestopFromFort(fortData pogoshim.PokemonFortProto, cellId uint64, now int64) *Pokestop`; `calculatePowerUpPoints(fortData pogoshim.PokemonFortProto) (null.Int, null.Int)`; `updateFromPokestopIncidentDisplay(pokestopDisplay pogoshim.PokestopIncidentDisplayProto)`; `updateFromStationProto(stationProto pogoshim.StationProto, cellId uint64) *Station`; `syncStationBattlesFromProto(station *Station, battleDetail pogoshim.BreadBattleDetailProto)`; `stationBattleFromProto(stationId string, battleDetail pogoshim.BreadBattleDetailProto, updated int64) *StationBattleData`.
- Same transformation rules as Task 4. Oneof note: `PokestopIncidentDisplayProto.MapDisplay` — if `updateFromPokestopIncidentDisplay` reads oneof members (`GetCharacterDisplay()` etc.), the generator does not emit oneof accessors; add them to a hand-written `pogoshim/manual.go` (same pattern: cached fd + Get+IsValid), with a unit test in `pogoshim/manual_test.go` against a synthetic proto exercising the oneof case.

- [ ] **Step 1: New test first** (`decoder/fort_shim_test.go`): synthetic `*pogo.PokemonFortProto` (gym variant with raid info + gym display, and pokestop variant with incident displays + lure), wrapped to shims, through `updateGymFromFort` / `updatePokestopFromFort`; assert entity fields (team, raid timestamps, lure id, incident ids). Fails to compile initially.
- [ ] **Step 2: Apply transformation bottom-up** (incident display → gym/pokestop/station leaf functions → batch bodies → Raw struct types → decode.go wrap sites). Adapt `station_battle_test.go` constructors.
- [ ] **Step 3: Full suite green, vet clean.** Escape audit: `grep -n "pogoshim\." decoder/*.go | grep -E "chan |go func|\.Set\(" ` — confirm no shim flows into a channel, goroutine, or cache set (the batch paths are synchronous; `FortTrackerGMOContents` remains strings/ints).
- [ ] **Step 4: Commit** — `git commit -m "feat: fort/station/incident decode on pogoshim (Wave 2a)"`

---

### Task 6: Wave 2b — pokemon/weather/spawnpoint cascade + weather-consensus boundary fix

**Files:**
- Modify: `decoder/gmo_decode.go` (`UpdatePokemonBatch`, `UpdateClientWeatherBatch` bodies; weather enum-map build)
- Modify: `decoder/main.go` (`RawWildPokemonData/RawNearbyPokemonData/RawMapPokemonData.Data` → shim types)
- Modify: `decoder/pokemon_decode.go` (`wildSignificantUpdate`, `nearbySignificantUpdate`, `updateFromWild`, `updateFromMap`, `updateFromNearby`)
- Modify: `decoder/spawnpoint.go:252` (`spawnpointUpdateFromWild`)
- Modify: `decoder/weather.go:304` (`updateWeatherFromClientWeatherProto` → takes the new value struct)
- Modify: `decoder/weather_consensus.go` (value-struct retention)
- Modify: `decode.go` GMO pokemon/weather collection loops (std-decode + wrap, as in Task 5)
- Test: `decoder/weather_consensus_test.go` (new or adapted), `decoder/pokemon_wild_shim_test.go`

**Interfaces:**
- Produces: `updateFromWild(ctx, db, wildPokemon pogoshim.WildPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string)` (weather enum map type unchanged); `updateFromNearby(... pogoshim.NearbyPokemonProto ...)`; `updateFromMap(... pogoshim.MapPokemonProto ...)`; `wildSignificantUpdate(wildPokemon pogoshim.WildPokemonProto, time int64) bool`; `nearbySignificantUpdate(pogoshim.NearbyPokemonProto, int64) bool`; `spawnpointUpdateFromWild(ctx, db, wildPokemon pogoshim.WildPokemonProto, timestampMs int64)`.
- **Weather consensus boundary fix** — replace retained protos with a value struct:

```go
// weatherObservation is the Golbat-owned copy of the ClientWeatherProto
// fields consensus and weather updates read. Retaining decoded protos is
// forbidden: hyperpb messages must not outlive their arena.
type weatherObservation struct {
	S2CellId           int64
	GameplayCondition  int32
	WindDirection      int32
	CloudLevel         int32
	RainLevel          int32
	WindLevel          int32
	SnowLevel          int32
	FogLevel           int32
	SpecialEffectLevel int32
	Alerts             []weatherAlert // {Severity int32; WarnWeather bool}
}

func weatherObservationFromShim(w pogoshim.ClientWeatherProto) weatherObservation
```

  `WeatherConsensusState.LastObsByCondition` becomes `map[int32]weatherObservation`; `applyObservation(hourKey int64, account string, obs weatherObservation) (bool, weatherObservation, bool /*havePublish*/)` (adjust the publish-signaling to value semantics — the previous nil-pointer signal becomes the third return); `updateWeatherFromClientWeatherProto` → `updateWeatherFromObservation(obs weatherObservation)` reading the same fields; `UpdateClientWeatherBatch` takes `[]pogoshim.ClientWeatherProto` (or extracts observations up front and operates on those — prefer extracting once: `obs := weatherObservationFromShim(w)` immediately, everything downstream value-typed).

- [ ] **Step 1: Tests first** — `weather_consensus_test.go`: three accounts vote across two conditions; assert publish decision and that the published observation carries the display levels of the winning condition's last observation (locks the retention semantics through the refactor). `pokemon_wild_shim_test.go`: synthetic wild through `updateFromWild` (weather-boost path included) asserting entity fields.
- [ ] **Step 2: Apply transformation bottom-up** (weatherObservation + consensus first — it compiles independently — then weather batch, then pokemon chain, then Raw structs + decode.go wrap sites).
- [ ] **Step 3: Full suite green, vet clean. Escape audit repeated** (Task 5 Step 3 grep) — expected stores now: only `weatherObservation` values (allowed: no proto references) and `[]byte` in diskEncounterCache.
- [ ] **Step 4: Commit** — `git commit -m "feat: pokemon/weather decode on pogoshim, weather consensus retains values (Wave 2b)"`

---

### Task 7: GMO engine flip

**Files:**
- Modify: `decode.go` `decodeGMO` (`:465`) + `isCellNotEmpty` (`:584`)
- Test: extend `protoshadow_test.go` with a full-GMO differential case

**Interfaces:**
- Consumes: everything above. After this task no std-struct field access remains on the GMO path.

- [ ] **Step 1: Flip decodeGMO** to the engine pattern:

```go
func decodeGMO(ctx context.Context, protoData *ProtoData, scanParameters decoder.ScanParameters) string {
	maybeShadow(engMethodGmo, protoData.Data)
	res, err := decodeWithArena(engMethodGmo, protoData.Data,
		pogoshim.AsGetMapObjectsOutProto,
		func(gmo pogoshim.GetMapObjectsOutProto) string {
			// existing body, reading through shims:
			//  - status check via gmo.GetStatus()
			//  - for cell := range gmo.GetMapCell().All() { ... collect Raw slices,
			//    fort tracker contents, cell ids ... }
			//  - weather := gmo.GetClientWeather() -> []pogoshim.ClientWeatherProto via .All()
			//  - the batch calls, gated by scanParameters exactly as today
		})
	if err != nil {
		statsCollector.IncDecodeGMO("error", "parse")
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}
```

  `isCellNotEmpty(cell pogoshim.ClientMapCellProto) bool` via `.GetFort().Len() > 0 || ...` matching its current field checks. ALL batch processing (including `CheckRemovedForts` and the `ProactiveIVSwitch` dispatch) must remain inside the `process` closure — the ProactiveIVSwitch goroutines receive only `WeatherUpdate` values (no shims), verify that holds.
- [ ] **Step 2: Full-GMO differential test** — synthetic GMO with every entity type; run `shadowCompare(engMethodGmo, payload)` (both engines, digest equality) and assert true; run decodeGMO end-to-end under each engine (config override) against fresh caches asserting identical entity outcomes for a couple of representative fields per entity type.
- [ ] **Step 3: Full suite green, vet clean. Final escape audit** across the whole repo: `grep -rn "pogoshim\." --include="*.go" . | grep -vE "_test.go|pogoshim/|cmd/pogoshimgen"` — review every hit against the no-retention rule; document the audit result in the commit message.
- [ ] **Step 4: Commit** — `git commit -m "feat: GMO decode on the hyperpb engine (Wave 2)"`

---

### Task 8: Ops, docs, and handoff sweep

**Files:**
- Modify: `Makefile` (PGO capture targets), `config.toml.example` (`[proto_engine]` section), `CLAUDE.md` (engine architecture + invariants)
- Create: none

- [ ] **Step 1: Makefile PGO targets** — copy the `pgo-capture`/`pgo-status` block verbatim from `git show perf/proto-decode-phase0:Makefile` (Go auto-applies a committed `default.pgo`; the heavy user can capture from their own instance).
- [ ] **Step 2: config.toml.example** — commented `[proto_engine]` section documenting `gmo`/`encounter`/`disk_encounter` (`"hyperpb"` default, `"std"` fallback/rollback), `shadow_sample_rate` (0.01 default; 0 disables; mismatches appear as `golbat_proto_shadow_total{result="mismatch"}`), `pgo`.
- [ ] **Step 3: CLAUDE.md** — a "Proto decode engines" subsection under Raw Message Processing: the shim surface, the arena/buffer invariants (no shim retention; payload owned by decode until arena free), the regen step (`scripts/genshim.sh` after vbase updates), rollback = config flip.
- [ ] **Step 4: Full verification sweep** — `go build ./... && go test ./... && go vet ./...` plus `gofmt -l` empty repo-wide on changed files; run the repo's existing test suite one final time and paste summary into the report.
- [ ] **Step 5: Commit** — `git commit -m "docs: proto engine config, PGO targets, and engine architecture notes"`

---

## Execution Notes

- Tasks are strictly sequential (each cascade builds on the previous signatures).
- Task 1 needs network for `go get buf.build/go/hyperpb`.
- The `pogoshim` generated file will be several hundred KB — it is committed; reviewers should treat it as generated (spot-check, don't line-review).
- Behavioral guardrail for every task: existing result strings, statsCollector calls, and webhook/db behavior stay byte-identical; only proto access mechanics change. Reviewers should specifically hunt for accidental semantic drift (`!= nil` vs `IsZero` on messages that can legitimately be present-but-empty, float widening, `len()` vs `.Len()` on nil).
- Rollback story for the heavy user: `[proto_engine] gmo = "std"` etc. in config — no rebuild.
