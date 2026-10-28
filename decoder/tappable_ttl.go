package decoder

import "time"

// tappableCacheTTL keeps a tappable cached until 60 s past its expiry
// (1 min floor); without an expiry, 60 min.
func tappableCacheTTL(t *Tappable, now int64) time.Duration {
	if !t.ExpireTimestamp.Valid {
		return 60 * time.Minute
	}
	ttl := time.Duration(t.ExpireTimestamp.Int64+60-now) * time.Second
	return max(ttl, time.Minute)
}

// tappableCacheUpdateTTL is a test seam.
var tappableCacheUpdateTTL = func(id uint64, ttl time.Duration) { tappableCache.UpdateTTL(id, ttl) }

// tappableCacheOnSave caches a new tappable, or refreshes the TTL when its expiry changed.
func tappableCacheOnSave(t *Tappable, isNewRecord bool, now int64) {
	expire := t.ExpireTimestamp.Int64
	if isNewRecord {
		tappableCache.Set(t.Id, t, tappableCacheTTL(t, now))
	} else if expire != t.cachedExpire {
		tappableCacheUpdateTTL(t.Id, tappableCacheTTL(t, now))
	}
	t.cachedExpire = expire
}
