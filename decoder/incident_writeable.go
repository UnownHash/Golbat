package decoder

import (
	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Incident implements Writeable
var _ writebehind.Writeable = (*Incident)(nil)

// WriteKey returns a unique key for this Incident (for squashing)
func (i *Incident) WriteKey() string {
	return "incident:" + i.Id
}

// WriteType returns the entity type name (for metrics)
func (i *Incident) WriteType() string {
	return "incident"
}

// WriteToDB performs the actual database write for this Incident
// This delegates to the shared direct write function
func (i *Incident) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	i.Lock()
	defer i.Unlock()
	return incidentWriteDB(dbDetails, i, isNewRecord)
}
