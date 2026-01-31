package decoder

import (
	"sync"

	"github.com/guregu/null/v6"
)

// Tappable struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Tappable struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

	Id                      uint64      `db:"id" json:"id"`
	Lat                     float64     `db:"lat" json:"lat"`
	Lon                     float64     `db:"lon" json:"lon"`
	FortId                  null.String `db:"fort_id" json:"fort_id"` // either fortId or spawnpointId are given
	SpawnId                 null.Int    `db:"spawn_id" json:"spawn_id"`
	Type                    string      `db:"type" json:"type"`
	Encounter               null.Int    `db:"pokemon_id" json:"pokemon_id"`
	ItemId                  null.Int    `db:"item_id" json:"item_id"`
	Count                   null.Int    `db:"count" json:"count"`
	ExpireTimestamp         null.Int    `db:"expire_timestamp" json:"expire_timestamp"`
	ExpireTimestampVerified bool        `db:"expire_timestamp_verified" json:"expire_timestamp_verified"`
	Updated                 int64       `db:"updated" json:"updated"`

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)
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
		ta.Lat = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Lat")
		}
	}
}

func (ta *Tappable) SetLon(v float64) {
	if !floatAlmostEqual(ta.Lon, v, floatTolerance) {
		ta.Lon = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Lon")
		}
	}
}

func (ta *Tappable) SetFortId(v null.String) {
	if ta.FortId != v {
		ta.FortId = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "FortId")
		}
	}
}

func (ta *Tappable) SetSpawnId(v null.Int) {
	if ta.SpawnId != v {
		ta.SpawnId = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "SpawnId")
		}
	}
}

func (ta *Tappable) SetType(v string) {
	if ta.Type != v {
		ta.Type = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Type")
		}
	}
}

func (ta *Tappable) SetEncounter(v null.Int) {
	if ta.Encounter != v {
		ta.Encounter = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Encounter")
		}
	}
}

func (ta *Tappable) SetItemId(v null.Int) {
	if ta.ItemId != v {
		ta.ItemId = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "ItemId")
		}
	}
}

func (ta *Tappable) SetCount(v null.Int) {
	if ta.Count != v {
		ta.Count = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "Count")
		}
	}
}

func (ta *Tappable) SetExpireTimestamp(v null.Int) {
	if ta.ExpireTimestamp != v {
		ta.ExpireTimestamp = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "ExpireTimestamp")
		}
	}
}

func (ta *Tappable) SetExpireTimestampVerified(v bool) {
	if ta.ExpireTimestampVerified != v {
		ta.ExpireTimestampVerified = v
		ta.dirty = true
		if dbDebugEnabled {
			ta.changedFields = append(ta.changedFields, "ExpireTimestampVerified")
		}
	}
}
