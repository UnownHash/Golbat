# Tappable Webhook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `tappable` webhook type that fires once per new or changed reward on a reward tappable, plus the verified-flag dirty fix.

**Architecture:** `webhooks` gets a `Tappable` type. `setRewards` marks `Tappable.rewardChanged`; `saveTappableRecord` sends a `TappableWebhook` after queuing the write when the tappable has a reward and is new or changed. `setExpireTimestamp` sets the verified flag once.

**Tech Stack:** Go, existing `webhooks` sender, `jsonenc` for golden JSON.

**Spec:** `docs/superpowers/specs/2026-10-28-tappable-webhook-design.md`

## Global Constraints

- Build/test with `-tags go_json`: `go test -tags go_json ./decoder/ ./webhooks/`.
- Branch `feat/tappable-webhook` (stacked on `feat/tappable-scan-api`). Do not push.
- Payload type and config string are both `"tappable"`.
- Payload fields exactly as spec §3; nullable fields are pointers, no `omitempty`; `id` is a decimal string.
- Trigger: `RewardType.Valid && (isNewRecord || rewardChanged)`; `rewardChanged` cleared after every save. A `Rewards`-only change never sets it.
- `Tappable` stays 264 bytes (`entity_sizes_test.go`).
- Comments and docs terse and factual.

## Review Focus

1. Reward → encounter → same reward again must send on the return (the flag from the encounter switch is cleared without sending) → Task 3 `TestTappableWebhookRewardReturnsAfterEncounter`.
2. A DB-loaded tappable whose reward then changes must send (not new, but changed) → Task 3 `TestTappableWebhookDbLoadedRewardChange`.
3. Count-only change sends (Poracle may filter on amount) → Task 3 `TestTappableRewardChangedFlag` case `count changed`.
4. Fort tappable re-tapped with the same reward (expiry moves +120 s each tap) sends nothing → Task 3 `TestTappableWebhookSameRewardFortRetap`.
5. A tappable seen first as an encounter never sends → Task 3 `TestTappableWebhookTrigger` case `encounter`.

---

### Task 1: Verified-flag dirty fix

**Files:**
- Modify: `decoder/tappable_decode.go` (`setExpireTimestamp`)
- Test: `decoder/tappable_decode_test.go`

**Interfaces:**
- Produces: none (behavior only).

- [ ] **Step 1: Write the failing tests**
  - `TestTappableVerifiedSpawnRetapNotDirty`: put a spawnpoint in `spawnpointCache` (unique id, `DespawnSec = null.IntFrom(1800)`, cleanup deletes it); decode a reward tappable whose `request.Location` is `&pogo.TappableLocation{LocationId: &pogo.TappableLocation_SpawnpointId{SpawnpointId: <hex of id>}}` at `timestampMs = 1700000000000`; assert `ExpireTimestampVerified == true` and `ExpireTimestamp` valid; `ClearDirty()`; decode the same request again; assert `IsDirty() == false`.
  - `TestTappableFortRetapStillDirty`: fort tappable (`&pogo.TappableLocation{LocationId: &pogo.TappableLocation_FortId{FortId: <32-hex id>}}`), decode at `t`, `ClearDirty()`, decode at `t + 5000` ms → `IsDirty() == true` (expiry moved).

- [ ] **Step 2: Run** `go test -tags go_json ./decoder/ -run 'TestTappableVerifiedSpawnRetapNotDirty|TestTappableFortRetapStillDirty'` → first FAILS (dirty after re-tap), second PASSES.

- [ ] **Step 3: Implement** in `setExpireTimestamp`: local `verified := false`; the known-despawn branch sets `verified = true` instead of calling the setter; one `ta.SetExpireTimestampVerified(verified)` at the end. Expiry setters unchanged.

- [ ] **Step 4: Run** the Step 2 command and `go test -tags go_json ./decoder/ -run Tappable` → PASS.

- [ ] **Step 5: Commit** `fix: tappable verified flag no longer dirties every re-tap`

### Task 2: Webhook type and payload

**Files:**
- Modify: `webhooks/webhook.go` (`Tappable` const before `webhookTypesLength`, payload type, config string)
- Create: `decoder/tappable_webhook.go`
- Test: `webhooks/webhooks_test.go`, `decoder/tappable_webhook_test.go`

**Interfaces:**
- Produces:
  - `webhooks.Tappable WebhookType`
  - `type TappableWebhook struct` with json fields `id` (string), `latitude`, `longitude` (float64), `type` (string), `fort_id` (*string), `spawn_id` (*int64), `reward_type` (*string), `item_id`, `reward_pokemon_id`, `count` (*int64), `rewards` (*json.RawMessage), `expire_timestamp` (*int64), `expire_timestamp_verified` (bool), `updated` (int64)
  - `func buildTappableWebhook(t *Tappable) TappableWebhook` — reuse `jsonRaw` (`decoder/api_gym.go`) for `rewards`

- [ ] **Step 1: Write the failing tests**
  - `webhooks/webhooks_test.go` `TestTappableWebhookType`: `webhookConfigStringToType["tappable"]` equals `[]WebhookType{Tappable}`; `webhookTypeToPayloadType[Tappable] == "tappable"`.
  - `decoder/tappable_webhook_test.go` `TestTappableWebhookGolden`: tappable `{Id: 18446744073709551000, Lat: 47.3769, Lon: 8.5417, Type: "TAPPABLE_TYPE_BREAKFAST", SpawnId: 987654321, RewardType: "pokemon_candy", RewardPokemonId: 25, Count: 6, Rewards: `[{"pokemon_candy":25,"count":6}]`, ExpireTimestamp: 1761660000, ExpireTimestampVerified: true, Updated: 1761659400}` marshaled via `jsonenc.Marshal(buildTappableWebhook(...))` equals exactly
    `{"id":"18446744073709551000","latitude":47.3769,"longitude":8.5417,"type":"TAPPABLE_TYPE_BREAKFAST","fort_id":null,"spawn_id":987654321,"reward_type":"pokemon_candy","item_id":null,"reward_pokemon_id":25,"count":6,"rewards":[{"pokemon_candy":25,"count":6}],"expire_timestamp":1761660000,"expire_timestamp_verified":true,"updated":1761659400}`

- [ ] **Step 2: Run** `go test -tags go_json ./webhooks/ ./decoder/ -run 'TestTappableWebhookType|TestTappableWebhookGolden'` → FAIL (undefined).

- [ ] **Step 3: Implement** the const, both maps, `TappableWebhook`, `buildTappableWebhook`.

- [ ] **Step 4: Run** Step 2 command and `go test -tags go_json ./webhooks/` → PASS.

- [ ] **Step 5: Commit** `feat: tappable webhook type and payload`

### Task 3: Reward-changed flag and trigger

**Files:**
- Modify: `decoder/tappable.go` (`rewardChanged bool \`db:"-"\`` after `newRecord`), `decoder/tappable_decode.go` (`setRewards`), `decoder/tappable_state.go` (`saveTappableRecord`)
- Modify: `CLAUDE.md` (webhook types table row `| \`tappable\` | Tappable | \`tappable\` |` + one line: new/changed reward only, encounters via `pokemon`), `config.toml.example` (`"tappable"` in the `# types = [...]` comment, line 67)
- Test: `decoder/tappable_webhook_test.go`

**Interfaces:**
- Consumes: `webhooks.Tappable`, `buildTappableWebhook` (Task 2).
- Produces: `Tappable.rewardChanged`.

- [ ] **Step 1: Write the failing tests** (swap `webhooksSender` for a `recordingWebhooksSender` (`decoder/station_battle_test.go`) with restore; count `webhooks.Tappable` messages)
  - `TestTappableRewardChangedFlag` table on `setRewards` starting from a tappable with rare candy ×1 and `rewardChanged=false`: `same reward` → false; `count changed` (×2) → true; `species changed` (candy 25 → 26) → true; `to encounter` (all empty) → true; `rewards only` (same columns, different `Rewards` string) → false.
  - `TestTappableWebhookTrigger` via `saveTappableRecord(ctx, db.DbDetails{}, ta)` (cache + `tappableQueue` nil-safe path as other tappable save tests): `new reward` → 1 hook, payload `TappableWebhook.RewardType == "item"`; `same reward resaved` (set `Updated` dirty only) → 0; `changed reward` → 1; `encounter` (new, `Encounter` set, no reward) → 0; `clean DB-loaded` (not dirty, not new, unindexed) → 0; after each save `rewardChanged == false`.
  - `TestTappableWebhookRewardReturnsAfterEncounter`: reward → save (1) → encounter → save (0) → same reward → save (1); total 2.
  - `TestTappableWebhookDbLoadedRewardChange`: not-new tappable with reward A, `setRewards(B)`, save → 1.
  - `TestTappableWebhookSameRewardFortRetap`: fort tappable decoded twice via `updateFromProcessTappableProto` 5 s apart, saved after each → 1 hook total.
  - `entity_sizes_test.go` pin stays 264 (no edit; must still pass).

- [ ] **Step 2: Run** `go test -tags go_json ./decoder/ -run 'TappableRewardChanged|TappableWebhook'` → FAIL.

- [ ] **Step 3: Implement**
  - `setRewards`: compare the four column fields against current values before the setters; `ta.rewardChanged = ta.rewardChanged || changed`.
  - `saveTappableRecord`: after `tappableRtreeUpdateOnSave`, if `tappable.RewardType.Valid && (isNewRecord || tappable.rewardChanged)` → `webhooksSender.AddMessage(webhooks.Tappable, buildTappableWebhook(tappable), MatchStatsGeofence(tappable.Lat, tappable.Lon))`; then `tappable.rewardChanged = false`. Also clear it on the clean-save early return.
  - Docs edits listed under Files.

- [ ] **Step 4: Run** Step 2 command, then `go build -tags go_json ./... && go vet -tags go_json ./... && go test -tags go_json ./... && ~/go/bin/staticcheck -tags go_json ./decoder/ ./webhooks/ | grep -v U1000` → all PASS, no new staticcheck lines.

- [ ] **Step 5: Commit** `feat: send tappable webhook on new or changed reward`
