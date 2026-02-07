package decoder

import (
	"fmt"
	"sync"

	"github.com/guregu/null/v6"
)

// TappableData contains all database-persisted fields for Tappable.
// This struct is embedded in Tappable and can be safely copied for write-behind queueing.
type TappableData struct {
	Id                      uint64      `db:"id"`
	Lat                     float64     `db:"lat"`
	Lon                     float64     `db:"lon"`
	FortId                  null.String `db:"fort_id"` // either fortId or spawnpointId are given
	SpawnId                 null.Int    `db:"spawn_id"`
	Type                    string      `db:"type"`
	Encounter               null.Int    `db:"pokemon_id"`
	ItemId                  null.Int    `db:"item_id"`
	Count                   null.Int    `db:"count"`
	ExpireTimestamp         null.Int    `db:"expire_timestamp"`
	ExpireTimestampVerified bool        `db:"expire_timestamp_verified"`
	Updated                 int64       `db:"updated"`
}

// Tappable struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Tappable struct {
	mu sync.Mutex `db:"-"` // Object-level mutex

	TappableData // Embedded data fields - can be copied for write-behind queue

	dirty         bool     `db:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-"` // Track which fields changed (only when dbDebugEnabled)
}

// IsDirty returns true if any field has been modified
func (ta *Tappable) IsDirty() bool {
	return ta.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (ta *Tappable) ClearDirty() {
	ta.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (ta *Tappable) IsNewRecord() bool {
	return ta.newRecord
}

// Lock acquires the Tappable's mutex
func (ta *Tappable) Lock() {
	ta.mu.Lock()
}

// Unlock releases the Tappable's mutex
func (ta *Tappable) Unlock() {
	ta.mu.Unlock()
}

// --- Set methods with dirty tracking ---

func (ta *Tappable) SetLat(v float64) {
	if !floatAlmostEqual(ta.Lat, v, floatTolerance) {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("Lat:%f->%f", ta.Lat, v))
		}
		ta.Lat = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetLon(v float64) {
	if !floatAlmostEqual(ta.Lon, v, floatTolerance) {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("Lon:%f->%f", ta.Lon, v))
		}
		ta.Lon = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetFortId(v null.String) {
	if ta.FortId != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("FortId:%s->%s", FormatNull(ta.FortId), FormatNull(v)))
		}
		ta.FortId = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetSpawnId(v null.Int) {
	if ta.SpawnId != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("SpawnId:%s->%s", FormatNull(ta.SpawnId), FormatNull(v)))
		}
		ta.SpawnId = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetType(v string) {
	if ta.Type != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("Type:%s->%s", ta.Type, v))
		}
		ta.Type = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetEncounter(v null.Int) {
	if ta.Encounter != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("Encounter:%s->%s", FormatNull(ta.Encounter), FormatNull(v)))
		}
		ta.Encounter = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetItemId(v null.Int) {
	if ta.ItemId != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("ItemId:%s->%s", FormatNull(ta.ItemId), FormatNull(v)))
		}
		ta.ItemId = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetCount(v null.Int) {
	if ta.Count != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("Count:%s->%s", FormatNull(ta.Count), FormatNull(v)))
		}
		ta.Count = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetExpireTimestamp(v null.Int) {
	if ta.ExpireTimestamp != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("ExpireTimestamp:%s->%s", FormatNull(ta.ExpireTimestamp), FormatNull(v)))
		}
		ta.ExpireTimestamp = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetExpireTimestampVerified(v bool) {
	if ta.ExpireTimestampVerified != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("ExpireTimestampVerified:%t->%t", ta.ExpireTimestampVerified, v))
		}
		ta.ExpireTimestampVerified = v
		ta.dirty = true
	}
}

func (ta *Tappable) SetUpdated(v int64) {
	if ta.Updated != v {
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, fmt.Sprintf("Updated:%d->%d", ta.Updated, v))
		}
		ta.Updated = v
		ta.dirty = true
	}
}
