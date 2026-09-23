package decoder

import (
	"math/rand"
	"slices"
	"testing"
)

// dnfTestClause carries its request position so tests can check which
// clauses a bucket holds.
type dnfTestClause struct {
	n       int
	pokemon []ApiPokemonDnfId
}

func buildTestIndex(clauses []dnfTestClause) *dnfFilterIndex[dnfTestClause] {
	return buildDnfFilterIndex(clauses, func(c *dnfTestClause) []ApiPokemonDnfId { return c.pokemon })
}

// clauseNumbers returns the distinct clause positions across a lookup's
// bucket and extra slices, sorted.
func clauseNumbers(bucket, extra []dnfTestClause) []int {
	var out []int
	for _, c := range append(slices.Clip(bucket), extra...) {
		out = append(out, c.n)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// clauseApplies is the definition the index must implement: a clause applies
// when its pokemon list is empty or any entry matches the id (0 = any) and
// form (nil = any).
func clauseApplies(c dnfTestClause, pokemonId, form int16) bool {
	if len(c.pokemon) == 0 {
		return true
	}
	for _, id := range c.pokemon {
		if (id.Pokemon == 0 || id.Pokemon == pokemonId) && (id.Form == nil || *id.Form == form) {
			return true
		}
	}
	return false
}

func TestDnfFilterIndexMergesEveryApplicableKey(t *testing.T) {
	form0, form1 := int16(0), int16(1)
	clauses := []dnfTestClause{
		{n: 0, pokemon: []ApiPokemonDnfId{{Pokemon: 1}}}, // {1, -1}
		{n: 1}, // {-1, -1}
		{n: 2, pokemon: []ApiPokemonDnfId{{Pokemon: 0, Form: &form1}}},  // {-1, 1}
		{n: 3, pokemon: []ApiPokemonDnfId{{Pokemon: 16, Form: &form0}}}, // {16, 0}
		// Reaches {1, 1} through two keys.
		{n: 4, pokemon: []ApiPokemonDnfId{{Pokemon: 1}, {Pokemon: 1, Form: &form1}}},
	}
	ix := buildTestIndex(clauses)

	tests := []struct {
		name          string
		pokemon, form int16
		want          []int
	}{
		{"species key plus generic", 1, 0, []int{0, 1, 4}},
		{"species key plus form-only key", 1, 1, []int{0, 1, 2, 4}},
		{"exact key plus generic", 16, 0, []int{1, 3}},
		{"exact-key species, other form", 16, 1, []int{1, 2}},
		{"form-only key", 19, 1, []int{1, 2}},
		{"generic only", 19, 0, []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clauseNumbers(ix.clauses(tt.pokemon, tt.form)); !slices.Equal(got, tt.want) {
				t.Errorf("clauses(%d, %d) = %v, want %v", tt.pokemon, tt.form, got, tt.want)
			}
		})
	}
}

func TestDnfFilterIndexNoGenericClause(t *testing.T) {
	ix := buildTestIndex([]dnfTestClause{{n: 0, pokemon: []ApiPokemonDnfId{{Pokemon: 1}}}})
	if got := clauseNumbers(ix.clauses(16, 0)); len(got) != 0 {
		t.Errorf("unlisted species got clauses %v, want none", got)
	}
	if got := clauseNumbers(ix.clauses(1, 5)); !slices.Equal(got, []int{0}) {
		t.Errorf("listed species got %v, want [0]", got)
	}
}

// The index must return exactly the applicable clauses for any combination of
// keys.
func TestDnfFilterIndexMatchesDefinition(t *testing.T) {
	r := rand.New(rand.NewSource(414))
	randomId := func() ApiPokemonDnfId {
		id := ApiPokemonDnfId{Pokemon: int16(r.Intn(4))} // 0 = any
		if r.Intn(2) == 0 {
			f := int16(r.Intn(3))
			id.Form = &f
		}
		return id
	}
	for iter := 0; iter < 2000; iter++ {
		clauses := make([]dnfTestClause, 1+r.Intn(6))
		for i := range clauses {
			clauses[i].n = i
			for j := r.Intn(4); j > 0; j-- {
				clauses[i].pokemon = append(clauses[i].pokemon, randomId())
			}
		}
		ix := buildTestIndex(clauses)
		for pokemon := int16(1); pokemon <= 4; pokemon++ {
			for form := int16(0); form <= 3; form++ {
				var want []int
				for _, c := range clauses {
					if clauseApplies(c, pokemon, form) {
						want = append(want, c.n)
					}
				}
				bucket, extra := ix.clauses(pokemon, form)
				if got := clauseNumbers(bucket, extra); !slices.Equal(got, want) {
					t.Fatalf("clauses %+v: clauses(%d, %d) = %v, want %v", clauses, pokemon, form, got, want)
				}
				// A merged bucket is evaluated per candidate: request order,
				// each clause once.
				var order []int
				for _, c := range bucket {
					order = append(order, c.n)
				}
				if !slices.IsSorted(order) || len(slices.Compact(slices.Clone(order))) != len(order) {
					t.Fatalf("clauses %+v: bucket for (%d, %d) = %v, want ascending without duplicates", clauses, pokemon, form, order)
				}
			}
		}
	}
}

// Species keys and form-only keys must not be crossed into species × form
// buckets: the index stays linear in the request size.
func TestDnfFilterIndexStaysLinear(t *testing.T) {
	var ids []ApiPokemonDnfId
	for i := int16(1); i <= 1000; i++ {
		f := i
		ids = append(ids, ApiPokemonDnfId{Pokemon: i}, ApiPokemonDnfId{Pokemon: 0, Form: &f})
	}
	ix := buildTestIndex([]dnfTestClause{{n: 0, pokemon: ids}})
	if len(ix.keyed) > len(ids) {
		t.Fatalf("index holds %d buckets for %d pokemon entries", len(ix.keyed), len(ids))
	}
	if got := clauseNumbers(ix.clauses(5, 7)); !slices.Equal(got, []int{0}) {
		t.Errorf("clauses(5, 7) = %v, want [0]", got)
	}
}
