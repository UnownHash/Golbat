package geo

import (
	"os"
	"testing"

	"github.com/paulmach/orb/geojson"
)

// Regression: GeoJSON rings carry a closing duplicate vertex; fed raw into
// s2.LoopFromPoints that produces an INVALID loop (degenerate edge) whose
// Normalize can come out inverted — covering the planet minus the fence.
// In a real 100-fence project file, 14 city fences inverted this way:
// 6.3M interior cells and 4.5GB instead of ~4k cells and ~1MB (OOM on
// small hosts). Beernem (in testdata) is one of the 14; Gent is a healthy
// control. ringToS2Points must keep both sane.
func TestS2RingClosingDuplicateInversion(t *testing.T) {
	data, err := os.ReadFile("testdata/inverted_ring_regression.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	fc, err := geojson.UnmarshalFeatureCollection(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	l := BuildS2LookupFromFeatures(fc)
	total := l.CellCount() + len(l.edgeCells)
	// Two city fences: sane covering is O(hundreds); an inversion is O(millions).
	if total > 20000 {
		t.Fatalf("covering exploded: %d cells — inverted loop regression", total)
	}
	if l.CellCount() == 0 {
		t.Fatal("no interior cells — fences were wrongly skipped")
	}
}
