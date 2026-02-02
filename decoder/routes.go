package decoder

import (
	"fmt"
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
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Name:%s->%s", r.Name, v))
		}
		r.Name = v
		r.dirty = true
	}
}

func (r *Route) SetShortcode(v string) {
	if r.Shortcode != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Shortcode:%s->%s", r.Shortcode, v))
		}
		r.Shortcode = v
		r.dirty = true
	}
}

func (r *Route) SetDescription(v string) {
	if r.Description != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Description:%s->%s", r.Description, v))
		}
		r.Description = v
		r.dirty = true
	}
}

func (r *Route) SetDistanceMeters(v int64) {
	if r.DistanceMeters != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("DistanceMeters:%d->%d", r.DistanceMeters, v))
		}
		r.DistanceMeters = v
		r.dirty = true
	}
}

func (r *Route) SetDurationSeconds(v int64) {
	if r.DurationSeconds != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("DurationSeconds:%d->%d", r.DurationSeconds, v))
		}
		r.DurationSeconds = v
		r.dirty = true
	}
}

func (r *Route) SetEndFortId(v string) {
	if r.EndFortId != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("EndFortId:%s->%s", r.EndFortId, v))
		}
		r.EndFortId = v
		r.dirty = true
	}
}

func (r *Route) SetEndImage(v string) {
	if r.EndImage != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("EndImage:%s->%s", r.EndImage, v))
		}
		r.EndImage = v
		r.dirty = true
	}
}

func (r *Route) SetEndLat(v float64) {
	if !floatAlmostEqual(r.EndLat, v, floatTolerance) {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("EndLat:%f->%f", r.EndLat, v))
		}
		r.EndLat = v
		r.dirty = true
	}
}

func (r *Route) SetEndLon(v float64) {
	if !floatAlmostEqual(r.EndLon, v, floatTolerance) {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("EndLon:%f->%f", r.EndLon, v))
		}
		r.EndLon = v
		r.dirty = true
	}
}

func (r *Route) SetImage(v string) {
	if r.Image != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Image:%s->%s", r.Image, v))
		}
		r.Image = v
		r.dirty = true
	}
}

func (r *Route) SetImageBorderColor(v string) {
	if r.ImageBorderColor != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("ImageBorderColor:%s->%s", r.ImageBorderColor, v))
		}
		r.ImageBorderColor = v
		r.dirty = true
	}
}

func (r *Route) SetReversible(v bool) {
	if r.Reversible != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Reversible:%t->%t", r.Reversible, v))
		}
		r.Reversible = v
		r.dirty = true
	}
}

func (r *Route) SetStartFortId(v string) {
	if r.StartFortId != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("StartFortId:%s->%s", r.StartFortId, v))
		}
		r.StartFortId = v
		r.dirty = true
	}
}

func (r *Route) SetStartImage(v string) {
	if r.StartImage != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("StartImage:%s->%s", r.StartImage, v))
		}
		r.StartImage = v
		r.dirty = true
	}
}

func (r *Route) SetStartLat(v float64) {
	if !floatAlmostEqual(r.StartLat, v, floatTolerance) {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("StartLat:%f->%f", r.StartLat, v))
		}
		r.StartLat = v
		r.dirty = true
	}
}

func (r *Route) SetStartLon(v float64) {
	if !floatAlmostEqual(r.StartLon, v, floatTolerance) {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("StartLon:%f->%f", r.StartLon, v))
		}
		r.StartLon = v
		r.dirty = true
	}
}

func (r *Route) SetTags(v null.String) {
	if r.Tags != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Tags:%s->%s", FormatNull(r.Tags), FormatNull(v)))
		}
		r.Tags = v
		r.dirty = true
	}
}

func (r *Route) SetType(v int8) {
	if r.Type != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Type:%d->%d", r.Type, v))
		}
		r.Type = v
		r.dirty = true
	}
}

func (r *Route) SetVersion(v int64) {
	if r.Version != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Version:%d->%d", r.Version, v))
		}
		r.Version = v
		r.dirty = true
	}
}

func (r *Route) SetWaypoints(v string) {
	if r.Waypoints != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Waypoints:%s->%s", r.Waypoints, v))
		}
		r.Waypoints = v
		r.dirty = true
	}
}

func (r *Route) SetUpdated(v int64) {
	if r.Updated != v {
		if dbDebugEnabled {
			r.changedFields = append(r.changedFields, fmt.Sprintf("Updated:%d->%d", r.Updated, v))
		}
		r.Updated = v
		r.dirty = true
	}
}
