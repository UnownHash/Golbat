package decoder

import "github.com/puzpuzpuz/xsync/v4"

// Maintained max-expiry availability index. Each map holds, per distinct filter
// option, the latest expiry timestamp seen on any resident fort. Availability
// reads the maps instead of scanning fortLookupCache; see
// docs/superpowers/specs/2026-07-17-maintained-fort-availability-design.md.
//
// Quests are NOT here — they can be retracted mid-life (geofence clear, event
// swap) which a monotonic max-expiry cannot express, so they keep the reconcile
// aggregate (questConditionCount).

type showcaseKey struct {
	PokemonId int16
	Form      int16
	TypeId    int8
}

type invasionKey struct {
	Character      int16
	DisplayType    int16
	Confirmed      bool
	Slot1PokemonId int16
	Slot1Form      int16
}

type raidKey struct {
	RaidLevel int8
	PokemonId int16
	Form      int16
}

type battleKey struct {
	BattleLevel int8
	PokemonId   int16
	Form        int16
}

var (
	lureExpiry     *xsync.Map[int16, int64]
	showcaseExpiry *xsync.Map[showcaseKey, int64]
	invasionExpiry *xsync.Map[invasionKey, int64]
	raidExpiry     *xsync.Map[raidKey, int64]
	battleExpiry   *xsync.Map[battleKey, int64]
)

func initFortAvailability() {
	lureExpiry = xsync.NewMap[int16, int64]()
	showcaseExpiry = xsync.NewMap[showcaseKey, int64]()
	invasionExpiry = xsync.NewMap[invasionKey, int64]()
	raidExpiry = xsync.NewMap[raidKey, int64]()
	battleExpiry = xsync.NewMap[battleKey, int64]()
}

// observeExpiry records key as available until at least expiry, keeping the
// larger of any prior expiry (a still-active fort refreshes the lifetime).
// Already-expired observations are ignored so dead keys never enter the map.
func observeExpiry[K comparable](m *xsync.Map[K, int64], key K, expiry, now int64) {
	if expiry <= now {
		return
	}
	m.Compute(key, func(old int64, _ bool) (int64, xsync.ComputeOp) {
		if old >= expiry {
			return old, xsync.CancelOp
		}
		return expiry, xsync.UpdateOp
	})
}

// pruneExpired deletes key iff it is STILL expired. It must never be a blind
// Delete: that could race an observe that just refreshed the key and wrongly
// drop a live option. Compute re-checks under the key's lock.
func pruneExpired[K comparable](m *xsync.Map[K, int64], key K, now int64) {
	m.Compute(key, func(cur int64, loaded bool) (int64, xsync.ComputeOp) {
		if loaded && cur <= now {
			return 0, xsync.DeleteOp
		}
		return cur, xsync.CancelOp
	})
}

// readAvailable emits the distinct still-active options from a maintained
// index, converting each live key to its API shape and pruning expired keys as
// it goes. It uses the strong Map.Range (each key visited at most once —
// RangeRelaxed could emit a duplicate); prune-on-read is the conditional
// pruneExpired, never a blind Delete. Returns a non-nil empty slice, never nil.
func readAvailable[K comparable, V any](m *xsync.Map[K, int64], now int64, conv func(K) V) []V {
	out := []V{}
	m.Range(func(k K, exp int64) bool {
		if exp > now {
			out = append(out, conv(k))
		} else {
			pruneExpired(m, k, now)
		}
		return true
	})
	return out
}

func observeRaid(fl *FortLookup, now int64) {
	if fl.RaidLevel > 0 {
		observeExpiry(raidExpiry, raidKey{fl.RaidLevel, fl.RaidPokemonId, fl.RaidPokemonForm}, fl.RaidEndTimestamp, now)
	}
}

func readRaids(now int64) []ApiGymRaidAvailable {
	return readAvailable(raidExpiry, now, func(k raidKey) ApiGymRaidAvailable { return ApiGymRaidAvailable(k) })
}

// observeStationBattles records every distinct active battle option on a
// station: the StationBattles slice when present, else the top-battle scalar
// projection. Level-0 ("no battle") observations are gated out here since
// observeExpiry only gates on expiry, not on the level-0 sentinel.
func observeStationBattles(fl *FortLookup, now int64) {
	obs := func(level int8, id, form int16, end int64) {
		if level == 0 {
			return
		}
		observeExpiry(battleExpiry, battleKey{level, id, form}, end, now)
	}
	if len(fl.StationBattles) == 0 {
		obs(fl.BattleLevel, fl.BattlePokemonId, fl.BattlePokemonForm, fl.BattleEndTimestamp)
		return
	}
	for _, b := range fl.StationBattles {
		obs(b.BattleLevel, b.BattlePokemonId, b.BattlePokemonForm, b.BattleEndTimestamp)
	}
}

func readBattles(now int64) []ApiStationBattleAvailable {
	return readAvailable(battleExpiry, now, func(k battleKey) ApiStationBattleAvailable { return ApiStationBattleAvailable(k) })
}

// observePokestop records the lure and showcase options active on a pokestop.
// LureId 0 means no lure; ContestPokemonId 0 means no active showcase — both
// are gated here since observeExpiry only gates on expiry, not these sentinels.
func observePokestop(fl *FortLookup, now int64) {
	if fl.LureId != 0 {
		observeExpiry(lureExpiry, fl.LureId, fl.LureExpireTimestamp, now)
	}
	// A showcase is either pokemon-based (ContestPokemonId) or type-based
	// (ContestPokemonType, pokemon id 0 -> consumer key `h<type>`); surface both.
	if fl.ContestPokemonId != 0 || fl.ContestPokemonType != 0 {
		observeExpiry(showcaseExpiry, showcaseKey{fl.ContestPokemonId, fl.ContestPokemonForm, fl.ContestPokemonType}, fl.ShowcaseExpiry, now)
	}
}

// observeInvasion records the active invasion signature on one incident.
func observeInvasion(inc *FortLookupIncident, now int64) {
	observeExpiry(invasionExpiry, invasionKey{
		Character: inc.Character, DisplayType: int16(inc.DisplayType), Confirmed: inc.Confirmed,
		Slot1PokemonId: inc.Slot1PokemonId, Slot1Form: inc.Slot1Form,
	}, inc.ExpireTimestamp, now)
}

func readLures(now int64) []ApiPokestopLureAvailable {
	return readAvailable(lureExpiry, now, func(k int16) ApiPokestopLureAvailable { return ApiPokestopLureAvailable{LureId: k} })
}

func readShowcases(now int64) []ApiPokestopShowcaseAvailable {
	return readAvailable(showcaseExpiry, now, func(k showcaseKey) ApiPokestopShowcaseAvailable { return ApiPokestopShowcaseAvailable(k) })
}

func readInvasions(now int64) []ApiPokestopInvasionAvailable {
	return readAvailable(invasionExpiry, now, func(k invasionKey) ApiPokestopInvasionAvailable { return ApiPokestopInvasionAvailable(k) })
}
