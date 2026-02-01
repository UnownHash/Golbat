package decoder

import (
	"sync"

	"github.com/guregu/null/v6"
)

// Route struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Route struct {
	mu sync.Mutex `db:"-" json:"-"` // Object-level mutex

	Id               string      `db:"id"`
	Name             string      `db:"name"`
	Shortcode        string      `db:"shortcode"`
	Description      string      `db:"description"`
	DistanceMeters   int64       `db:"distance_meters"`
	DurationSeconds  int64       `db:"duration_seconds"`
	EndFortId        string      `db:"end_fort_id"`
	EndImage         string      `db:"end_image"`
	EndLat           float64     `db:"end_lat"`
	EndLon           float64     `db:"end_lon"`
	Image            string      `db:"image"`
	ImageBorderColor string      `db:"image_border_color"`
	Reversible       bool        `db:"reversible"`
	StartFortId      string      `db:"start_fort_id"`
	StartImage       string      `db:"start_image"`
	StartLat         float64     `db:"start_lat"`
	StartLon         float64     `db:"start_lon"`
	Tags             null.String `db:"tags"`
	Type             int8        `db:"type"`
	Updated          int64       `db:"updated"`
	Version          int64       `db:"version"`
	Waypoints        string      `db:"waypoints"`

	dirty         bool     `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord     bool     `db:"-" json:"-"` // Not persisted - tracks if this is a new record
	changedFields []string `db:"-" json:"-"` // Track which fields changed (only when dbDebugEnabled)

	oldValues RouteOldValues `db:"-" json:"-"` // Old values for webhook comparison
}

// RouteOldValues holds old field values for webhook comparison
type RouteOldValues struct {
	Version int64
}

// IsDirty returns true if any field has been modified
func (r *Route) IsDirty() bool {
	return r.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (r *Route) ClearDirty() {
	r.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (r *Route) IsNewRecord() bool {
	return r.newRecord
}

// Lock acquires the Route's mutex
func (r *Route) Lock() {
	r.mu.Lock()
}

// Unlock releases the Route's mutex
func (r *Route) Unlock() {
	r.mu.Unlock()
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (r *Route) snapshotOldValues() {
	r.oldValues = RouteOldValues{
		Version: r.Version,
	}
}

// --- Set methods with dirty tracking ---

func (r *Route) SetName(v string) {
	if r.Name != v {
		r.Name = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Name")
		}
	}
}

func (r *Route) SetShortcode(v string) {
	if r.Shortcode != v {
		r.Shortcode = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Shortcode")
		}
	}
}

func (r *Route) SetDescription(v string) {
	if r.Description != v {
		r.Description = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Description")
		}
	}
}

func (r *Route) SetDistanceMeters(v int64) {
	if r.DistanceMeters != v {
		r.DistanceMeters = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "DistanceMeters")
		}
	}
}

func (r *Route) SetDurationSeconds(v int64) {
	if r.DurationSeconds != v {
		r.DurationSeconds = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "DurationSeconds")
		}
	}
}

func (r *Route) SetEndFortId(v string) {
	if r.EndFortId != v {
		r.EndFortId = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndFortId")
		}
	}
}

func (r *Route) SetEndImage(v string) {
	if r.EndImage != v {
		r.EndImage = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndImage")
		}
	}
}

func (r *Route) SetEndLat(v float64) {
	if !floatAlmostEqual(r.EndLat, v, floatTolerance) {
		r.EndLat = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndLat")
		}
	}
}

func (r *Route) SetEndLon(v float64) {
	if !floatAlmostEqual(r.EndLon, v, floatTolerance) {
		r.EndLon = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "EndLon")
		}
	}
}

func (r *Route) SetImage(v string) {
	if r.Image != v {
		r.Image = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Image")
		}
	}
}

func (r *Route) SetImageBorderColor(v string) {
	if r.ImageBorderColor != v {
		r.ImageBorderColor = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "ImageBorderColor")
		}
	}
}

func (r *Route) SetReversible(v bool) {
	if r.Reversible != v {
		r.Reversible = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Reversible")
		}
	}
}

func (r *Route) SetStartFortId(v string) {
	if r.StartFortId != v {
		r.StartFortId = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartFortId")
		}
	}
}

func (r *Route) SetStartImage(v string) {
	if r.StartImage != v {
		r.StartImage = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartImage")
		}
	}
}

func (r *Route) SetStartLat(v float64) {
	if !floatAlmostEqual(r.StartLat, v, floatTolerance) {
		r.StartLat = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartLat")
		}
	}
}

func (r *Route) SetStartLon(v float64) {
	if !floatAlmostEqual(r.StartLon, v, floatTolerance) {
		r.StartLon = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "StartLon")
		}
	}
}

func (r *Route) SetTags(v null.String) {
	if r.Tags != v {
		r.Tags = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Tags")
		}
	}
}

func (r *Route) SetType(v int8) {
	if r.Type != v {
		r.Type = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Type")
		}
	}
}

func (r *Route) SetVersion(v int64) {
	if r.Version != v {
		r.Version = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Version")
		}
	}
}

func (r *Route) SetWaypoints(v string) {
	if r.Waypoints != v {
		r.Waypoints = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Waypoints")
		}
	}
}

func (r *Route) SetUpdated(v int64) {
	if r.Updated != v {
		r.Updated = v
		r.dirty = true
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, "Updated")
		}
	}
}
