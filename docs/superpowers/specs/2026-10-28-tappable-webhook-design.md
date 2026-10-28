# Tappable Webhook — Design Spec

- **Date:** 2026-10-28
- **Status:** Design approved, not built
- **Branch:** `feat/tappable-webhook`, stacked on `feat/tappable-scan-api` (PR #430)
- **Consumer:** Poracle (needs a Poracle-side handler; not part of this repo)

## 1. Problem

Reward tappables (candy, items, stardust, …) are decoded and stored but reach no notification bot. Poracle consumes Golbat webhooks; there is no tappable type.

Encounter tappables are out of scope: they already reach Poracle as `pokemon` webhooks (`seen_type` `tappable_encounter` / `tappable_lure_encounter`) with IVs.

## 2. Webhook type

`webhooks/webhook.go`:
- `WebhookType` value `Tappable` (before `webhookTypesLength`).
- Payload type `"tappable"`.
- Config string `"tappable"` → `[]WebhookType{Tappable}`.

Envelope as every Golbat hook: `{"type": "tappable", "message": {...}}`.

## 3. Payload

`TappableWebhook` (`decoder/tappable_webhook.go`), mirroring `ApiTappableResult` with Poracle coordinate names. Nullable fields are pointers (JSON `null`), no `omitempty`.

```jsonc
{
  "id": "12345678901234567890",     // decimal string (exceeds 2^53)
  "latitude": 47.3769,
  "longitude": 8.5417,
  "type": "TAPPABLE_TYPE_BREAKFAST",
  "fort_id": null,                   // fort/lure tappables
  "spawn_id": 987654321,             // spawnpoint tappables
  "reward_type": "pokemon_candy",
  "item_id": null,
  "reward_pokemon_id": 25,
  "count": 6,
  "rewards": [{"pokemon_candy": 25, "count": 6}],
  "expire_timestamp": 1761660000,
  "expire_timestamp_verified": true,
  "updated": 1761659400
}
```

`rewards` is the stored JSON passed through as a raw message.

## 4. Trigger

- `Tappable.rewardChanged bool` (`db:"-"`), placed after `newRecord` (tail padding; `Tappable` stays 264 bytes, pinned in `entity_sizes_test.go`).
- `setRewards` sets it when `RewardType`, `ItemId`, `RewardPokemonId` or `Count` changes value. A `Rewards`-only change does not set it (the JSON follows the columns; an extra item behind the first does not change what Poracle filters on).
- `saveTappableRecord`, after queuing the write: send when `RewardType.Valid` and (`isNewRecord` or `rewardChanged`), then clear `rewardChanged`. The clean-save path (DB-loaded, unchanged) sends nothing.
- Areas: `MatchStatsGeofence(lat, lon)`.

Result: one hook per new reward or reward change. Repeat taps with the same reward send nothing, including fort tappables whose expiry moves each tap. Encounters and tappables without a stored reward never send.

## 5. Verified-flag fix

`setExpireTimestamp` (`decoder/tappable_decode.go`) calls `SetExpireTimestampVerified(false)` then `SetExpireTimestampVerified(true)`. Since #336 replaced value comparison with setter dirty tracking, the flip marks every re-tap of a verified-spawn tappable dirty, writing identical values (introduced as harmless in #285).

Fix: compute `verified` locally, call `SetExpireTimestampVerified(verified)` once; expiry setters unchanged. A re-tap with the same known despawn is not dirty. Fort tappables (tap + 120 s) still write.

## 6. Tests

- Fix: decode a verified-spawn tappable twice, same timestamp → second decode not dirty (fails today); first decode sets expiry and `verified`.
- Flag: first reward sets it; same reward again does not; changed `count` or species does; switch to encounter does not; `Rewards`-only change does not.
- Trigger (capturing sender, as `recordingWebhooksSender`): new reward → one `Tappable` hook; same reward re-saved → none; changed reward → one more; encounter → none; clean DB-loaded save → none; flag cleared after send.
- Payload golden JSON snapshot (string id, `null`s, raw `rewards`).
- Registration: config string `"tappable"` maps to `Tappable`; payload type set (the `init()` check panics otherwise).
- Size pin: `Tappable` still 264.

## 7. Docs and PR

- `CLAUDE.md`: `tappable` row in the webhook types table; fires on new/changed reward only.
- `config.toml.example`: add `"tappable"` to the `types` comment.
- PR: draft from `feat/tappable-webhook` against `feat/tappable-scan-api`; retarget to `main` once #430 merges (rebase if squash-merged). Description carries the payload for the Poracle handler and notes encounters stay on the `pokemon` webhook.
