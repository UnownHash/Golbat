//go:build !dbdebug

package decoder

// dbDebugEnabled is false when dbdebug build tag is not set.
// The compiler will eliminate dead code in if statements checking this const.
const dbDebugEnabled = false

// dbDebugLog is a no-op stub when dbdebug build tag is not set.
// This function is never called at runtime due to const-folding of dbDebugEnabled.
func dbDebugLog(reason, entityType, id string, changedFields []string) {
	// No-op: this function exists only to satisfy the compiler.
	// It will never be called because dbDebugEnabled is false.
}

// debugChangeAccumulator is the production stub for an entity's per-save
// change accumulator (see db_debug.go for the real implementation and the
// `debug` field's doc comment on each entity struct, e.g. pokemon.go). It is
// zero-sized: as long as it is not the LAST field of the struct that embeds
// it, it contributes no bytes to that struct's unsafe.Sizeof and no word to
// the GC pointer bitmap, unlike the []string this replaces. A zero-sized
// field placed last forces Go to add a word of padding (to keep a
// past-the-end pointer valid), which would silently cancel the saving — see
// each entity's `debug` field placement comment for why it sits where it
// does.
type debugChangeAccumulator struct{}

func (d *debugChangeAccumulator) recordChange(string) {}

func (d *debugChangeAccumulator) fields() []string { return nil }

func (d *debugChangeAccumulator) reset() {}
