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

// pokemonDebugState is the production stub for Pokemon's per-save change
// accumulator (see db_debug.go for the real implementation and the `debug`
// field's doc comment in pokemon.go). It is zero-sized: since it's not the
// last field in Pokemon, it contributes no bytes to unsafe.Sizeof(Pokemon{})
// and no word to the GC pointer bitmap, unlike the []string this replaces.
type pokemonDebugState struct{}

func (d *pokemonDebugState) recordChange(string) {}

func (d *pokemonDebugState) fields() []string { return nil }

func (d *pokemonDebugState) reset() {}
