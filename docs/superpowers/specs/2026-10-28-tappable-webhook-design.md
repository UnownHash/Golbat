# Tappable Webhook — Design Spec

- **Date:** 2026-10-28
- **Status:** Built; revised after review on #431 (payload conventions, RewardType-only trigger, request guard)
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

`TappableWebhook` (`decoder/tappable_webhook.go`). Shared fields use the `pokemon` hook's names and encodings (`PokemonWebhook`, `decoder/pokemon_state.go`); reward fields use `null.Int`/`null.String`.

```jsonc
{
  "encounter_id": "12345678901234567890", // decimal string (exceeds 2^53)
  "spawnpoint_id": "3b7a1c",               // lowercase hex, "None" for fort tappables
  "pokestop_id": "None",                   // fort id for fort/lure tappables
  "pokestop_name": null,                   // null without a stop, "Unknown" if not found
  "latitude": 47.3769,
  "longitude": 8.5417,
  "type": "TAPPABLE_TYPE_BREAKFAST",
  "reward_type": "pokemon_candy",
  "item_id": null,
  "reward_pokemon_id": 25,
  "count": 6,
  "rewards": [{"pokemon_candy": 25, "count": 6}], // loot protojson, as the DB column/API
  "disappear_time": 1761660000,            // 0 when unknown
  "disappear_time_verified": true,
  "updated": 1761659400
}
```

- `pokestop_name` is read in `UpdateTappable` before the tappable lock (never two entity locks), only when the request has a fort id, and passed to `saveTappableRecord`.
- `rewards` keeps the loot shape (not the quest `[{type, info}]` shape); `webhooks.md` lists the `reward_type` values.
- `disappear_time` is unverified for fort/lure tappables (tap + 120 s) and for spawns with an unknown despawn (now + 20 min).

## 4. Trigger

- `Tappable.rewardChanged bool` (`db:"-"`), placed after `newRecord` (tail padding; `Tappable` stays 264 bytes, pinned in `entity_sizes_test.go`).
- `setRewards` sets it when `RewardType` changes. A tappable's reward does not change after it is first seen, so the hook means "a reward was first recorded".
- `saveTappableRecord`, after queuing the write: send when `RewardType.Valid` and (`isNewRecord` or `rewardChanged`), then clear `rewardChanged`. The clean-save path (DB-loaded, unchanged) sends nothing.
- Areas: `MatchStatsGeofence(lat, lon)`.
- Requests without a request proto or with encounter id 0 are ignored in `decodeTappable` (before the pokemon and tappable updates) and in `UpdateTappable`; otherwise they collapse onto tappable 0 at (0,0).

Switching to an encounter sets `rewardChanged` (the type becomes null) but sends nothing; a reward recorded again afterwards sends.

## 5. Verified-flag fix

`setExpireTimestamp` (`decoder/tappable_decode.go`) calls `SetExpireTimestampVerified(false)` then `SetExpireTimestampVerified(true)`. Since #336 replaced value comparison with setter dirty tracking, the flip marks every re-tap of a verified-spawn tappable dirty, writing identical values (introduced as harmless in #285).

Fix: compute `verified` locally, call `SetExpireTimestampVerified(verified)` once; expiry setters unchanged. A re-tap with the same known despawn is not dirty. Fort tappables (tap + 120 s) still write.

## 6. Tests

- Fix: decode a verified-spawn tappable twice, same timestamp → second decode not dirty; first decode sets expiry and `verified`; fort re-tap still dirty.
- Flag: first reward sets it; same type (even with another count) does not; type change does; switch to encounter does.
- Trigger, through `saveTappableRecord` with a never-started test queue: new reward → one hook; same reward re-saved → none; encounter → none; clean DB-loaded save → none; reward → encounter → reward → two; DB-loaded tappable whose first reward arrives → one; fort re-tap with the same reward → one total.
- Missing request / encounter id 0 → ignored in `decodeTappable` and `UpdateTappable`.
- Payload golden JSON (hex spawnpoint, `"None"`, `null`s, raw `rewards`), plus a fort case (`pokestop_name`, `disappear_time` 0).
- Registration: config string `"tappable"` maps to `Tappable`; payload type set.
- Size pin: `Tappable` still 264.

## 7. Docs and PR

- `webhooks.md`: `tappable` section (trigger, payload, `reward_type` values, expiry caveats), envelope `type` list, table of contents, accepted type strings.
- `CLAUDE.md`: `tappable` row in the webhook types table; fires when a reward is first recorded.
- `config.toml.example`: add `"tappable"` to the `types` comment.
- PR: draft from `feat/tappable-webhook` against `feat/tappable-scan-api`; retarget to `main` once #430 merges (rebase if squash-merged). Description carries the payload for the Poracle handler and notes encounters stay on the `pokemon` webhook.
