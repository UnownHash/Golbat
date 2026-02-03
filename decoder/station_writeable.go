package decoder

import (
	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Station implements Writeable
var _ writebehind.Writeable = (*Station)(nil)

// WriteKey returns a unique key for this Station (for squashing)
func (s *Station) WriteKey() string {
	return "station:" + s.Id
}

// WriteType returns the entity type name (for metrics)
func (s *Station) WriteType() string {
	return "station"
}

// WriteToDB performs the actual database write for this Station
// This delegates to the shared direct write function
func (s *Station) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	s.Lock()
	defer s.Unlock()
	return stationWriteDB(dbDetails, s, isNewRecord)
}
