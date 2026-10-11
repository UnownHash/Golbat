package decoder

import (
	"context"
	"errors"
	"fmt"
	"github.com/guregu/null/v6"
	"golbat/config"
	"golbat/tz"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPersonalShinyScan(t *testing.T) {
	// Pin the independently verified game calculation, including opaque IDs.
	for _, tc := range []struct {
		id                string
		encounter         uint64
		shiny, background float64
	}{
		{"0123456789abcdef", 1, 0.22137850522994995, 0.1431974172592163},
		{"Player-ID:ABC/123", 1, 0.3684830665588379, 0.9563266038894653},
		{" Trainer-é ", 1, 0.03772324323654175, 0.7365694642066956},
	} {
		input := make([]byte, 8+len(tc.id))
		copy(input[8:], tc.id)
		p := shinyPlayer{input: input}
		for i := 0; i < 2; i++ {
			shiny, background := p.rolls(tc.encounter)
			if got := float64(shiny) / shinyScale; got != tc.shiny {
				t.Fatalf("shiny roll=%v want=%v", got, tc.shiny)
			}
			if got := float64(background) / shinyScale; got != tc.background {
				t.Fatalf("background roll=%v want=%v", got, tc.background)
			}
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rates" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("incorrect rates request")
		}
		_, _ = w.Write([]byte(`{"rates":[{"pokemon":25,"form":0,"costume":0,"prediction":{"probability":0.1,"supported":true},"overrides":{"America/Phoenix":{"probability":0.05,"supported":true}},"background":{"probability":0.2,"supported":true},"background_overrides":{"America/Phoenix":{"probability":0.15,"supported":true}}}]}`))
	}))
	defer server.Close()
	oldConfig := config.Config.ShinyRates
	oldLimit := config.Config.Tuning.MaxPokemonResults
	config.Config.ShinyRates = config.ShinyRates{URL: server.URL, Token: "test"}
	config.Config.Tuning.MaxPokemonResults = 100
	t.Cleanup(func() {
		config.Config.ShinyRates = oldConfig
		config.Config.Tuning.MaxPokemonResults = oldLimit
		shinyRatesSnapshot.Store(nil)
	})
	shinyRatesSnapshot.Store(nil)
	if _, err := ScanPokemonWithShiny(context.Background(), ApiPokemonScan3{DnfFilters: []ApiPokemonDnfFilter3{{ShinyFor: []string{"x"}}}}); err == nil {
		t.Fatal("personal scan ran before rates loaded")
	}
	if err := refreshShinyRates(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pokemon{PokemonData: PokemonData{Id: 1, PokemonId: 25, Lat: 33.45, Lon: -112.07, SeenType: SeenTypeFrom(SeenTypeCodeWild), ExpireTimestamp: null.ValueFrom(uint32(time.Now().Unix() + 600))}}
	pokemonRtreePreloadInsert(p)
	pokemonCache.Set(1, p, time.Minute)
	pokemonTreeSnapshot.Store(nil)
	t.Cleanup(func() {
		pokemonCache.Delete(1)
		pokemonLookupCache.Delete(1)
		pokemonTreeMutex.Lock()
		pokemonTree.Delete([2]float64{p.Lon, p.Lat}, [2]float64{p.Lon, p.Lat}, 1)
		pokemonTreeMutex.Unlock()
		pokemonTreeSnapshot.Store(nil)
	})
	req := ApiPokemonScan3{Min: ApiLatLon{Lat: 33.44, Lon: -112.08}, Max: ApiLatLon{Lat: 33.46, Lon: -112.06}, DnfFilters: []ApiPokemonDnfFilter3{{ShinyFor: []string{"Player-ID:ABC/123"}}}}
	check := func(want int) {
		t.Helper()
		res, err := ScanPokemonWithShiny(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Pokemon) != want {
			t.Fatalf("got %d results want %d", len(res.Pokemon), want)
		}
	}
	check(0)
	req.DnfFilters[0].ShinyFor = append(req.DnfFilters[0].ShinyFor, " Trainer-é ")
	check(1)
	req.DnfFilters[0].Cp = &ApiPokemonDnfMinMax{Min: 100, Max: 200}
	check(0)
	req.DnfFilters[0].Cp = nil
	req.DnfFilters = append(req.DnfFilters, ApiPokemonDnfFilter3{Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, Iv: &ApiPokemonDnfMinMax{Min: 100, Max: 100}})
	check(1) // specific ordinary clause must not hide the shiny wildcard
	req.DnfFilters[0].ShinyFor = []string{"Player-ID:ABC/123"}
	req.DnfFilters[1].Iv = nil
	check(1)

	// Backgrounds use the next roll from the same generator.
	req.DnfFilters = []ApiPokemonDnfFilter3{{BackgroundFor: []string{"Player-ID:ABC/123"}}}
	check(0)
	req.DnfFilters[0].BackgroundFor = []string{"0123456789abcdef"}
	check(1)
	req.DnfFilters[0].ShinyFor = []string{"Player-ID:ABC/123"}
	check(0) // shiny_for and background_for in one clause are AND'd
	req.DnfFilters[0].ShinyFor = []string{" Trainer-é "}
	check(1)

	// Annotation marks results without filtering them.
	req.DnfFilters = []ApiPokemonDnfFilter3{{}}
	req.AnnotateFor = []string{"0123456789abcdef", "Player-ID:ABC/123", " Trainer-é "}
	res, err := ScanPokemonWithShiny(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pokemon) != 1 || !slices.Equal(res.Pokemon[0].ShinyFor, []string{" Trainer-é "}) ||
		!slices.Equal(res.Pokemon[0].BackgroundFor, []string{"0123456789abcdef"}) {
		t.Fatalf("annotations = %+v", res.Pokemon)
	}
	// Duplicate IDs are matched and reported once.
	dup := req
	dup.DnfFilters = []ApiPokemonDnfFilter3{{}}
	dup.AnnotateFor = []string{" Trainer-é ", " Trainer-é ", " Trainer-é "}
	if res, err := ScanPokemonWithShiny(context.Background(), dup); err != nil || len(res.Pokemon) != 1 || len(res.Pokemon[0].ShinyFor) != 1 {
		t.Fatalf("duplicate annotation: %v %+v", err, res)
	}
	if len(dup.AnnotateFor) != 3 {
		t.Fatal("caller's request was modified")
	}

	// A personal clause never hides ordinary clauses, even species-specific ones.
	req.AnnotateFor = nil
	req.DnfFilters = []ApiPokemonDnfFilter3{{}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, ShinyFor: []string{"Player-ID:ABC/123"}}}
	check(1)
	req.DnfFilters[0].Cp = &ApiPokemonDnfMinMax{Min: 100, Max: 200}
	check(0)
	req.DnfFilters[1].Pokemon = []ApiPokemonDnfId{{Pokemon: 26}}
	req.DnfFilters[1].ShinyFor = []string{" Trainer-é "}
	check(0) // the personal clause's pokemon list still applies
	req.DnfFilters[1].Pokemon = []ApiPokemonDnfId{{Pokemon: 25}}
	check(1)
	req.AnnotateFor = []string{"0123456789abcdef"}

	// Annotation alone keeps ordinary clause selection: the specific clause hides the wildcard.
	req.DnfFilters = []ApiPokemonDnfFilter3{{Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, Iv: &ApiPokemonDnfMinMax{Min: 100, Max: 100}}, {}}
	check(0)

	// Too many distinct IDs is a client error.
	many := make([]string, maxPersonalPlayers+1)
	for i := range many {
		many[i] = fmt.Sprint("player-", i)
	}
	req.DnfFilters = []ApiPokemonDnfFilter3{{ShinyFor: many}}
	if _, err := ScanPokemonWithShiny(context.Background(), req); !errors.Is(err, ErrPersonalRequest) {
		t.Fatalf("err = %v, want ErrPersonalRequest", err)
	}

	req.DnfFilters = []ApiPokemonDnfFilter3{{ShinyFor: []string{strings.Repeat("x", maxPersonalPlayerIDBytes+1)}}}
	if _, err := ScanPokemonWithShiny(context.Background(), req); !errors.Is(err, ErrPersonalRequest) {
		t.Fatalf("oversized ID: err = %v", err)
	}

	// Nearby sightings are never roll-eligible and are rejected before hashing.
	req.AnnotateFor = nil
	req.DnfFilters = []ApiPokemonDnfFilter3{{ShinyFor: []string{" Trainer-é "}}}
	check(1)
	p.SeenType = SeenTypeFrom(SeenTypeCodeNearbyStop)
	updatePokemonLookup(p, false, nil)
	check(0)
}

func BenchmarkPersonalRolls(b *testing.B) {
	id := "Player-ID:ABC/123"
	input := make([]byte, 8+len(id))
	copy(input[8:], id)
	p := shinyPlayer{input: input}
	var sink uint32
	for i := 0; i < b.N; i++ {
		shiny, background := p.rolls(uint64(i))
		sink += shiny ^ background
	}
	_ = sink
}

func TestShinyZonePerSpawn(t *testing.T) {
	// All four corners of this box are America/Denver, but the point inside is
	// America/Phoenix; overrides must follow the spawn, not the box.
	for _, c := range [][2]float64{{35.5, -111.2}, {35.5, -109.8}, {36.3, -111.2}, {36.3, -109.8}} {
		if z := tz.SearchTimezone(c[0], c[1]); z != "America/Denver" {
			t.Skipf("timezone data changed: corner %v is %s", c, z)
		}
	}
	s := &shinyScan{}
	if z := s.zoneAt(map[string]shinyPrediction{"America/Phoenix": {}}, 35.812, -110.195); z != "America/Phoenix" {
		t.Fatalf("zone = %q", z)
	}
}
