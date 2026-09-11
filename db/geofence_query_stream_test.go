package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

func squareFence() *geojson.Feature {
	return geojson.NewFeature(orb.Polygon{{{10, 20}, {12, 20}, {12, 24}, {10, 24}, {10, 20}}})
}

// TestFenceQueriesHonourCancelledContext: every fence query must take the
// caller's context so an abandoned request releases its connection instead of
// streaming the whole candidate set to nobody.
func TestFenceQueriesHonourCancelledContext(t *testing.T) {
	sdb, _ := newFakeDb([]string{"id", "lat", "lon"}, nil, nil)
	dbd := DbDetails{GeneralDb: sdb}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := GetPokestopPositions(ctx, dbd, squareFence()); err == nil {
		t.Error("GetPokestopPositions ignored a cancelled context")
	}
	if _, err := GetQuestStatus(ctx, dbd, squareFence()); err == nil {
		t.Error("GetQuestStatus ignored a cancelled context")
	}
	if _, err := PokestopIdsWithinFence(ctx, dbd, squareFence()); err == nil {
		t.Error("PokestopIdsWithinFence ignored a cancelled context")
	}
}

// fenceQuery names one of the three fence queries so the tests below can run
// the same scenario against each.
type fenceQuery struct {
	name  string
	label string
	cols  []string
	row   []driver.Value
	run   func(ctx context.Context, dbd DbDetails, f *geojson.Feature) error
}

var fenceQueries = []fenceQuery{
	{
		name: "positions", label: "select pokestop-positions",
		cols: []string{"id", "lat", "lon"}, row: []driver.Value{"a", 22.0, 11.0},
		run: func(ctx context.Context, dbd DbDetails, f *geojson.Feature) error {
			_, err := GetPokestopPositions(ctx, dbd, f)
			return err
		},
	},
	{
		name: "quest-status", label: "select quest-status",
		cols: []string{"lat", "lon", "q", "aq"}, row: []driver.Value{22.0, 11.0, int64(1), int64(0)},
		run: func(ctx context.Context, dbd DbDetails, f *geojson.Feature) error {
			_, err := GetQuestStatus(ctx, dbd, f)
			return err
		},
	},
	{
		name: "ids", label: "select pokestops for quest removal",
		cols: []string{"id", "lat", "lon"}, row: []driver.Value{"a", 22.0, 11.0},
		run: func(ctx context.Context, dbd DbDetails, f *geojson.Feature) error {
			_, err := PokestopIdsWithinFence(ctx, dbd, f)
			return err
		},
	},
}

// TestFenceQueriesRecordStreamErrorsInStats: the streaming paths must report
// the error that ends the stream, not just the error from opening the query.
// A connection drop or mid-stream deadline surfaces only through rows.Err(),
// and must not be counted as a successful query.
func TestFenceQueriesRecordStreamErrorsInStats(t *testing.T) {
	for _, q := range fenceQueries {
		t.Run(q.name, func(t *testing.T) {
			errStream := errors.New("connection dropped mid-stream")
			sdb, _ := newFakeDb(q.cols, [][]driver.Value{q.row}, errStream)
			cs := installCaptureStats(t)

			err := q.run(context.Background(), DbDetails{GeneralDb: sdb}, squareFence())
			if !errors.Is(err, errStream) {
				t.Fatalf("returned %v, want the stream error", err)
			}
			calls := cs.calls[q.label]
			if len(calls) != 1 {
				t.Fatalf("IncDbQuery(%q) called %d times, want 1", q.label, len(calls))
			}
			if !errors.Is(calls[0], errStream) {
				t.Fatalf("IncDbQuery(%q) recorded %v, want the stream error", q.label, calls[0])
			}
		})
	}
}

// TestFenceQueriesUseInclusiveBoundingBox: the Go matcher counts the fence
// boundary as inside, so the SQL pre-filter must not exclude a stop sitting
// exactly on the bounding box edge. All three queries bind the same clause.
func TestFenceQueriesUseInclusiveBoundingBox(t *testing.T) {
	const want = "WHERE lat >= ? AND lon >= ? AND lat <= ? AND lon <= ? AND enabled = 1"
	for _, q := range fenceQueries {
		t.Run(q.name, func(t *testing.T) {
			sdb, cap := newFakeDb(q.cols, nil, nil)
			if err := q.run(context.Background(), DbDetails{GeneralDb: sdb}, squareFence()); err != nil {
				t.Fatal(err)
			}
			query, _ := cap.last()
			if !strings.Contains(query, want) {
				t.Fatalf("query %q does not bind the inclusive bounding box %q", query, want)
			}
		})
	}
}

// TestFenceQueriesRejectNilGeometry: a feature whose geometry is nil must be
// answered with an error, not a nil-pointer panic in the fallback branch.
func TestFenceQueriesRejectNilGeometry(t *testing.T) {
	for _, q := range fenceQueries {
		t.Run(q.name, func(t *testing.T) {
			sdb, _ := newFakeDb(q.cols, nil, nil)
			if err := q.run(context.Background(), DbDetails{GeneralDb: sdb}, geojson.NewFeature(nil)); err == nil {
				t.Fatal("query accepted a nil geometry")
			}
		})
	}
}

// TestNewFenceMatcherToleratesNonStringNameProperty pins the request-body
// regression at the db layer: a numeric "name" property must not panic.
func TestNewFenceMatcherToleratesNonStringNameProperty(t *testing.T) {
	fence := squareFence()
	fence.Properties["name"] = 123
	if _, err := newFenceMatcher(fence); err != nil {
		t.Fatalf("polygon fence with a numeric name did not compile: %v", err)
	}
}

// TestFenceQueriesRejectNonPolygonFence: there is no SQL fallback. A fence
// that is not an area is an error before any query is issued.
func TestFenceQueriesRejectNonPolygonFence(t *testing.T) {
	for _, q := range fenceQueries {
		t.Run(q.name, func(t *testing.T) {
			sdb, cap := newFakeDb(q.cols, nil, nil)
			err := q.run(context.Background(), DbDetails{GeneralDb: sdb}, geojson.NewFeature(orb.Point{11, 22}))
			if err == nil {
				t.Fatal("point fence accepted")
			}
			if query, _ := cap.last(); query != "" {
				t.Fatalf("point fence reached the database: %q", query)
			}
		})
	}
}
