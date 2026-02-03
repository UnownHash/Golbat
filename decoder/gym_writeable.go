package decoder

import (
	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Gym implements Writeable
var _ writebehind.Writeable = (*Gym)(nil)

// WriteKey returns a unique key for this Gym (for squashing)
func (g *Gym) WriteKey() string {
	return "gym:" + g.Id
}

// WriteType returns the entity type name (for metrics)
func (g *Gym) WriteType() string {
	return "gym"
}

// WriteToDB performs the actual database write for this Gym
// This delegates to the shared direct write function
func (g *Gym) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	g.Lock()
	defer g.Unlock()
	return gymWriteDB(dbDetails, g, isNewRecord)
}
