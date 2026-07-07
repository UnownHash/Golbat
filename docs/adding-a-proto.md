# Adding a new proto to the decode pipeline

How to take a Pokemon GO method Golbat doesn't process yet (or a new proto on
an existing method) from raw bytes to decoded, engine-accelerated, shadow-
verified processing. Follow the steps in order — each layer feeds the next.

## The pipeline at a glance

```
raw payload ──▶ decode.go dispatch ──▶ decodeWithArena(method, engine, payload, wrap, process)
                                          │  engine per config: hyperpb (arena) or std (protobuf-go)
                                          ▼
                                pogoshim.As<Root>(msg.ProtoReflect())
                                          │  typed accessors, engine-agnostic
                                          ▼
                            decoder.UpdateXxx(ctx, db, shim, ...)   ← your processing
                                          │
                              entity Set* calls (values copied out)
```

Shadow verification (`maybeShadow`) samples packets, decodes them with BOTH
engines, and compares digests — your safety net while a new proto soaks.

## Step 1: Generate the typed accessors (pogoshim)

1. Add the top-level proto name(s) to the `-roots` default in
   `cmd/pogoshimgen/main.go` — the **Data** proto and, if you read the
   request too, the **Request** proto (e.g. `GetFooOutProto,GetFooProto`).
   Names are bare (no `POGOProtos.Rpc.` prefix). The generator pulls in the
   full transitive closure automatically.
2. Regenerate: `./scripts/genshim.sh` — rewrites `pogoshim/pogoshim.gen.go`.
   Commit the regenerated file together with your change.
3. What you get per message: `pogoshim.As<Msg>(protoreflect.Message)`,
   protoc-style getters (`GetFortId() string`, enums typed as `pogo.<Enum>`,
   float32 stays float32), `Has<Field>()` for submessages, list wrappers with
   `Len()/At(i)/All()`. Strings/bytes are **cloned out of the arena** — safe
   to retain. Oneof members get ordinary `Get`/`Has` accessors.
   **Not generated: map-field accessors** — if your proto has map fields,
   read them through raw protoreflect (`shim` exposes nothing; wrap the
   parent message yourself) or extend the generator first.

## Step 2: Register an engine handle

In `protoengine.go`, add one entry to the `engineSpecs` table per root proto:

```go
{method: "foo", md: (*pogo.GetFooOutProto)(nil).ProtoReflect().Descriptor(),
 newStd: func() proto.Message { return &pogo.GetFooOutProto{} }, target: &fooEngine},
```

plus the `var fooEngine *protoEngineHandle` package var. Request+Data pairs
get two handles sharing one `method` key (see `openInvasionReqEngine` /
`openInvasionEngine`). The method key is also the config name — pick
something an operator will recognize.

`initProtoEngines()` compiles the hyperpb type at startup; PGO warmup
(first 256 packets or 10 minutes, whichever first) then recompiles with a
traffic-shaped profile automatically.

## Step 3: Wire the decode entry

In `decode.go` (dispatch switch + a `decodeFoo` function), follow the
established pattern:

```go
func decodeFoo(ctx context.Context, sDec []byte) string {
    maybeShadow("foo", sDec)
    res, err := decodeWithArena("foo", fooEngine, sDec,
        pogoshim.AsGetFooOutProto,
        func(out pogoshim.GetFooOutProto) string {
            if out.GetResult() != pogo.GetFooOutProto_SUCCESS {
                statsCollector.IncDecodeFoo("error", "non_success")
                return "unsuccessful GetFooOutProto"
            }
            return decoder.UpdateFooRecords(ctx, dbDetails, out)
        })
    if err != nil {
        statsCollector.IncDecodeFoo("error", "parse")
        return fmt.Sprintf("Failed to parse %s", err)
    }
    return res
}
```

Request+Data methods nest: decode the Request in an outer `decodeWithArena`,
decode Data inside its process closure (both shims usable together; both
arenas freed after processing). See `decodeOpenInvasion` once Wave 3 lands.

## Step 4: Write the decoder processing

`decoder.UpdateFooRecords(ctx, db, out pogoshim.GetFooOutProto)` — read via
getters, write into entities via their `Set*` methods. **The iron rules:**

- **Shims must not outlive the `process` closure.** Never store a shim (or
  anything holding one) in a struct field, package var, cache, channel, or a
  goroutine that can outlive the decode call. The arena behind it is freed
  and pooled the moment `decodeWithArena` returns. If you need data later,
  copy it into a plain Golbat-owned struct at the boundary (see
  `weatherObservation` and `mapFortSummary` for the pattern) — or cache the
  raw `[]byte` payload and re-decode on consumption (see
  `diskEncounterCache`).
- Strings from getters are already safe to retain (cloned) — it's *shims and
  messages* that must not escape.
- Don't wrap possibly-nil `*pogo.X` pointers via `.ProtoReflect()` yourself;
  `As<Msg>` normalizes invalid messages to zero shims, but prefer reaching
  submessages through parent-shim getters.
- Never mutate a decoded proto (hyperpb panics on mutation; we are read-only
  by design).

## Step 5: Shadow coverage

Add your method to `genericShadowEngine` in `protoshadow.go` (map the method
key to its handle). The generic digest (`digestMessageGeneric`) walks every
field automatically — no per-method digest code needed. Multi-proto methods
need a small composite entry; see the comments at `protoshadow.go`'s
`genericShadowEngine`.

## Step 6: Config + docs

- The method works immediately with `proto_engine.default` (= `"hyperpb"`).
  An operator can pin it via `[proto_engine.overrides] foo = "std"` —
  document the key in `config.toml.example`.
- Resolution order: legacy explicit key (gmo/encounter/disk_encounter only)
  > `overrides[method]` > `default`. Unknown values warn at startup and run
  std.

## Step 7: Tests (the minimum bar)

1. **Cross-engine differential**: build the proto with std struct literals,
   marshal, run your decoder function under BOTH engines (wrap via
   `ProtoReflect()` for std; `decodeWithArena` for hyperpb) and assert the
   same entity outcomes. See `decoder/fort_shim_test.go` for the shape.
2. **Shadow digest agreement**: a synthetic payload through
   `shadowCompare("foo", payload)` must return true.
3. If you added a retention boundary (Step 4 copy-out), a test that locks
   the copied fields.

## When vbase.proto updates (game version bump)

`pogo/vbase.pb.go` is regenerated by the existing proto pipeline; then run
`./scripts/genshim.sh` and commit. If a field your code reads was removed,
the build fails at the getter call — that's the compile-time safety working.

## Rollout

Deploy; watch `golbat_proto_shadow_total{method="foo"}` — `match` should
grow, `mismatch` must stay zero (mismatches also log `[PROTO_SHADOW]` at
ERROR). Rollback is `[proto_engine.overrides] foo = "std"` + restart.
