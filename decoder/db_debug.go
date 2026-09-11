//go:build dbdebug

package decoder

import (
	"strings"

	log "github.com/sirupsen/logrus"
)

// dbDebugEnabled is true when built with -tags dbdebug
const dbDebugEnabled = true

// dbDebugLog logs a database operation with changed fields
func dbDebugLog(reason, entityType, id string, changedFields []string) {
	fields := ""
	if len(changedFields) > 0 {
		fields = " changed=[" + strings.Join(changedFields, ", ") + "]"
	}
	log.Debugf("[DB_%s] %s id=%s%s", reason, entityType, id, fields)
}

// debugChangeAccumulator is the real, dbdebug-build implementation of an
// entity's per-save change accumulator (see the `debug` field's doc comment
// on each entity struct, e.g. pokemon.go). Every Set* method appends a
// formatted "Field:old->new" description here; each entity's save path reads
// fields() into dbDebugLog for one aggregated log line per save, then
// reset()s it.
//
// Originally each entity carried its own `changedFields []string` field for
// this. High-cardinality entities (Pokemon, Spawnpoint, ...) can't afford a
// permanent 24-byte slice header on every cached instance, so the
// accumulator lives in a build-tag gated type instead, shared by all
// entities. See db_debug_off.go for the zero-sized production stub.
type debugChangeAccumulator struct {
	changedFields []string
}

// recordChange appends a pre-formatted "Field:old->new" description.
func (d *debugChangeAccumulator) recordChange(desc string) {
	d.changedFields = append(d.changedFields, desc)
}

// fields returns the accumulated descriptions for this save.
func (d *debugChangeAccumulator) fields() []string {
	return d.changedFields
}

// reset clears the accumulator after a save, reusing the backing array.
func (d *debugChangeAccumulator) reset() {
	d.changedFields = d.changedFields[:0]
}
