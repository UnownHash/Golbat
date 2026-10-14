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

	// Personal conditions are ordinary clause conditions under the #420
	// rules: only the most specific group that exists applies.
	shiny := []string{" Trainer-é "}
	notShiny := []string{"Player-ID:ABC/123"}
	form0 := int16(0)
	iv100 := &ApiPokemonDnfMinMax{Min: 100, Max: 100}
	req.AnnotateFor = nil
	for _, tc := range []struct {
		name    string
		filters []ApiPokemonDnfFilter3
		want    int
	}{
		{"everything else", []ApiPokemonDnfFilter3{{ShinyFor: shiny}}, 1},
		{"species group shadows everything else",
			[]ApiPokemonDnfFilter3{{ShinyFor: shiny}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, Iv: iv100}}, 0},
		{"client merges the shared clause into the species group",
			[]ApiPokemonDnfFilter3{{ShinyFor: shiny}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, Iv: iv100}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, ShinyFor: shiny}}, 1},
		{"a never-matching clause hides the species from my shinies",
			[]ApiPokemonDnfFilter3{{ShinyFor: shiny}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, ShinyFor: shiny, Iv: &ApiPokemonDnfMinMax{Min: 1, Max: 0}}}, 0},
		{"exact form shadows the species",
			[]ApiPokemonDnfFilter3{{Pokemon: []ApiPokemonDnfId{{Pokemon: 25, Form: &form0}}, ShinyFor: notShiny}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, ShinyFor: shiny}}, 0},
		{"species clause applies without an exact form",
			[]ApiPokemonDnfFilter3{{Pokemon: []ApiPokemonDnfId{{Pokemon: 25, Form: &form0}}, ShinyFor: shiny}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, ShinyFor: notShiny}}, 1},
		{"ordinary clause in the same group still ORs",
			[]ApiPokemonDnfFilter3{{Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}, ShinyFor: notShiny}, {Pokemon: []ApiPokemonDnfId{{Pokemon: 25}}}}, 1},
		{"other species' group does not apply",
			[]ApiPokemonDnfFilter3{{Pokemon: []ApiPokemonDnfId{{Pokemon: 26}}, ShinyFor: shiny}}, 0},
	} {
		req.DnfFilters = tc.filters
		res, err := ScanPokemonWithShiny(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Pokemon) != tc.want {
			t.Errorf("%s: got %d results want %d", tc.name, len(res.Pokemon), tc.want)
		}
	}
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

func TestPersonalDrawOrder(t *testing.T) {
	// Draws are taken only for rolls the variant has. Rolls for encounter 1
	// (see TestPersonalShinyScan): first/second draw per player.
	//   0123456789abcdef   0.2214 / 0.1432
	//   Player-ID:ABC/123  0.3685 / 0.9563
	//    Trainer-é         0.0377 / 0.7366
	players := []string{"0123456789abcdef", "Player-ID:ABC/123", " Trainer-é "}
	background := &shinyPrediction{Probability: 0.2, Supported: true}
	s := &shinyScan{ctx: context.Background(), players: map[string]*shinyPlayer{}, rates: &shinyRates{variants: map[shinyVariant]shinyRate{
		{25, 0, 0}: {Pokemon: 25, Prediction: shinyPrediction{Probability: 0.1, Supported: true}, Background: background},
		{26, 0, 0}: {Pokemon: 26, Prediction: shinyPrediction{Supported: false}, Background: background},
		{29, 0, 0}: {Pokemon: 29, Prediction: shinyPrediction{Probability: 0, Supported: true}, Background: background},
	}}}
	for _, id := range players {
		input := make([]byte, 8+len(id))
		copy(input[8:], id)
		s.players[id] = &shinyPlayer{input: input}
	}
	seen := "wild"
	annotate := func(pokemonId int16) ApiPokemonResult {
		r := ApiPokemonResult{Id: "1", PokemonId: pokemonId, SeenType: &seen}
		s.annotate(&r, players)
		return r
	}
	// Shiny-capable: shiny is the first draw, background the second.
	if r := annotate(25); !slices.Equal(r.ShinyFor, []string{" Trainer-é "}) || !slices.Equal(r.BackgroundFor, []string{"0123456789abcdef"}) {
		t.Fatalf("capable: shiny %v background %v", r.ShinyFor, r.BackgroundFor)
	}
	// Never shiny, whether unsupported or at probability 0: no shiny draw,
	// so the background is the first draw.
	for _, pokemon := range []int16{26, 29} {
		if r := annotate(pokemon); len(r.ShinyFor) != 0 || !slices.Equal(r.BackgroundFor, []string{" Trainer-é "}) {
			t.Fatalf("%d not capable: shiny %v background %v", pokemon, r.ShinyFor, r.BackgroundFor)
		}
	}
	// Filtering agrees with annotation.
	s.rates.backgroundMaximum = map[int16]uint32{26: background.cutoff(0), 29: background.cutoff(0)}
	for _, pokemon := range []int16{26, 29} {
		lookup := PokemonLookup{PokemonId: pokemon, RollEligibleCostume: 1}
		for _, id := range players {
			want := id == " Trainer-é "
			if got := s.match(1, &lookup, [2]float64{0, 0}, &ApiPokemonDnfFilter3{BackgroundFor: []string{id}}); got != want {
				t.Errorf("%d: match background_for %q = %v, want %v", pokemon, id, got, want)
			}
		}
	}
}

func TestShinyRatesRejectMissingList(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"error":"down"}`, `{"rates":null}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		old := config.Config.ShinyRates
		config.Config.ShinyRates = config.ShinyRates{URL: server.URL}
		good := &shinyRates{}
		shinyRatesSnapshot.Store(good)
		if err := refreshShinyRates(context.Background()); err == nil {
			t.Errorf("%s: accepted", body)
		}
		if shinyRatesSnapshot.Load() != good {
			t.Errorf("%s: replaced the last good snapshot", body)
		}
		config.Config.ShinyRates = old
		shinyRatesSnapshot.Store(nil)
		server.Close()
	}
}

func TestShinyRatesUnsupportedSpecies(t *testing.T) {
	// 931 has no entry but is listed as never shiny; 25 has no entry and is not.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"rates":[],"unsupportedPokemon":[132,931]}`))
	}))
	defer server.Close()
	old := config.Config.ShinyRates
	config.Config.ShinyRates = config.ShinyRates{URL: server.URL}
	defer func() { config.Config.ShinyRates = old; shinyRatesSnapshot.Store(nil) }()
	if err := refreshShinyRates(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := &shinyScan{rates: shinyRatesSnapshot.Load()}
	rate931, ok931 := s.rate(931, 2601, 0)
	rate25, ok25 := s.rate(25, 0, 0)
	if cutoff, capable := s.shinyCutoff(rate931, ok931, 0, 0), shinyCapable(rate931, ok931); capable || cutoff != 0 {
		t.Errorf("unsupported species: cutoff %d capable %v", cutoff, capable)
	}
	if cutoff, capable := s.shinyCutoff(rate25, ok25, 0, 0), shinyCapable(rate25, ok25); !capable || cutoff != defaultShinyCutoff {
		t.Errorf("unlisted species: cutoff %d capable %v", cutoff, capable)
	}
}

func TestShinyCapabilityIgnoresTimezone(t *testing.T) {
	// Capability comes from the base rate; a timezone override changes only
	// the cutoff. Rolls as in TestPersonalDrawOrder.
	if z := tz.SearchTimezone(33.45, -112.07); z != "America/Phoenix" {
		t.Skipf("timezone data changed: %s", z)
	}
	players := []string{"0123456789abcdef", "Player-ID:ABC/123", " Trainer-é "}
	background := &shinyPrediction{Probability: 0.2, Supported: true}
	phoenix := func(p shinyPrediction) map[string]shinyPrediction {
		return map[string]shinyPrediction{"America/Phoenix": p}
	}
	s := &shinyScan{ctx: context.Background(), players: map[string]*shinyPlayer{}, rates: &shinyRates{
		variants: map[shinyVariant]shinyRate{
			// Never shiny, even where an override claims otherwise.
			{27, 0, 0}: {Pokemon: 27, Prediction: shinyPrediction{Supported: false}, Overrides: phoenix(shinyPrediction{Probability: 1, Supported: true}), Background: background},
			// Shiny-capable everywhere, with no chance in Phoenix.
			{28, 0, 0}: {Pokemon: 28, Prediction: shinyPrediction{Probability: 1, Supported: true}, Overrides: phoenix(shinyPrediction{Supported: false}), Background: background},
		},
		maximum:           map[int16]uint32{27: shinyScale, 28: shinyScale},
		backgroundMaximum: map[int16]uint32{27: background.cutoff(0), 28: background.cutoff(0)},
	}}
	for _, id := range players {
		input := make([]byte, 8+len(id))
		copy(input[8:], id)
		s.players[id] = &shinyPlayer{input: input}
	}
	seen := "wild"
	for _, tc := range []struct {
		pokemon           int16
		lat, lon          float64
		shiny, background []string
	}{
		{27, 33.45, -112.07, nil, []string{" Trainer-é "}},
		{27, 0, 0, nil, []string{" Trainer-é "}},
		{28, 33.45, -112.07, nil, []string{"0123456789abcdef"}},
		{28, 0, 0, players, []string{"0123456789abcdef"}},
	} {
		r := ApiPokemonResult{Id: "1", PokemonId: tc.pokemon, SeenType: &seen, Lat: tc.lat, Lon: tc.lon}
		s.annotate(&r, players)
		if !slices.Equal(r.ShinyFor, tc.shiny) || !slices.Equal(r.BackgroundFor, tc.background) {
			t.Errorf("%d at %v,%v: shiny %v background %v", tc.pokemon, tc.lat, tc.lon, r.ShinyFor, r.BackgroundFor)
		}
		lookup := PokemonLookup{PokemonId: tc.pokemon, RollEligibleCostume: 1}
		location := [2]float64{tc.lon, tc.lat}
		for _, id := range players {
			if got, want := s.match(1, &lookup, location, &ApiPokemonDnfFilter3{ShinyFor: []string{id}}), slices.Contains(tc.shiny, id); got != want {
				t.Errorf("%d at %v,%v: match shiny_for %q = %v", tc.pokemon, tc.lat, tc.lon, id, got)
			}
			if got, want := s.match(1, &lookup, location, &ApiPokemonDnfFilter3{BackgroundFor: []string{id}}), slices.Contains(tc.background, id); got != want {
				t.Errorf("%d at %v,%v: match background_for %q = %v", tc.pokemon, tc.lat, tc.lon, id, got)
			}
		}
	}
}
