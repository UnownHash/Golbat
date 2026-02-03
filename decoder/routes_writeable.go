package decoder

import (
	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Route implements Writeable
var _ writebehind.Writeable = (*Route)(nil)

// WriteKey returns a unique key for this Route (for squashing)
func (r *Route) WriteKey() string {
	return "route:" + r.Id
}

// WriteType returns the entity type name (for metrics)
func (r *Route) WriteType() string {
	return "route"
}

// WriteToDB performs the actual database write for this Route
// This delegates to the shared direct write function
func (r *Route) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	r.Lock()
	defer r.Unlock()
	return routeWriteDB(dbDetails, r, isNewRecord)
}
