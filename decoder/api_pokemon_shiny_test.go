package decoder

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/guregu/null/v6"
	"golbat/config"
	"golbat/derive/encounter"
	"golbat/derive/javarandom"
	"golbat/tz"
	"math"
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
	s := &shinyScan{rates: &shinyRates{zoned: true}}
	if z := s.zoneAt(map[string]uint32{"America/Phoenix": 1}, 35.812, -110.195); z != "America/Phoenix" {
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
	s := &shinyScan{ctx: context.Background(), players: map[string]*shinyPlayer{}, rates: newShinyRates([]shinyRate{
		{Pokemon: 25, Prediction: shinyPrediction{Probability: 0.1, Supported: true}, Background: background},
		{Pokemon: 26, Prediction: shinyPrediction{Supported: false}, Background: background},
		{Pokemon: 29, Prediction: shinyPrediction{Probability: 0, Supported: true}, Background: background},
	}, nil, nil)}
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

// thirdDraw is the Ditto shiny roll computed from scratch, independently of
// shinyPlayer: the third Next(24) of the personal generator.
func thirdDraw(encounter uint64, player string) uint32 {
	input := make([]byte, 8+len(player))
	binary.LittleEndian.PutUint64(input, encounter)
	copy(input[8:], player)
	digest := sha1.Sum(input)
	rng := javarandom.New(binary.LittleEndian.Uint64(digest[:8]))
	rng.Next(24)
	rng.Next(24)
	return rng.Next(24)
}

func TestPersonalDittoShiny(t *testing.T) {
	// Pidgey disguises at rate 1 would be shiny and have a background for
	// everyone; a known Ditto must ignore that and the unsupportedPokemon
	// entry for 132, and use only the third draw at 1/64.
	players := []string{"0123456789abcdef", "Player-ID:ABC/123", " Trainer-é "}
	always := shinyPrediction{Probability: 1, Supported: true}
	s := &shinyScan{ctx: context.Background(), players: map[string]*shinyPlayer{}, rates: newShinyRates([]shinyRate{
		{Pokemon: 16, Prediction: always, Background: &always},
	}, []int{dittoPokemonId}, nil)}
	for _, id := range players {
		input := make([]byte, 8+len(id))
		copy(input[8:], id)
		s.players[id] = &shinyPlayer{input: input}
	}
	seen, disguise := "wild", uint16(16)
	annotate := func(encounter uint64) ApiPokemonResult {
		r := ApiPokemonResult{Id: fmt.Sprint(encounter), PokemonId: dittoPokemonId, IsDitto: true, DisplayPokemonId: &disguise, SeenType: &seen}
		s.annotate(&r, players)
		return r
	}
	ditto := &Pokemon{PokemonData: PokemonData{PokemonId: dittoPokemonId, IsDitto: true, DisplayPokemonId: null.ValueFrom(disguise), SeenType: SeenTypeFrom(SeenTypeCodeWild)}}
	lookup := PokemonLookup{PokemonId: dittoPokemonId, RollEligibleCostume: personalLookup(ditto)}
	if lookup.RollEligibleCostume == 0 {
		t.Fatal("known Ditto is not eligible")
	}
	// Pinned: 6 is a Ditto shiny for Player-ID:ABC/123 only, 26 for
	// Trainer-é only. 25 is not, though Trainer-é's first two draws are
	// below 1/64; 15 is not, though 0123456789abcdef's first is.
	pinned := map[uint64][]string{1: nil, 6: {"Player-ID:ABC/123"}, 15: nil, 25: nil, 26: {" Trainer-é "}}
	for encounter := uint64(1); encounter <= 1000; encounter++ {
		var want []string
		for _, id := range players {
			if thirdDraw(encounter, id) < (1<<24)/64 {
				want = append(want, id)
			}
		}
		if pin, ok := pinned[encounter]; ok && !slices.Equal(want, pin) {
			t.Fatalf("encounter %d: generator gives %v, pinned %v", encounter, want, pin)
		}
		r := annotate(encounter)
		if !slices.Equal(r.ShinyFor, want) || r.BackgroundFor != nil {
			t.Fatalf("encounter %d: shiny %v background %v, want shiny %v", encounter, r.ShinyFor, r.BackgroundFor, want)
		}
		for _, id := range players {
			if got := s.match(encounter, &lookup, [2]float64{0, 0}, &ApiPokemonDnfFilter3{ShinyFor: []string{id}}); got != slices.Contains(want, id) {
				t.Fatalf("encounter %d: match shiny_for %q = %v", encounter, id, got)
			}
			// No Ditto background has been observed: never matched.
			if s.match(encounter, &lookup, [2]float64{0, 0}, &ApiPokemonDnfFilter3{BackgroundFor: []string{id}}) {
				t.Fatalf("encounter %d: matched background_for %q", encounter, id)
			}
		}
		if got := s.match(encounter, &lookup, [2]float64{0, 0}, &ApiPokemonDnfFilter3{ShinyFor: players}); got != (len(want) > 0) {
			t.Fatalf("encounter %d: match any = %v", encounter, got)
		}
	}

	// Records not known to be Ditto stay ineligible.
	for name, p := range map[string]*Pokemon{
		"undisguised 132":               {PokemonData: PokemonData{PokemonId: dittoPokemonId, SeenType: SeenTypeFrom(SeenTypeCodeWild)}},
		"disguised, not known as Ditto": {PokemonData: PokemonData{PokemonId: 16, DisplayPokemonId: null.ValueFrom(uint16(19)), SeenType: SeenTypeFrom(SeenTypeCodeWild)}},
		"nearby Ditto":                  {PokemonData: PokemonData{PokemonId: dittoPokemonId, IsDitto: true, DisplayPokemonId: null.ValueFrom(disguise), SeenType: SeenTypeFrom(SeenTypeCodeNearbyStop)}},
	} {
		if got := personalLookup(p); got != 0 {
			t.Errorf("%s: eligible (%d)", name, got)
		}
	}
	unknown := ApiPokemonResult{Id: "6", PokemonId: dittoPokemonId, SeenType: &seen}
	if s.annotate(&unknown, players); unknown.ShinyFor != nil || unknown.BackgroundFor != nil {
		t.Errorf("undisguised 132 annotated: %v %v", unknown.ShinyFor, unknown.BackgroundFor)
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
	rate931, rate25 := s.rate(931, 2601, 0), s.rate(25, 0, 0)
	if cutoff, capable := s.shinyCutoff(rate931, 0, 0), rate931.capable(); capable || cutoff != 0 {
		t.Errorf("unsupported species: cutoff %d capable %v", cutoff, capable)
	}
	if cutoff, capable := s.shinyCutoff(rate25, 0, 0), rate25.capable(); !capable || cutoff != defaultShinyCutoff {
		t.Errorf("unlisted species: cutoff %d capable %v", cutoff, capable)
	}
	if m := s.rates.shinyMaximum(931); m != 0 {
		t.Errorf("unsupported species maximum %d", m)
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
	s := &shinyScan{ctx: context.Background(), players: map[string]*shinyPlayer{}, rates: newShinyRates([]shinyRate{
		// Never shiny, even where an override claims otherwise.
		{Pokemon: 27, Prediction: shinyPrediction{Supported: false}, Overrides: phoenix(shinyPrediction{Probability: 1, Supported: true}), Background: background},
		// Shiny-capable everywhere, with no chance in Phoenix.
		{Pokemon: 28, Prediction: shinyPrediction{Probability: 1, Supported: true}, Overrides: phoenix(shinyPrediction{Supported: false}), Background: background},
	}, nil, nil)}
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

func TestShinyRatesNormalize(t *testing.T) {
	phoenix := func(p shinyPrediction) map[string]shinyPrediction {
		return map[string]shinyPrediction{"America/Phoenix": p}
	}
	base := shinyPrediction{Probability: 1.0 / 512, Supported: true}
	background := shinyPrediction{Probability: 0.04, Supported: true}
	// As in the live feed: every override matches its base, here once only
	// after rounding to a cutoff. Nothing needs a timezone.
	rates := newShinyRates([]shinyRate{
		{Pokemon: 1, Prediction: base, Overrides: phoenix(shinyPrediction{Probability: 0.0019531, Supported: true})},
		{Pokemon: 2, Prediction: base, Overrides: phoenix(base),
			Background: &background, BackgroundOverrides: phoenix(background)},
		// Not capable: overrides cannot change that, so none are kept.
		{Pokemon: 3, Prediction: shinyPrediction{Supported: true}, Overrides: phoenix(base)},
	}, nil, nil)
	if rates.zoned {
		t.Error("snapshot with only no-op overrides is zoned")
	}
	for v, c := range rates.variants {
		if c.shinyOverrides != nil || c.backgroundOverrides != nil {
			t.Errorf("%v kept overrides %+v", v, c)
		}
	}
	if c := rates.variants[shinyVariant{2, 0, 0}]; c.shiny != defaultShinyCutoff || c.background != background.cutoff(0) {
		t.Errorf("cutoffs %+v", c)
	}
	if m := rates.maximum[3]; m != 0 {
		t.Errorf("not-capable species maximum %d", m)
	}

	// A real override survives, marks the snapshot, and raises the maximum.
	rates = newShinyRates([]shinyRate{
		{Pokemon: 1, Prediction: base, Overrides: map[string]shinyPrediction{"America/Phoenix": {Probability: 0.25, Supported: true}, "America/Denver": base}},
		{Pokemon: 2, Prediction: base, BackgroundOverrides: phoenix(shinyPrediction{Probability: 0.1, Supported: true})},
	}, nil, nil)
	if !rates.zoned {
		t.Error("snapshot with effective overrides is not zoned")
	}
	if c := rates.variants[shinyVariant{1, 0, 0}]; len(c.shinyOverrides) != 1 || c.shinyOverrides["America/Phoenix"] != shinyScale/4 {
		t.Errorf("shiny overrides %v", c.shinyOverrides)
	}
	if rates.maximum[1] != shinyScale/4 || rates.backgroundMaximum[2] == 0 {
		t.Errorf("maxima %v %v", rates.maximum, rates.backgroundMaximum)
	}
	s := &shinyScan{rates: rates}
	if got := s.shinyCutoff(rates.variants[shinyVariant{1, 0, 0}], 0, 0); got != defaultShinyCutoff {
		t.Errorf("default cutoff %d", got)
	}
	if tz.SearchTimezone(33.45, -112.07) != "America/Phoenix" {
		t.Skip("timezone data changed")
	}
	if got := s.shinyCutoff(rates.variants[shinyVariant{1, 0, 0}], 33.45, -112.07); got != shinyScale/4 {
		t.Errorf("Phoenix cutoff %d", got)
	}
}

func TestShinyRatesFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fallback string
		want     uint32
	}{
		{"omitted", ``, defaultShinyCutoff},
		{"published", `,"fallback":{"probability":0.00390625,"assumed":true,"supported":true}`, shinyScale / 256},
		{"zero", `,"fallback":{"probability":0,"supported":true}`, 0},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"rates":[{"pokemon":1,"form":0,"costume":0,"prediction":{"probability":2,"supported":true}}],"unsupportedPokemon":[931]` + tc.fallback + `}`))
		}))
		old := config.Config.ShinyRates
		config.Config.ShinyRates = config.ShinyRates{URL: server.URL}
		err := refreshShinyRates(context.Background())
		config.Config.ShinyRates = old
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		s := &shinyScan{rates: shinyRatesSnapshot.Load()}
		shinyRatesSnapshot.Store(nil)
		// Unlisted variants, and listed ones with an invalid probability,
		// take the fallback; a zero fallback leaves them not capable.
		for _, rate := range []shinyCutoffs{s.rate(25, 0, 0), s.rate(1, 0, 0)} {
			if rate.shiny != tc.want || rate.capable() != (tc.want > 0) {
				t.Errorf("%s: cutoffs %+v", tc.name, rate)
			}
		}
		if m := s.rates.shinyMaximum(25); m != tc.want {
			t.Errorf("%s: maximum %d", tc.name, m)
		}
		if rate := s.rate(931, 0, 0); rate.capable() {
			t.Errorf("%s: unsupported species capable", tc.name)
		}
	}
}

func TestShinyRatesMaxBytes(t *testing.T) {
	body := `{"rates":[{"pokemon":25,"form":0,"costume":0,"prediction":{"probability":0.1,"supported":true}}]}`
	for _, tc := range []struct {
		name     string
		limit    int64
		chunked  bool
		tooLarge bool
	}{
		{"fits exactly", int64(len(body)), false, false},
		{"over by one", int64(len(body)) - 1, false, true},
		{"over by one, no length", int64(len(body)) - 1, true, true},
		{"unset uses the default", 0, false, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.chunked {
				w.(http.Flusher).Flush()
			}
			_, _ = w.Write([]byte(body))
		}))
		old := config.Config.ShinyRates
		config.Config.ShinyRates = config.ShinyRates{URL: server.URL, MaxBytes: tc.limit}
		good := &shinyRates{}
		shinyRatesSnapshot.Store(good)
		err := refreshShinyRates(context.Background())
		if got := errors.Is(err, errShinyRatesTooLarge); got != tc.tooLarge {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if tc.tooLarge && shinyRatesSnapshot.Load() != good {
			t.Errorf("%s: replaced the last good snapshot", tc.name)
		}
		config.Config.ShinyRates = old
		shinyRatesSnapshot.Store(nil)
		server.Close()
	}
}

func TestPersonalRollSeenTypes(t *testing.T) {
	rolled := map[SeenTypeCode]bool{
		SeenTypeCodeWild:                  true,
		SeenTypeCodeEncounter:             true,
		SeenTypeCodeTappableEncounter:     true,
		SeenTypeCodeTappableLureEncounter: true,
	}
	codes := []SeenTypeCode{SeenTypeCodeUnknown}
	for code := SeenTypeCodeUnset; code <= SeenTypeCodeTappableLureEncounter; code++ {
		codes = append(codes, code)
	}
	for _, code := range codes {
		p := &Pokemon{PokemonData: PokemonData{PokemonId: 94, Costume: null.ValueFrom[uint8](3), SeenType: SeenTypeFrom(code)}}
		want := uint8(0)
		if rolled[code] {
			want = 4
		}
		if got := personalLookup(p); got != want {
			t.Errorf("personalLookup with seen type %d = %d, want %d", code, got, want)
		}
	}
}

func TestPersonalSizeVectors(t *testing.T) {
	// Real wild encounters from roundaboutluke (#423): the player's 32-bit
	// draw at the height index and the next one, the raw second Gaussian
	// deviate, and the height (m) and weight (kg) the game showed.
	for _, v := range []struct {
		class          int32
		draw, next     uint32
		noise          float64
		height, weight float64
		size           MasterFileSize
	}{
		{3, 3890164944, 917775855, 1.516281, 0.8420, 20.456, MasterFileSize{0.7, 12.5, sizeBounds{0.343, 0.35, 0.525, 0.875, 1.05, 1.225}}},
		{4, 2539945545, 1892707713, 0.814014, 0.4194, 13.362, MasterFileSize{0.3, 6.5, sizeBounds{0.147, 0.15, 0.225, 0.375, 0.45, 0.525}}},
		{1, 4081782436, 1664957500, 1.13957, 0.6495, 21.367, MasterFileSize{1.3, 54.5, sizeBounds{0.637, 0.65, 0.975, 1.625, 1.95, 2.015}}},
		{1, 4165317312, 74602773, -0.534469, 0.1476, 0.543, MasterFileSize{0.3, 3.1, sizeBounds{0.147, 0.15, 0.225, 0.375, 0.45, 0.525}}},
		{5, 2455997842, 1299456597, -0.286937, 0.8072, 44.198, MasterFileSize{0.5, 28, sizeBounds{0.245, 0.25, 0.375, 0.625, 0.75, 0.875}}},
		{5, 2975473780, 191867137, -0.137099, 2.1657, 78.019, MasterFileSize{1.4, 51, sizeBounds{0.686, 0.7, 1.05, 1.75, 2.1, 2.17}}},
	} {
		// nextFloat reads the top 24 of the 32 bits.
		float := func(bits uint32) float64 { return float64(bits>>8) / (1 << 24) }
		e := encounterSize{class: v.class, noise: v.noise, MasterFileSize: v.size}
		height, weight := e.at(float(v.draw), func() float64 { return float(v.next) })
		if math.Abs(height-v.height) > 5e-5 || math.Abs(weight-v.weight) > 5e-4 {
			t.Errorf("class %d draw %d: got %.4f m %.3f kg, want %.4f m %.3f kg", v.class, v.draw, height, weight, v.height, v.weight)
		}
	}
}

func TestPersonalSize(t *testing.T) {
	bounds := sizeBounds{0.343, 0.35, 0.525, 0.875, 1.05, 1.225} // Bulbasaur
	base := MasterFileSize{Height: 0.7, Weight: 6.9, Bounds: bounds}
	bands := map[int32][2]float64{
		1: {bounds[0], bounds[1]}, 2: {bounds[1], bounds[2]}, 3: {bounds[2], bounds[3]},
		4: {bounds[3], bounds[4]}, 5: {bounds[4], bounds[5]},
	}
	for class, band := range bands {
		far := 0
		const n = 20000
		for seed := uint64(0); seed < n; seed++ {
			e := encounterSize{class: class, MasterFileSize: base}
			height, weight := e.personal(javarandom.New(seed))
			if height < band[0] || height > band[1] {
				t.Fatalf("class %d: height %v outside %v", class, height, band)
			}
			h := height / base.Height
			want := base.Weight * h * h
			if class == 5 {
				want = base.Weight * h
			}
			if math.Abs(weight-want) > 1e-9 {
				t.Fatalf("class %d: weight %v want %v without noise", class, weight, want)
			}
			split := band[0] + 0.2*(band[1]-band[0])
			if class == 1 && height < split || class == 5 && height > band[0]+0.8*(band[1]-band[0]) {
				far++
			}
		}
		// XXS/XXL land in the far 20% of the band 5% of the time.
		if class == 1 || class == 5 {
			if rate := float64(far) / n; rate < 0.04 || rate > 0.06 {
				t.Errorf("class %d: far-band rate %v, want about 0.05", class, rate)
			}
		}
	}
	// Noise moves weight linearly (base weight x 0.125 per unit), clamped to
	// +/-4, and is dropped when it would make the ratio nonpositive.
	_, quiet := encounterSize{class: 3, MasterFileSize: base}.personal(javarandom.New(7))
	for noise, want := range map[float64]float64{2: 0.25, 9: 0.5} {
		if _, noisy := (encounterSize{class: 3, noise: noise, MasterFileSize: base}).personal(javarandom.New(7)); math.Abs(noisy-quiet-base.Weight*want) > 1e-9 {
			t.Errorf("noise %v: %v vs %v", noise, noisy, quiet)
		}
	}
	if _, w := (encounterSize{class: 1, noise: -4, MasterFileSize: base}).personal(javarandom.New(7)); w <= 0 {
		t.Errorf("nonpositive weight %v", w)
	}
}

func TestPersonalSizeAnnotation(t *testing.T) {
	old := getMasterFileData()
	t.Cleanup(func() {
		masterFileMu.Lock()
		masterFileData = old
		masterFileMu.Unlock()
	})
	masterFileMu.Lock()
	masterFileData = MasterFileData{Initialized: true, Pokemon: map[int]MasterFilePokemon{
		1:              {Size: MasterFileSize{Height: 0.7, Weight: 6.9, Bounds: sizeBounds{0.343, 0.35, 0.525, 0.875, 1.05, 1.225}}},
		dittoPokemonId: {Size: MasterFileSize{Height: 0.3, Weight: 4, Bounds: sizeBounds{0.147, 0.15, 0.225, 0.375, 0.45, 0.525}}},
	}}
	masterFileMu.Unlock()

	const encounterID = 8429844873227918432
	draw := encounter.For(encounterID, false, 0)
	atk, def, sta, level, weather := uint8(draw.Attack), uint8(draw.Defense), uint8(draw.Stamina), uint8(draw.Level), uint8(0)
	seen := "wild"
	player := "0123456789abcdef"
	input := make([]byte, 8+len(player))
	copy(input[8:], player)
	s := &shinyScan{ctx: context.Background(), players: map[string]*shinyPlayer{player: {input: input}},
		rates: &shinyRates{variants: map[shinyVariant]shinyCutoffs{}, fallback: defaultShinyCutoff}}
	r := ApiPokemonResult{Id: fmt.Sprint(uint64(encounterID)), PokemonId: 1, SeenType: &seen, AtkIv: &atk, DefIv: &def, StaIv: &sta, Level: &level, Weather: &weather}
	s.annotate(&r, []string{player})
	size, ok := r.SizeFor[player]
	if !ok {
		t.Fatalf("no size: %+v", r.SizeFor)
	}
	// Unknown variant: 1/512 shiny draw, no background, then the height.
	rng := s.players[player].generator(encounterID)
	rng.Next(24)
	height, weight := encounterSize{class: draw.Size, noise: draw.WeightNoise, MasterFileSize: masterFileData.Pokemon[1].Size}.personal(rng)
	if size.Height != float32(height) || size.Weight != float32(weight) {
		t.Fatalf("size %+v, want %v/%v", size, height, weight)
	}
	// Values that don't match the derivation get no size.
	r = ApiPokemonResult{Id: r.Id, PokemonId: 1, SeenType: &seen, AtkIv: &def, DefIv: &atk, StaIv: &sta, Level: &level, Weather: &weather}
	if atk != def {
		s.annotate(&r, []string{player})
		if r.SizeFor != nil {
			t.Fatalf("mismatched derivation still sized: %+v", r.SizeFor)
		}
	}
	// A known Ditto's size draws follow its own draws, which aren't
	// modelled: no size even with masterfile data.
	disguise := uint16(1)
	r = ApiPokemonResult{Id: r.Id, PokemonId: dittoPokemonId, IsDitto: true, DisplayPokemonId: &disguise, SeenType: &seen, AtkIv: &atk, DefIv: &def, StaIv: &sta, Level: &level, Weather: &weather}
	if s.annotate(&r, []string{player}); r.SizeFor != nil {
		t.Fatalf("known Ditto sized: %+v", r.SizeFor)
	}
}

func TestMasterFileSizeSettings(t *testing.T) {
	for _, raw := range []string{
		`{"xxsLowerBound":0.343,"xsLowerBound":0.35,"mLowerBound":0.525,"mUpperBound":0.875,"xlUpperBound":1.05,"xxlUpperBound":1.225,"xxsScaleMultiplier":0.67}`,
		`[{"name":"xxsLowerBound","value":0.343},{"name":"xsLowerBound","value":0.35},{"name":"mLowerBound","value":0.525},{"name":"mUpperBound","value":0.875},{"name":"xlUpperBound","value":1.05},{"name":"xxlUpperBound","value":1.225},{"name":"disablePokedexRecordDisplayForForms","value":true}]`,
		`{"xxs_lower_bound":0.343,"xs_lower_bound":0.35,"m_lower_bound":0.525,"m_upper_bound":0.875,"xl_upper_bound":1.05,"xxl_upper_bound":1.225}`,
	} {
		var b sizeBounds
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			t.Fatal(err)
		}
		if b != (sizeBounds{0.343, 0.35, 0.525, 0.875, 1.05, 1.225}) || !b.valid() {
			t.Fatalf("%s -> %v", raw, b)
		}
	}
	data := MasterFileData{Pokemon: map[int]MasterFilePokemon{710: {
		Size:  MasterFileSize{Height: 0.4, Weight: 5, Bounds: sizeBounds{0.3, 0.3, 0.4, 0.4, 0.4, 0.4}},
		Forms: map[int]MasterFileForm{2644: {Size: MasterFileSize{Height: 0.8, Weight: 15, Bounds: sizeBounds{0.6, 0.6, 0.6, 0.6, 0.6, 0.8}}}, 2641: {}},
	}}}
	if size, ok := sizeFor(data, 710, 2644); !ok || size.Height != 0.8 || size.Bounds[5] != 0.8 {
		t.Fatalf("form override: %+v %v", size, ok)
	}
	if size, ok := sizeFor(data, 710, 2641); !ok || size.Height != 0.4 {
		t.Fatalf("form fallback: %+v %v", size, ok)
	}
	if _, ok := sizeFor(MasterFileData{Pokemon: map[int]MasterFilePokemon{1: {}}}, 1, 0); ok {
		t.Fatal("missing size data reported ok")
	}
}
