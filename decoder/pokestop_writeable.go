package decoder

import (
	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Pokestop implements Writeable
var _ writebehind.Writeable = (*Pokestop)(nil)

// WriteKey returns a unique key for this Pokestop (for squashing)
func (p *Pokestop) WriteKey() string {
	return "pokestop:" + p.Id
}

// WriteType returns the entity type name (for metrics)
func (p *Pokestop) WriteType() string {
	return "pokestop"
}

// WriteToDB performs the actual database write for this Pokestop
// This delegates to the shared direct write function
func (p *Pokestop) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	p.Lock()
	defer p.Unlock()
	return pokestopWriteDB(dbDetails, p, isNewRecord)
}
