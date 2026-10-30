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

// tappableCacheOnSave stamps the per-entry TTL. Set, not UpdateTTL: the
// upsert re-caches a tappable evicted mid-save (see OtterCache).
func tappableCacheOnSave(t *Tappable, now int64) {
	tappableCache.Set(t.Id, t, tappableCacheTTL(t, now))
}
