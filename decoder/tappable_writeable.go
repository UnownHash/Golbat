package decoder

import (
	"fmt"

	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Tappable implements Writeable
var _ writebehind.Writeable = (*Tappable)(nil)

// WriteKey returns a unique key for this Tappable (for squashing)
func (t *Tappable) WriteKey() string {
	return fmt.Sprintf("tappable:%d", t.Id)
}

// WriteType returns the entity type name (for metrics)
func (t *Tappable) WriteType() string {
	return "tappable"
}

// WriteToDB performs the actual database write for this Tappable
// This delegates to the shared direct write function
func (t *Tappable) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	t.Lock()
	defer t.Unlock()
	return tappableWriteDB(dbDetails, t, isNewRecord)
}
