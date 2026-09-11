package decoder

import (
	"reflect"
	"strings"
	"testing"
)

// selectColumnSet splits a SELECT column constant into its bare column names.
func selectColumnSet(columns string) map[string]bool {
	out := map[string]bool{}
	for _, c := range strings.Split(columns, ",") {
		if name := strings.TrimSpace(c); name != "" {
			out[name] = true
		}
	}
	return out
}

// dbTaggedColumns lists the db column names persisted by a *Data struct.
func dbTaggedColumns(v any) []string {
	ty := reflect.TypeOf(v)
	out := make([]string, 0, ty.NumField())
	for i := 0; i < ty.NumField(); i++ {
		switch tag := ty.Field(i).Tag.Get("db"); tag {
		case "", "-":
		default:
			out = append(out, tag)
		}
	}
	return out
}

// A column that is written but never selected silently resets on every
// cache-miss load: the entity comes back with the Go zero value and, for any
// column the upsert lists in ON DUPLICATE KEY UPDATE, writes that zero back
// over the stored row. showcase_focus and first_seen_timestamp were both lost
// this way, so the whole set is asserted rather than the two known cases.
func TestPokestopSelectColumnsCoverEveryPersistedField(t *testing.T) {
	selected := selectColumnSet(pokestopSelectColumns)
	for _, column := range dbTaggedColumns(PokestopData{}) {
		if !selected[column] {
			t.Errorf("pokestop column %q is persisted but missing from pokestopSelectColumns, so it never loads from the database", column)
		}
	}
}

func TestGymSelectColumnsCoverEveryPersistedField(t *testing.T) {
	selected := selectColumnSet(gymSelectColumns)
	for _, column := range dbTaggedColumns(GymData{}) {
		if !selected[column] {
			t.Errorf("gym column %q is persisted but missing from gymSelectColumns, so it never loads from the database", column)
		}
	}
}
