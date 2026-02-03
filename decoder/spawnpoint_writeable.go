package decoder

import (
	"fmt"

	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Spawnpoint implements Writeable
var _ writebehind.Writeable = (*Spawnpoint)(nil)

// WriteKey returns a unique key for this Spawnpoint (for squashing)
func (s *Spawnpoint) WriteKey() string {
	return fmt.Sprintf("spawnpoint:%d", s.Id)
}

// WriteType returns the entity type name (for metrics)
func (s *Spawnpoint) WriteType() string {
	return "spawnpoint"
}

// WriteToDB performs the actual database write for this Spawnpoint
// This delegates to the shared direct write function
func (s *Spawnpoint) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	s.Lock()
	defer s.Unlock()
	return spawnpointWriteDB(dbDetails, s)
}
