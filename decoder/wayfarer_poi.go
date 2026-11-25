package decoder

import (
	"fmt"

	"github.com/guregu/null/v6"
)

// WayfarerPoiData contains all database-persisted fields for WayfarerPoi.
// This struct is embedded in WayfarerPoi and can be safely copied for write-behind queueing.
//
// A Wayfarer POI is a point of interest of the Niantic geodata service. Its id
// is the fort id when the POI is in the game, but most POIs are not: the id is
// kept as the string the server sent rather than a FortId.
type WayfarerPoiData struct {
	Id                 string      `db:"id"`
	Lat                float64     `db:"lat"`
	Lon                float64     `db:"lon"`
	Name               null.String `db:"name"`
	Description        null.String `db:"description"`
	Url                null.String `db:"url"`
	InGame             bool        `db:"in_game"`
	FirstSeenTimestamp int64       `db:"first_seen_timestamp"`
	Updated            int64       `db:"updated"`
}

// WayfarerPoi struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type WayfarerPoi struct {
	mu TrackedMutex[string] `db:"-" json:"-"` // Object-level mutex with contention tracking

	WayfarerPoiData // Embedded data fields - can be copied for write-behind queue

	dirty     bool `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-" json:"-"` // Not persisted - tracks if this is a new record

	// debug accumulates per-field change descriptions for dbDebugLog (see
	// debugChangeAccumulator in db_debug.go).
	debug debugChangeAccumulator `db:"-" json:"-"`

	oldValues WayfarerPoiData `db:"-" json:"-"` // Old values for webhook comparison
}

// IsDirty returns true if any field has been modified
func (p *WayfarerPoi) IsDirty() bool {
	return p.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (p *WayfarerPoi) ClearDirty() {
	p.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (p *WayfarerPoi) IsNewRecord() bool {
	return p.newRecord
}

// Lock acquires the WayfarerPoi's mutex with caller tracking
func (p *WayfarerPoi) Lock(caller string) {
	p.mu.Lock(caller, "WayfarerPoi", p.Id)
}

// Unlock releases the WayfarerPoi's mutex
func (p *WayfarerPoi) Unlock() {
	p.mu.Unlock("WayfarerPoi", p.Id)
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (p *WayfarerPoi) snapshotOldValues() {
	p.oldValues = p.WayfarerPoiData
}

// --- Set methods with dirty tracking ---

func (p *WayfarerPoi) SetLat(v float64) {
	if !floatAlmostEqual(p.Lat, v, floatTolerance) {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("Lat:%f->%f", p.Lat, v))
		}
		p.Lat = v
		p.dirty = true
	}
}

func (p *WayfarerPoi) SetLon(v float64) {
	if !floatAlmostEqual(p.Lon, v, floatTolerance) {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("Lon:%f->%f", p.Lon, v))
		}
		p.Lon = v
		p.dirty = true
	}
}

func (p *WayfarerPoi) SetName(v null.String) {
	if p.Name != v {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("Name:%s->%s", FormatNull(p.Name), FormatNull(v)))
		}
		p.Name = v
		p.dirty = true
	}
}

func (p *WayfarerPoi) SetDescription(v null.String) {
	if p.Description != v {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("Description:%s->%s", FormatNull(p.Description), FormatNull(v)))
		}
		p.Description = v
		p.dirty = true
	}
}

func (p *WayfarerPoi) SetUrl(v null.String) {
	if p.Url != v {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("Url:%s->%s", FormatNull(p.Url), FormatNull(v)))
		}
		p.Url = v
		p.dirty = true
	}
}

func (p *WayfarerPoi) SetInGame(v bool) {
	if p.InGame != v {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("InGame:%t->%t", p.InGame, v))
		}
		p.InGame = v
		p.dirty = true
	}
}

func (p *WayfarerPoi) SetFirstSeenTimestamp(v int64) {
	if p.FirstSeenTimestamp != v {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("FirstSeenTimestamp:%d->%d", p.FirstSeenTimestamp, v))
		}
		p.FirstSeenTimestamp = v
		p.dirty = true
	}
}

func (p *WayfarerPoi) SetUpdated(v int64) {
	if p.Updated != v {
		if dbDebugEnabled {
			p.debug.recordChange(fmt.Sprintf("Updated:%d->%d", p.Updated, v))
		}
		p.Updated = v
		p.dirty = true
	}
}
