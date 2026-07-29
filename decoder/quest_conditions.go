package decoder

import "github.com/puzpuzpuz/xsync/v4"

// questConditionKey identifies one distinct quest option (reward + title +
// target) for a single quest slot. AR and no-AR slots are distinguished by
// WithAr so a fort that offers the same option under both slots contributes two
// keys. Reward fields mirror those held in FortLookup; Title/Target are the
// extra dimensions FortLookup deliberately omits and that this aggregate exists
// to carry.
type questConditionKey struct {
	WithAr     bool
	RewardType int16
	ItemId     int16
	Amount     int16
	PokemonId  int16
	FormId     int16
	Title      string
	Target     int32
}

// ApiQuestConditionResult is the endpoint-facing view of one quest option and
// how many resident forts currently offer it. Consumed by the available
// endpoint (Task 4).
type ApiQuestConditionResult struct {
	WithAr     bool   `json:"with_ar"`
	RewardType int16  `json:"reward_type"`
	ItemId     int16  `json:"item_id"`
	Amount     int16  `json:"amount"`
	PokemonId  int16  `json:"pokemon_id"`
	FormId     int16  `json:"form_id"`
	Title      string `json:"title"`
	Target     int32  `json:"target"`
	Count      int    `json:"count"`
}

// questConditionCount is the running aggregate: how many resident pokestops
// currently offer each quest option. Maintained as forts enter/leave/change,
// mirroring the pokemonFormCount pattern (see adjustPokemonFormCount). Entries
// are deleted when their count reaches zero.
var questConditionCount *xsync.Map[questConditionKey, int64]

// questFortKeys records the exact quest-condition keys each resident pokestop
// currently contributes to questConditionCount. FortLookup intentionally omits
// quest title/target, so this side map is the only place a fort's
// previously-counted keys can be recovered — it lets every change/eviction
// decrement exactly the keys that were incremented, independent of the evicted
// *Pokestop's (possibly since-changed) quest fields. Only forts that currently
// carry a quest hold an entry, so its footprint tracks the number of active
// quests, not the whole fort population.
var questFortKeys *xsync.Map[string, []questConditionKey]

func initQuestConditions() {
	questConditionCount = xsync.NewMap[questConditionKey, int64]()
	questFortKeys = xsync.NewMap[string, []questConditionKey]()
}

// adjustQuestConditions applies delta to the aggregate count of each key,
// deleting an entry once its count reaches zero (mirrors adjustPokemonFormCount).
func adjustQuestConditions(keys []questConditionKey, delta int64) {
	for _, k := range keys {
		questConditionCount.Compute(k, func(old int64, _ bool) (int64, xsync.ComputeOp) {
			if old+delta <= 0 {
				return 0, xsync.DeleteOp // delete entry when count reaches zero
			}
			return old + delta, xsync.UpdateOp
		})
	}
}

// questConditionKeysFromPokestop returns one key per present quest slot (no-AR
// slot first, then AR). Empty when the pokestop carries no quest. The fixed slot
// order makes the result directly comparable via questKeysEqual.
func questConditionKeysFromPokestop(p *Pokestop) []questConditionKey {
	var keys []questConditionKey
	// quest_* is the AR quest (Golbat decode writes quest_* when haveAr is
	// set), which ReactMap labels with_ar=true; alternative_quest_* is the
	// non-AR quest (with_ar=false).
	if p.QuestRewardType.Valid {
		keys = append(keys, questConditionKey{
			WithAr:     true,
			RewardType: int16(p.QuestRewardType.ValueOrZero()),
			ItemId:     int16(p.QuestItemId.ValueOrZero()),
			Amount:     int16(p.QuestRewardAmount.ValueOrZero()),
			PokemonId:  int16(p.QuestPokemonId.ValueOrZero()),
			FormId:     int16(p.QuestPokemonFormId.ValueOrZero()),
			Title:      p.QuestTitle.ValueOrZero(),
			Target:     int32(p.QuestTarget.ValueOrZero()),
		})
	}
	if p.AlternativeQuestRewardType.Valid {
		keys = append(keys, questConditionKey{
			WithAr:     false,
			RewardType: int16(p.AlternativeQuestRewardType.ValueOrZero()),
			ItemId:     int16(p.AlternativeQuestItemId.ValueOrZero()),
			Amount:     int16(p.AlternativeQuestRewardAmount.ValueOrZero()),
			PokemonId:  int16(p.AlternativeQuestPokemonId.ValueOrZero()),
			FormId:     int16(p.AlternativeQuestPokemonFormId.ValueOrZero()),
			Title:      p.AlternativeQuestTitle.ValueOrZero(),
			Target:     int32(p.AlternativeQuestTarget.ValueOrZero()),
		})
	}
	return keys
}

// questKeysEqual reports whether two key slices are identical in order and
// content. questConditionKeysFromPokestop emits a stable slot order, so plain
// element comparison is sufficient.
func questKeysEqual(a, b []questConditionKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reconcileFortQuestConditions makes questConditionCount reflect newKeys as the
// current contribution of fortId, decrementing whatever this fort contributed
// before. It is the single accounting primitive for a fort that still exists as
// a pokestop, called from updatePokestopLookup (the sole writer of a pokestop's
// FortLookup entry) so it fires uniformly on cache-miss load, save, quest change
// and startup preload.
//
// Concurrency: the count adjustment happens INSIDE the questFortKeys.Compute
// callback, which runs under the map's per-bucket lock. That makes the pair
// (swap this fort's stored keys, apply the matching +new/−old to the aggregate)
// a single atomic step, and it serializes every reconcile/removal for the same
// fortId. Without this, a concurrent add and remove could interleave so the
// add's increment lands after the remove has already deleted the tracker entry
// and decremented — orphaning a +1 with no tracker backing (a permanent leak).
// The nested lock order is always questFortKeys -> questConditionCount and never
// the reverse (GetAvailableQuestConditions only reads questConditionCount), so
// there is no deadlock. Callers of updatePokestopLookup hold the pokestop entity
// lock, but this primitive is self-contained and correct without it.
func reconcileFortQuestConditions(fortId string, newKeys []questConditionKey) {
	questFortKeys.Compute(fortId, func(old []questConditionKey, loaded bool) ([]questConditionKey, xsync.ComputeOp) {
		if questKeysEqual(old, newKeys) {
			return old, xsync.CancelOp // unchanged (covers the common no-quest and re-save cases)
		}
		adjustQuestConditions(old, -1)
		adjustQuestConditions(newKeys, +1)
		if len(newKeys) == 0 {
			return nil, xsync.DeleteOp // fort no longer offers any quest
		}
		return newKeys, xsync.UpdateOp
	})
}

// removeFortQuestConditions drops a fort's entire quest contribution — used when
// a pokestop leaves the in-memory index (eviction, deletion, conversion away
// from pokestop). The decrement runs inside the Compute callback (same atomicity
// and per-fort serialization as reconcileFortQuestConditions), so the amount
// removed is exactly what this fort last contributed; a fort with no quest (or
// already removed) is a cheap no-op.
func removeFortQuestConditions(fortId string) {
	questFortKeys.Compute(fortId, func(old []questConditionKey, loaded bool) ([]questConditionKey, xsync.ComputeOp) {
		if !loaded {
			return old, xsync.CancelOp
		}
		adjustQuestConditions(old, -1)
		return nil, xsync.DeleteOp
	})
}

// GetAvailableQuestConditions returns the distinct quest options currently
// offered by resident forts, with per-option counts. Consumed by the available
// endpoint (Task 4).
func GetAvailableQuestConditions() []ApiQuestConditionResult {
	var out []ApiQuestConditionResult
	questConditionCount.Range(func(k questConditionKey, count int64) bool {
		if count > 0 {
			out = append(out, ApiQuestConditionResult{
				WithAr:     k.WithAr,
				RewardType: k.RewardType,
				ItemId:     k.ItemId,
				Amount:     k.Amount,
				PokemonId:  k.PokemonId,
				FormId:     k.FormId,
				Title:      k.Title,
				Target:     k.Target,
				Count:      int(count),
			})
		}
		return true
	})
	return out
}
