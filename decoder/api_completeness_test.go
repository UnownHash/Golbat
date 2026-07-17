package decoder

import (
	"reflect"
	"strings"
	"testing"
)

// collectDbColumns walks a struct type — recursing into embedded anonymous
// structs (e.g. Pokestop embeds PokestopData) — and returns every persisted DB
// column: the `db:"..."` tag value, excluding db:"-" (memory-only) fields.
func collectDbColumns(t reflect.Type) []string {
	var cols []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			cols = append(cols, collectDbColumns(f.Type)...)
			continue
		}
		name := strings.Split(f.Tag.Get("db"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

// collectJsonFields returns the set of top-level json field names on a struct
// (stripping ,omitempty), skipping json:"-" and untagged fields.
func collectJsonFields(t reflect.Type) map[string]bool {
	m := make(map[string]bool)
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		m[name] = true
	}
	return m
}

// TestApiResultsExposeEveryDbColumn locks the whole-record invariant: every
// persisted DB column of a fort record must be exposed on its API result
// struct. Adding a DB column without exposing it fails here rather than
// silently dropping data downstream — e.g. a ReactMap filter key that reads a
// column the API never sent (the quest_item_id bug that motivated this test).
//
// If a column is deliberately internal, add it to that case's `allow` set WITH
// a comment explaining why — do not weaken the assertion.
func TestApiResultsExposeEveryDbColumn(t *testing.T) {
	cases := []struct {
		name    string
		dbType  reflect.Type
		apiType reflect.Type
		allow   map[string]bool // db columns intentionally not exposed (with reason)
	}{
		{"pokestop", reflect.TypeOf(Pokestop{}), reflect.TypeOf(ApiPokestopResult{}), nil},
		{"station", reflect.TypeOf(Station{}), reflect.TypeOf(ApiStationResult{}), nil},
		{"gym", reflect.TypeOf(Gym{}), reflect.TypeOf(ApiGymResult{}), nil},
		{"incident", reflect.TypeOf(Incident{}), reflect.TypeOf(ApiPokestopIncident{}), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cols := collectDbColumns(c.dbType)
			if len(cols) == 0 {
				t.Fatalf("no db columns found for %s — reflection is broken, the test would pass vacuously", c.dbType.Name())
			}
			exposed := collectJsonFields(c.apiType)
			for _, col := range cols {
				if c.allow[col] {
					continue
				}
				if !exposed[col] {
					t.Errorf("DB column %q of %s is not exposed as a json field on %s.\n"+
						"Add `json:%q` to %s, or if it is intentionally internal add it to this case's allow set with a reason.",
						col, c.dbType.Name(), c.apiType.Name(), col, c.apiType.Name())
				}
			}
		})
	}
}
