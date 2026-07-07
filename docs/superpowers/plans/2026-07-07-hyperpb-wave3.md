# Hyperpb Wave 3: Remaining Method Conversions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every remaining client-proto decode path (fort details, gym info, quests, map forts, routes, incidents/invasions, nebula battle state, tappables, stations, contests, RSVPs, social/player) runs through the proto engine with pogoshim accessors, the `getMapFortsCache` proto-retention hazard is eliminated, and shadow verification covers all migrated methods via a generic digest.

**Architecture:** Same one-surface/two-engines pattern as Waves 1–2 (see `docs/superpowers/plans/2026-07-06-hyperpb-migration.md` — its Global Constraints, transformation rules, and typed-nil/arena-lifetime invariants ALL apply here verbatim). New in this wave: engine config scales via `proto_engine.default` + per-method `[proto_engine.overrides]`; shadow uses a generic descriptor-ordered digest (no per-method walks); per-root engine handles replace the method-keyed map.

**Tech Stack:** unchanged (hyperpb pinned, pogoshim generator, existing stats_collector pattern).

## Global Constraints (in addition to the Wave 1–2 plan's, which remain binding)

- Work ONLY in `/Users/james/GolandProjects/golbat-wt/hyperpb-decode` (branch `perf/hyperpb-decode`, PR #381).
- Deployed-config compatibility: existing keys `proto_engine.gmo/encounter/disk_encounter` keep working. New resolution order: explicit legacy key (non-empty) > `overrides[method]` > `default`. `default` defaults to `"hyperpb"`; legacy keys' defaults become `""` (= inherit), preserving today's effective behavior.
- Shadow: all migrated methods get shadow coverage via the generic digest at the existing `shadow_sample_rate`.
- PGO: warmup unchanged (256 samples/method) but recording also stops 10 minutes after process start (`pgoWarmupDeadline`) so rare methods don't pay the double-parse forever; a method that never completes warmup simply keeps its baseline compiled type.
- Push-gateway path stays std (only scalars are read, nothing crosses the decoder boundary; document why in code).
- String/bytes cloning and As<M> IsValid normalization are generator-level and apply automatically to regenerated shims.
- Every task: gofmt/vet/build/full-test green; behavioral guardrail (result strings, statsCollector calls byte-identical).

## Migration surface (authoritative inventory — from branch recon 2026-07-07)

**Dispatch cases and their protos** (decode.go line refs current at plan time):
| method key (new config name) | decode fn | Data proto | Request proto | decoder entry |
|---|---|---|---|---|
| `fort_details` | decodeFortDetails L265 | FortDetailsOutProto | — | UpdatePokestopRecordWithFortDetailsOutProto / UpdateGymRecordWithFortDetailsOutProto |
| `gym_info` | decodeGetGymInfo L360 | GymGetInfoOutProto | — | UpdateGymRecordWithGymInfoProto |
| `quest` | decodeQuest L140 | FortSearchOutProto | — | UpdatePokestopWithQuest |
| `get_map_forts` | decodeGetMapForts L286 | GetMapFortsOutProto | — | UpdateFortRecordWithGetMapFortsOutProto |
| `routes` | decodeGetRoutes L319 | GetRoutesOutProto | — | UpdateRouteRecordWithSharedRouteProto |
| `start_incident` | decodeStartIncident L425 | StartIncidentOutProto | — | ConfirmIncident |
| `open_invasion` | decodeOpenInvasion L444 | OpenInvasionCombatSessionOutProto | OpenInvasionCombatSessionProto | UpdateIncidentLineup |
| `nebula_battle_state` | decodeNebulaInvasionState (decode_nebula.go:37) | BattleStateOutProto | — | UpdateIncidentLineupFromBattleState |
| `contest_data` | decodeGetContestData L621 | GetContestDataOutProto | GetContestDataProto | UpdatePokestopWithContestData |
| `size_contest_entry` | decodeGetPokemonSizeContestEntry L638 | GetPokemonSizeLeaderboardEntryOutProto | GetPokemonSizeLeaderboardEntryProto | UpdatePokestopWithPokemonSizeContestEntry |
| `station_details` | decodeGetStationDetails L660 | GetStationedPokemonDetailsOutProto | GetStationedPokemonDetailsProto | ResetStationedPokemonWithStationDetailsNotFound / UpdateStationWithStationDetails |
| `tappable` | decodeTappable L685 | ProcessTappableOutProto | ProcessTappableProto | UpdatePokemonRecordWithTappableEncounter / UpdateTappable |
| `event_rsvps` | decodeGetEventRsvp L710 | GetEventRsvpsOutProto | GetEventRsvpsProto | UpdateGymRecordWithRsvpProto |
| `event_rsvp_count` | decodeGetEventRsvpCount L739 | GetEventRsvpCountOutProto | — | ClearGymRsvp (string only) |
| `social` | decodeSocialActionWithRequest L165 | ProxyResponseProto (+ nested InternalGetFriendDetailsOutProto / InternalSearchPlayerOutProto / InternalSearchPlayerProto) | ProxyRequestProto | UpdatePlayerRecordWithPlayerSummary |

**Transitive decoder functions** (all become shim-typed): gym_process.go:13/28/59/78, pokestop_process.go:16/31/102/147, gym_decode.go updateGymFromFortProto/updateGymFromGymInfoOutProto/updateGymFromRsvpProto/updateGymFromGetMapFortsOutProto, pokestop_decode.go updatePokestopFromFortDetailsProto/updatePokestopFromQuestProto (the ~280-line quest oneof/enum switch — biggest single function in this wave)/updatePokestopFromGetContestDataOutProto/updatePokestopFromGetPokemonSizeContestEntryOutProto/updatePokestopFromGetMapFortsOutProto, routes_process.go:10, incident_process.go:13/29/41, incident_decode.go updateFromOpenInvasionCombatSessionOut/updateFromBattleState, tappable_process.go:13, pokemon_process.go:77 + pokemon_decode.go updatePokemonFromTappableEncounterProto, station_process.go:13/33, fort.go:190, player.go:1304.

**Retention (must fix in this wave):** `decoder/main.go:91 getMapFortsCache ttlcache.Cache[string, *pogo.GetMapFortsOutProto_FortProto]`; set at fort.go:198; consumed at gym_state.go:487-495 and pokestop_state.go:484-492; fields read from retained value: Id, Latitude, Longitude, Image[0].Url (len-guarded), Name.

**New generator roots** (append to existing three): FortDetailsOutProto, GymGetInfoOutProto, FortSearchOutProto, GetMapFortsOutProto, GetRoutesOutProto, StartIncidentOutProto, OpenInvasionCombatSessionProto, OpenInvasionCombatSessionOutProto, BattleStateOutProto, GetContestDataProto, GetContestDataOutProto, GetPokemonSizeLeaderboardEntryProto, GetPokemonSizeLeaderboardEntryOutProto, GetStationedPokemonDetailsProto, GetStationedPokemonDetailsOutProto, ProcessTappableProto, ProcessTappableOutProto, GetEventRsvpsProto, GetEventRsvpsOutProto, GetEventRsvpCountOutProto, ProxyRequestProto, ProxyResponseProto, InternalGetFriendDetailsOutProto, InternalSearchPlayerOutProto, InternalSearchPlayerProto.

**Mutation:** none on any path (verified). **proto.Marshal of client protos:** none.

---

### Task 1: Foundation — roots, engine registry, config scaling, generic digest

**Files:**
- Modify: `cmd/pogoshimgen/main.go` (default roots list), regenerate `pogoshim/pogoshim.gen.go`
- Modify: `protoengine.go`, `protoengine_hyperpb.go`, `protoengine_stub.go`
- Modify: `config/config.go`, `config/reader.go`
- Create: `protodigest.go` (package main — generic digest)
- Test: `protoengine_test.go` (extend), `protodigest_test.go`

**Interfaces (later tasks rely on these exactly):**
- `engineFor(method string) string` — resolution: legacy explicit key ("gmo"/"encounter"/"disk_encounter", non-empty) > `config.Config.ProtoEngine.Overrides[method]` > `config.Config.ProtoEngine.Default` ("hyperpb"). Unknown values warn once at init and resolve std.
- Config: `ProtoEngine.Default string` (default "hyperpb"), `ProtoEngine.Overrides map[string]string` (koanf `proto_engine.overrides`), legacy fields' defaults become "".
- Per-root engine handles: `type protoEngineHandle struct{...}` created by `newProtoEngine(md protoreflect.MessageDescriptor)`; package vars in protoengine.go for every root (e.g. `var fortDetailsEngine, gymInfoEngine, questEngine, mapFortsEngine, routesEngine, startIncidentEngine, openInvasionReqEngine, openInvasionEngine, battleStateEngine, contestDataReqEngine, contestDataEngine, sizeEntryReqEngine, sizeEntryEngine, stationDetailsReqEngine, stationDetailsEngine, tappableReqEngine, tappableEngine, rsvpReqEngine, rsvpEngine, rsvpCountEngine, proxyReqEngine, proxyRespEngine, friendDetailsEngine, searchPlayerOutEngine, searchPlayerReqEngine` + the existing three refactored to handles).
- `decodeWithArena[T](method string, eng *protoEngineHandle, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error)` — signature gains the handle; existing three call sites updated; std path derives the prototype from the handle's descriptor via `dynamicpb`? NO — std path must stay fast generated-struct unmarshal: the handle also carries `newStd func() proto.Message` (e.g. `func() proto.Message { return &pogo.FortDetailsOutProto{} }`) supplied at handle construction.
- PGO deadline: `pgoWarmupDeadline = 10 * time.Minute` from `initProtoEngines()`; `recordPGO` no-ops past it (atomic time check alongside the existing done flag).
- Generic digest: `func digestMessageGeneric(h hash, m protoreflect.Message)` — walks the DESCRIPTOR's fields in field-number order (never `Range`, whose order is unspecified); folds Has-bit + value per field; recurses messages; lists fold length then elements in order; map fields fold sorted-key entries; enums/scalars via the existing fold helpers (float32 via Float32bits). `shadowCompare` uses it for every method that lacks a hand-written digest (registry: hand walks stay for gmo/encounter/disk_encounter).

- [ ] **Step 1: Tests first.** `protodigest_test.go`: generic digest cross-engine equality (std wrap vs hyperpb wrap) for synthetic FortDetailsOutProto, FortSearchOutProto (with quest rewards), GetMapFortsOutProto payloads; corruption sensitivity (flip a field → digest differs); map-field determinism if any root proto has a map field (check; if none, note it). `protoengine_test.go`: engineFor resolution matrix (legacy set/unset × override × default), one new-root decode via both engines.
- [ ] **Step 2: Verify tests fail to compile.**
- [ ] **Step 3: Implement**: roots in generator + `./scripts/genshim.sh` (commit regenerated file in the same commit); handle refactor; config plumbing (koanf map field — verify koanf decodes TOML tables into `map[string]string`; follow existing koanf usage); PGO deadline; generic digest.
- [ ] **Step 4: Full suite + race tests green.**
- [ ] **Step 5: Commit** — `feat: engine foundation for Wave 3 (roots, handles, config default/overrides, generic digest)`

---

### Task 2: High-volume trio — fort_details, gym_info, quest + getMapFortsCache value fix

**Files:**
- Modify: `decode.go` (decodeFortDetails, decodeGetGymInfo, decodeQuest), `decoder/gym_process.go`, `decoder/pokestop_process.go`, `decoder/gym_decode.go`, `decoder/pokestop_decode.go` (incl. the ~280-line quest switch — mechanical but large; transformation rules from the Wave 1–2 plan apply), `decoder/gym_state.go`, `decoder/pokestop_state.go`, `decoder/fort.go`, `decoder/main.go` (cache type)
- Test: `decoder/quest_shim_test.go` (new), existing fort tests extended

**Interfaces:**
- `getMapFortsCache` becomes `*ttlcache.Cache[string, mapFortSummary]` with `type mapFortSummary struct { Id string; Latitude, Longitude float64; ImageUrl string; Name string }` extracted at Set time (`mapFortSummaryFromShim(f pogoshim.GetMapFortsOutProto_FortProto)` — Image[0].Url with Len guard). Consumers `updateGymFromGetMapFortsOutProto`/`updatePokestopFromGetMapFortsOutProto` take `mapFortSummary` (rename to `...FromMapFortSummary`). This REMOVES the std-engine constraint on GET_MAP_FORTS (Task 3 flips it safely).
- `UpdatePokestopRecordWithFortDetailsOutProto(ctx, db, fort pogoshim.FortDetailsOutProto) string`, `UpdateGymRecordWithFortDetailsOutProto(...)`, `UpdateGymRecordWithGymInfoProto(ctx, db, gymInfo pogoshim.GymGetInfoOutProto) string`, `UpdatePokestopWithQuest(ctx, db, quest pogoshim.FortSearchOutProto, haveAr bool) string` + their transitive updateFrom* functions.
- decode.go entries follow the Wave-1 pattern: `maybeShadow(method, sDec)` + `decodeWithArena(method, <engine handle>, sDec, pogoshim.As<Root>, process)` with byte-identical result strings/stats.
- Quest oneof note: reward/condition oneofs — generator emits member accessors (proven in Wave 2a); the switch on `quest.QuestType`/reward `.Type` becomes getter-driven; where the old code switches on oneof wrapper types (`.(*pogo.QuestConditionProto_WithPokemonType_)` style), use `Has<Member>()` accessors instead — flag any construct that doesn't map cleanly as NEEDS_CONTEXT rather than guessing.

- [ ] **Step 1: Tests first** (quest: synthetic FortSearchOutProto with AR + non-AR rewards of several types through UpdatePokestopWithQuest asserting reward columns; mapFortSummary round-trip).
- [ ] **Step 2: Transform bottom-up; adapt existing tests.**
- [ ] **Step 3: Full suite green; escape audit grep (per Wave 1–2 plan Step 3 pattern).**
- [ ] **Step 4: Commit** — `feat: fort details, gym info, quests on the proto engine (Wave 3a)`

---

### Task 3: get_map_forts, routes, incidents/invasions, nebula battle state

**Files:**
- Modify: `decode.go` (decodeGetMapForts, decodeGetRoutes, decodeStartIncident, decodeOpenInvasion), `decode_nebula.go`, `decoder/fort.go`, `decoder/routes_process.go`, `decoder/incident_process.go`, `decoder/incident_decode.go`
- Test: `decoder/incident_battlestate_test.go` (adapt constructors), new incident lineup shim test

**Interfaces:**
- `UpdateFortRecordWithGetMapFortsOutProto(ctx, db, mapFort pogoshim.GetMapFortsOutProto_FortProto) (bool, string)` — signature per current fort.go:190 shape; caches `mapFortSummary` (from Task 2) instead of the proto: GET_MAP_FORTS is now arena-safe; remove the std-only warning comments at decoder/main.go and CLAUDE.md (Task 5 doc pass confirms).
- `UpdateRouteRecordWithSharedRouteProto(ctx, db, route pogoshim.SharedRouteProto) error`; routes retention check: Route entity copies values (verify — if any proto retained, value-copy at boundary).
- `UpdateIncidentLineup(ctx, db, req pogoshim.OpenInvasionCombatSessionProto, resp pogoshim.OpenInvasionCombatSessionOutProto) string`; `ConfirmIncident(ctx, db, resp pogoshim.StartIncidentOutProto) string`; `UpdateIncidentLineupFromBattleState(ctx, db, fortId, incidentId string, battleState pogoshim.BattleStateOutProto) string`; nebula entry uses `decodeWithArena("nebula_battle_state", battleStateEngine, ...)` — note decodeNebula is called from BOTH grpc_server_raw.go and routes.go; only decode_nebula.go's unmarshal site changes.
- Two-proto methods (open_invasion): decode Request and Data each via their own `decodeWithArena` handle — NEST the Data decode inside the Request decode's process closure (both shims alive together, both arenas freed after processing; document the nesting pattern — it becomes the template for Task 4's request+data methods).

- [ ] **Step 1: Tests first** (battle-state lineup via shims both engines; map-fort cache flow end-to-end: set via GET_MAP_FORTS path, consume via fort-details path).
- [ ] **Step 2: Transform; adapt tests; escape audit.**
- [ ] **Step 3: Full suite green. Commit** — `feat: map forts, routes, incidents on the proto engine (Wave 3b)`

---

### Task 4: tappables, stations, contests, RSVPs, social/player

**Files:**
- Modify: `decode.go` (decodeTappable, decodeGetStationDetails, decodeGetContestData, decodeGetPokemonSizeContestEntry, decodeGetEventRsvp, decodeGetEventRsvpCount, decodeSocialActionWithRequest, decodeGetFriendDetails, decodeSearchPlayer), `decoder/tappable_process.go`, `decoder/pokemon_process.go` (+ pokemon_decode.go tappable chain), `decoder/station_process.go`, `decoder/pokestop_process.go` (contest pair), `decoder/gym_process.go` (rsvp pair), `decoder/player.go`
- Test: adapt `decode_push_gateway_test.go` if signatures leak (they shouldn't), new tappable + player shim tests

**Interfaces:**
- Same patterns as Tasks 2–3 (request+data nesting from Task 3 for tappable/station_details/contest_data/size_contest_entry/event_rsvps).
- Social: `decodeSocialActionWithRequest` decodes ProxyRequestProto (Request) and ProxyResponseProto (Data), then the response `.Payload` bytes decode as one of the Internal* protos — three nested decodeWithArena levels max; the inner payload decodes use their own handles under the SAME config method key `"social"`. `UpdatePlayerRecordWithPlayerSummary(db, summary pogoshim.InternalPlayerSummaryProto, profile pogoshim.PlayerPublicProfileProto, friendCode string, friendshipId string) error` — verify the Player entity copies all values out (strings are cloned by the shims; confirm no shim retained in playerCache).
- `event_rsvp_count` reads only `LocationId` — still migrate for uniformity (trivial).
- RSVP oneof: `rsvpRequest.EventDetails.(*pogo.GetEventRsvpsProto_Raid)` type-switch → `HasRaid()`/`GetRaid()` accessors.
- Push-gateway: NOT migrated; add the one-line comment in decode_push_gateway.go explaining why (scalar-only extraction, std is fine).

- [ ] **Step 1: Tests first (tappable encounter chain both engines; player summary field copy).**
- [ ] **Step 2: Transform; escape audit (special attention: playerCache, tappable caches).**
- [ ] **Step 3: Full suite green. Commit** — `feat: tappables, stations, contests, rsvps, social on the proto engine (Wave 3c)`

---

### Task 5: Docs, config example, sweep

**Files:**
- Modify: `config.toml.example` (`[proto_engine]` gains `default` + `[proto_engine.overrides]` example with the full method-key list), `CLAUDE.md` (engine section: all methods migrated, config resolution order, remove the GET_MAP_FORTS std-only caveat, note push-gateway exception), `decoder/main.go` (remove stale getMapFortsCache warning if Task 3 didn't)
- [ ] **Step 1: Docs accuracy pass** — every claim checked against code (config keys from config.go, method keys from decode.go dispatch, metric names).
- [ ] **Step 2: Full verification sweep** (build/vet/test/race pattern from prior waves) + `grep -rn '\*pogo\.' decode*.go decoder/*.go | grep -v _test` — the ONLY remaining hits must be push-gateway scalars and generated-adjacent code; list them in the report.
- [ ] **Step 3: Commit** — `docs: Wave 3 engine coverage and config resolution`

---

## Execution Notes

- Tasks strictly sequential. The Wave 1–2 plan's transformation rules, typed-nil rule, arena-lifetime invariant, and behavioral guardrails are incorporated by reference — implementers get both plan paths.
- The quest switch (Task 2) and battle-state decoding (Task 3) are the two spots most likely to surface constructs the transformation rules don't cover — NEEDS_CONTEXT over guessing.
- After Task 5: whole-branch delta review (merge-base = current PR head), push to PR #381.
