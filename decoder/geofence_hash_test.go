package decoder

import (
	"sync/atomic"
	"testing"
)

func TestGeofenceContentChangedGate(t *testing.T) {
	old := lastGeofenceHash
	lastGeofenceHash = atomic.Value{}
	defer func() { lastGeofenceHash = old }()

	if !geofenceContentChanged([]byte("v1")) {
		t.Error("first sighting of content must count as changed")
	}
	if geofenceContentChanged([]byte("v1")) {
		t.Error("identical content must not count as changed")
	}
	if !geofenceContentChanged([]byte("v2")) {
		t.Error("new content must count as changed")
	}
	if geofenceContentChanged([]byte("v2")) {
		t.Error("repeat of new content must not count as changed")
	}
	if !geofenceContentChanged(nil) {
		t.Error("nil source must always count as changed")
	}
	if geofenceContentChanged([]byte("v2")) {
		t.Error("nil sighting must not clobber the recorded hash")
	}
}
