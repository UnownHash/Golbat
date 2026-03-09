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
