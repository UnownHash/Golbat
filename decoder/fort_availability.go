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

func observeRaid(fl *FortLookup, now int64) {
	if fl.RaidLevel > 0 {
		observeExpiry(raidExpiry, raidKey{fl.RaidLevel, fl.RaidPokemonId, fl.RaidPokemonForm}, fl.RaidEndTimestamp, now)
	}
}

// readRaids emits the distinct active raid options, pruning expired keys.
// Strong Range (not RangeRelaxed): each key visited at most once.
func readRaids(now int64) []ApiGymRaidAvailable {
	out := []ApiGymRaidAvailable{}
	raidExpiry.Range(func(k raidKey, exp int64) bool {
		if exp > now {
			out = append(out, ApiGymRaidAvailable{RaidLevel: k.RaidLevel, PokemonId: k.PokemonId, Form: k.Form})
		} else {
			pruneExpired(raidExpiry, k, now)
		}
		return true
	})
	return out
}
