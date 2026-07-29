package decoder

import (
	"testing"
	"time"
)

// invalidateTreeSnapshots resets the shared scan snapshots between tests.
func invalidateTreeSnapshots() {
	pokemonTreeSnapshot.Store(nil)
	fortTreeSnapshot.Store(nil)
}

func TestPokemonTreeSnapshotIsReusedWithinMaxAge(t *testing.T) {
	invalidateTreeSnapshots()
	defer invalidateTreeSnapshots()

	s1 := getPokemonTreeSnapshot()
	s2 := getPokemonTreeSnapshot()
	if s1 != s2 {
		t.Error("expected snapshot to be reused within max age")
	}
}

func TestPokemonTreeSnapshotRefreshesAfterMaxAge(t *testing.T) {
	invalidateTreeSnapshots()
	defer invalidateTreeSnapshots()

	s1 := getPokemonTreeSnapshot()
	// Backdate the stored snapshot instead of sleeping.
	if snap := pokemonTreeSnapshot.Load(); snap != nil {
		snap.createdAt = time.Now().Add(-2 * treeSnapshotMaxAge)
	}
	s2 := getPokemonTreeSnapshot()
	if s1 == s2 {
		t.Error("expected a fresh snapshot after max age")
	}
}

func TestFortTreeSnapshotIsReusedWithinMaxAge(t *testing.T) {
	invalidateTreeSnapshots()
	defer invalidateTreeSnapshots()

	s1 := getFortTreeSnapshot()
	s2 := getFortTreeSnapshot()
	if s1 != s2 {
		t.Error("expected fort snapshot to be reused within max age")
	}
}
