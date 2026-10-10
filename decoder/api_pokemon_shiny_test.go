package decoder

import (
	"context"
	"crypto/sha256"
	"github.com/guregu/null/v6"
	"golbat/config"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPersonalShinyScan(t *testing.T) {
	// Pin the independently verified game calculation, including opaque IDs.
	for _, tc := range []struct {
		id        string
		encounter uint64
		want      float64
	}{
		{"0123456789abcdef", 1, 0.22137850522994995},
		{"Player-ID:ABC/123", 1, 0.3684830665588379},
		{" Trainer-é ", 1, 0.03772324323654175},
	} {
		input := make([]byte, 8+len(tc.id))
		copy(input[8:], tc.id)
		p := shinyPlayer{key: sha256.Sum256([]byte(tc.id)), input: input}
		for i := 0; i < 2; i++ {
			if got := float64(p.roll(tc.encounter)) / shinyScale; got != tc.want {
				t.Fatalf("roll=%v want=%v", got, tc.want)
			}
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rates" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("incorrect Shinx request")
		}
		_, _ = w.Write([]byte(`{"rates":[{"pokemon":25,"form":0,"costume":0,"prediction":{"probability":0.1,"supported":true},"overrides":{"America/Phoenix":{"probability":0.05,"supported":true}}}]}`))
	}))
	defer server.Close()
	oldConfig := config.Config.Shinx
	oldLimit := config.Config.Tuning.MaxPokemonResults
	config.Config.Shinx = config.Shinx{URL: server.URL, Token: "test"}
	config.Config.Tuning.MaxPokemonResults = 100
	t.Cleanup(func() {
		config.Config.Shinx = oldConfig
		config.Config.Tuning.MaxPokemonResults = oldLimit
		shinxRateCache.Lock()
		shinxRateCache.rates = nil
		shinxRateCache.expires = time.Time{}
		shinxRateCache.Unlock()
	})
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
}
