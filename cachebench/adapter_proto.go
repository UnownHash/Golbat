package cachebench

import (
	"time"

	"golbat/cachebench/protocache"
)

// protoAdapter wraps the purpose-built prototype (candidate D).
type protoAdapter struct {
	c *protocache.Cache[uint64, *Entity]
}

func NewProtoAdapter(cfg Config) BenchCache {
	pcfg := protocache.Config[uint64, *Entity]{
		TouchOnHit: cfg.TouchOnHit,
		WheelTick:  cfg.SweepInterval,
	}
	if cfg.OnEvict != nil {
		onEvict := cfg.OnEvict
		pcfg.OnEviction = func(key uint64, v *Entity, reason protocache.Reason) {
			r := EvictExpired
			if reason == protocache.ReasonDeleted {
				r = EvictDeleted
			}
			onEvict(key, v, r)
		}
	}
	return &protoAdapter{c: protocache.New(pcfg)}
}

func (a *protoAdapter) Get(key uint64) (*Entity, bool) {
	return a.c.Get(key)
}

func (a *protoAdapter) Set(key uint64, v *Entity, ttl time.Duration) {
	a.c.Set(key, v, ttl)
}

func (a *protoAdapter) GetOrSetFunc(key uint64, factory func() *Entity, ttl time.Duration) (*Entity, bool) {
	return a.c.GetOrSetFunc(key, factory, ttl)
}

func (a *protoAdapter) Delete(key uint64) {
	a.c.Delete(key)
}

func (a *protoAdapter) Len() int {
	return a.c.Len()
}

func (a *protoAdapter) Range(fn func(key uint64, v *Entity) bool) {
	a.c.Range(fn)
}

func (a *protoAdapter) Close() {
	a.c.Close()
}
