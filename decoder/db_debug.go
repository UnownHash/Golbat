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

// pokemonDebugState is the real, dbdebug-build implementation of Pokemon's
// per-save change accumulator (see the `debug` field's doc comment in
// pokemon.go). Every Set* method appends a formatted "Field:old->new"
// description here; pokemon_state.go's save path reads fields() into
// dbDebugLog for one aggregated log line per save, then reset()s it.
//
// This is the same shape gym.go/pokestop.go's changedFields []string field
// gives those entities — Pokemon just can't afford a permanent 24-byte slice
// header on every cached instance, so the accumulator lives in a build-tag
// gated type instead. See db_debug_off.go for the zero-sized production stub.
type pokemonDebugState struct {
	changedFields []string
}

// recordChange appends a pre-formatted "Field:old->new" description.
func (d *pokemonDebugState) recordChange(desc string) {
	d.changedFields = append(d.changedFields, desc)
}

// fields returns the accumulated descriptions for this save.
func (d *pokemonDebugState) fields() []string {
	return d.changedFields
}

// reset clears the accumulator after a save, reusing the backing array.
func (d *pokemonDebugState) reset() {
	d.changedFields = d.changedFields[:0]
}
