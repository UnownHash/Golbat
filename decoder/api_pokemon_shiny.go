package decoder

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/derive/javarandom"
	"golbat/tz"
)

const shinyScale = 1 << 24
const defaultShinyCutoff = shinyScale / 512

// Cost is candidates x players, so the number of distinct IDs is bounded.
const maxPersonalPlayers = 50

// ErrPersonalRequest marks a malformed personal shiny/background request.
var ErrPersonalRequest = errors.New("invalid personal request")

type shinyPrediction struct {
	Probability float64 `json:"probability"`
	Supported   bool    `json:"supported"`
}

// cutoff converts a probability to a threshold on a 24-bit roll. Invalid
// probabilities use fallback: 1/512 for shiny, never for backgrounds.
func (p shinyPrediction) cutoff(fallback uint32) uint32 {
	if !p.Supported {
		return 0
	}
	if math.IsNaN(p.Probability) || p.Probability < 0 || p.Probability > 1 {
		return fallback
	}
	return uint32(math.Ceil(p.Probability * shinyScale))
}

type shinyVariant struct{ Pokemon, Form, Costume int }
type shinyRate struct {
	Pokemon             int                        `json:"pokemon"`
	Form                int                        `json:"form"`
	Costume             int                        `json:"costume"`
	Prediction          shinyPrediction            `json:"prediction"`
	Overrides           map[string]shinyPrediction `json:"overrides"`
	Background          *shinyPrediction           `json:"background"`
	BackgroundOverrides map[string]shinyPrediction `json:"background_overrides"`
}
type shinyRates struct {
	variants          map[shinyVariant]shinyRate
	maximum           map[int16]uint32
	backgroundMaximum map[int16]uint32
}

// Scans read the current snapshot without locking; only the refresher swaps it.
var shinyRatesSnapshot atomic.Pointer[shinyRates]

// StartShinyRates refreshes rates in the background every five minutes, or
// 30 seconds after a failure. A failed refresh keeps the last snapshot.
func StartShinyRates(ctx context.Context) {
	if config.Config.ShinyRates.URL == "" {
		return
	}
	go func() {
		for {
			wait := 5 * time.Minute
			if err := refreshShinyRates(ctx); err != nil {
				wait = 30 * time.Second
				if shinyRatesSnapshot.Load() == nil {
					log.Warnf("Shiny rates have not loaded yet; personal scans are unavailable: %v", err)
				} else {
					log.Warnf("Shiny rate refresh failed; retaining cached rates: %v", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

func refreshShinyRates(ctx context.Context) error {
	c := config.Config.ShinyRates
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+"/v1/rates?compact=1", nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	var feed struct {
		Rates []shinyRate `json:"rates"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&feed); err != nil {
		return err
	}
	next := &shinyRates{variants: make(map[shinyVariant]shinyRate), maximum: make(map[int16]uint32), backgroundMaximum: make(map[int16]uint32)}
	for _, r := range feed.Rates {
		next.variants[shinyVariant{r.Pokemon, r.Form, r.Costume}] = r
		// Unknown costumes/forms still default to 1/512 shiny and no background.
		// The per-species maxima are a cheap rejection before the exact variant.
		bound := max(defaultShinyCutoff, r.Prediction.cutoff(defaultShinyCutoff))
		for _, p := range r.Overrides {
			bound = max(bound, p.cutoff(defaultShinyCutoff))
		}
		next.maximum[int16(r.Pokemon)] = max(next.maximum[int16(r.Pokemon)], bound)
		background := uint32(0)
		if r.Background != nil {
			background = r.Background.cutoff(0)
		}
		for _, p := range r.BackgroundOverrides {
			background = max(background, p.cutoff(0))
		}
		next.backgroundMaximum[int16(r.Pokemon)] = max(next.backgroundMaximum[int16(r.Pokemon)], background)
	}
	shinyRatesSnapshot.Store(next)
	return nil
}

// personalLookup packs roll eligibility and costume into PokemonLookup's
// spare byte: 0 means never personally shiny or background-eligible
// (Ditto, disguised, or seen only as nearby), otherwise costume+1.
func personalLookup(pokemon *Pokemon) uint8 {
	costume := pokemon.Costume.ValueOrZero()
	if pokemon.IsDitto || pokemon.PokemonId == 132 || costume == math.MaxUint8 ||
		pokemon.DisplayPokemonId.ValueOrZero() != 0 && int16(pokemon.DisplayPokemonId.ValueOrZero()) != pokemon.PokemonId ||
		pokemon.SeenType != SeenTypeFrom(SeenTypeCodeWild) && pokemon.SeenType != SeenTypeFrom(SeenTypeCodeEncounter) {
		return 0
	}
	return uint8(costume) + 1
}

type shinyPlayer struct {
	input []byte
}

// rolls returns the shiny roll and the next value from the same generator,
// which decides the background: two java.util.Random nextFloat draws, kept as
// their 24-bit integers. Recomputing (one SHA-1 block) is cheaper than a cache.
func (p *shinyPlayer) rolls(encounter uint64) (shiny, background uint32) {
	binary.LittleEndian.PutUint64(p.input[:8], encounter)
	digest := sha1.Sum(p.input)
	rng := javarandom.New(binary.LittleEndian.Uint64(digest[:8]))
	return rng.Next(24), rng.Next(24)
}

type shinyScan struct {
	ctx     context.Context
	rates   *shinyRates
	players map[string]*shinyPlayer
	// zone is the timezone of all four bbox corners, or "" if they differ.
	zone string
}

var shinyScanSlots = make(chan struct{}, 4)

// ScanPokemonWithShiny keeps the ordinary scan path intact. Only requests with
// shiny_for, background_for or annotate_for acquire a cancellable worker slot.
func ScanPokemonWithShiny(ctx context.Context, req ApiPokemonScan3) (*ApiPokemonScanResultV3, error) {
	players := make(map[string]*shinyPlayer)
	add := func(ids []string) error {
		for _, id := range ids {
			if id == "" {
				return fmt.Errorf("%w: empty player ID", ErrPersonalRequest)
			}
			if players[id] == nil {
				if len(players) == maxPersonalPlayers {
					return fmt.Errorf("%w: at most %d distinct player IDs per request", ErrPersonalRequest, maxPersonalPlayers)
				}
				input := make([]byte, 8+len(id))
				copy(input[8:], id)
				players[id] = &shinyPlayer{input: input}
			}
		}
		return nil
	}
	for _, f := range req.DnfFilters {
		if err := add(f.ShinyFor); err != nil {
			return nil, err
		}
		if err := add(f.BackgroundFor); err != nil {
			return nil, err
		}
	}
	if err := add(req.AnnotateFor); err != nil {
		return nil, err
	}
	if len(players) == 0 {
		return GetPokemonInArea3Clean(req), nil
	}
	if config.Config.ShinyRates.URL == "" {
		return nil, fmt.Errorf("shiny rates are not configured")
	}
	rates := shinyRatesSnapshot.Load()
	if rates == nil {
		return nil, fmt.Errorf("shiny rates have not loaded yet")
	}
	select {
	case shinyScanSlots <- struct{}{}:
		defer func() { <-shinyScanSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	req.shiny = &shinyScan{ctx: ctx, rates: rates, players: players, zone: bboxZone(req.Min, req.Max)}
	result := GetPokemonInArea3Clean(req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.AnnotateFor) > 0 {
		for i := range result.Pokemon {
			req.shiny.annotate(&result.Pokemon[i], req.AnnotateFor)
		}
	}
	return result, nil
}

// bboxZone resolves the timezone once per scan. Corners that agree are taken
// to cover the box; otherwise zones are looked up per pokemon, and only for
// variants that have overrides.
func bboxZone(lo, hi ApiLatLon) string {
	zone := tz.SearchTimezone(lo.Lat, lo.Lon)
	for _, c := range [][2]float64{{lo.Lat, hi.Lon}, {hi.Lat, lo.Lon}, {hi.Lat, hi.Lon}} {
		if tz.SearchTimezone(c[0], c[1]) != zone {
			return ""
		}
	}
	return zone
}

func (s *shinyScan) rate(pokemon, form, costume int) (shinyRate, bool) {
	rate, ok := s.rates.variants[shinyVariant{pokemon, form, costume}]
	return rate, ok
}

func (s *shinyScan) zoneAt(overrides map[string]shinyPrediction, lat, lon float64) string {
	if len(overrides) == 0 {
		return ""
	}
	if s.zone != "" {
		return s.zone
	}
	return tz.SearchTimezone(lat, lon)
}

func (s *shinyScan) shinyCutoff(rate shinyRate, ok bool, lat, lon float64) uint32 {
	if !ok {
		return defaultShinyCutoff
	}
	if override, found := rate.Overrides[s.zoneAt(rate.Overrides, lat, lon)]; found {
		return override.cutoff(defaultShinyCutoff)
	}
	return rate.Prediction.cutoff(defaultShinyCutoff)
}

func (s *shinyScan) backgroundCutoff(rate shinyRate, ok bool, lat, lon float64) uint32 {
	if !ok {
		return 0
	}
	if override, found := rate.BackgroundOverrides[s.zoneAt(rate.BackgroundOverrides, lat, lon)]; found {
		return override.cutoff(0)
	}
	if rate.Background == nil {
		return 0
	}
	return rate.Background.cutoff(0)
}

// matchAny is the second pass over personal clauses for candidates the
// normal v3 selection did not match.
func (s *shinyScan) matchAny(id uint64, lookup *PokemonLookup, pvp *PokemonPvpLookup, location [2]float64, clauses []ApiPokemonDnfFilter3) (bool, bool) {
	if s.ctx.Err() != nil {
		return false, true
	}
	if lookup.Personal == 0 {
		return false, false
	}
	for i := range clauses {
		if pokemonClauseApplies(&clauses[i], lookup) && isPokemonDnfMatch3(lookup, pvp, &clauses[i]) && s.match(id, lookup, location, &clauses[i]) {
			return true, false
		}
	}
	return false, false
}

// match needs no record lock: eligibility, form and costume come from the
// lookup and the location from the spatial index. Each check first compares
// the lowest roll with the species maximum, so most candidates stop there.
func (s *shinyScan) match(id uint64, lookup *PokemonLookup, location [2]float64, filter *ApiPokemonDnfFilter3) bool {
	lon, lat := location[0], location[1]
	rate, ok := s.rate(int(lookup.PokemonId), int(lookup.Form), int(lookup.Personal)-1)
	if len(filter.ShinyFor) > 0 {
		lowest := uint32(shinyScale)
		for _, player := range filter.ShinyFor {
			lowest = min(lowest, first(s.players[player].rolls(id)))
		}
		if lowest >= max(defaultShinyCutoff, s.rates.maximum[lookup.PokemonId]) || lowest >= s.shinyCutoff(rate, ok, lat, lon) {
			return false
		}
	}
	if len(filter.BackgroundFor) > 0 {
		lowest := uint32(shinyScale)
		for _, player := range filter.BackgroundFor {
			_, background := s.players[player].rolls(id)
			lowest = min(lowest, background)
		}
		if lowest >= s.rates.backgroundMaximum[lookup.PokemonId] || lowest >= s.backgroundCutoff(rate, ok, lat, lon) {
			return false
		}
	}
	return true
}

func first(a, _ uint32) uint32 { return a }

// annotate fills ShinyFor and BackgroundFor from the already-built result, so
// it needs no record lock. Ineligible results get neither.
func (s *shinyScan) annotate(r *ApiPokemonResult, players []string) {
	id, err := strconv.ParseUint(r.Id, 10, 64)
	if err != nil {
		return
	}
	pokemon := Pokemon{}
	pokemon.PokemonId = r.PokemonId
	pokemon.IsDitto = r.IsDitto
	if r.Costume != nil {
		pokemon.Costume.SetValid(*r.Costume)
	}
	if r.DisplayPokemonId != nil {
		pokemon.DisplayPokemonId.SetValid(*r.DisplayPokemonId)
	}
	if r.SeenType != nil {
		pokemon.SeenType, _ = ParseSeenType(*r.SeenType)
	}
	personal := personalLookup(&pokemon)
	if personal == 0 {
		return
	}
	form := 0
	if r.Form != nil {
		form = int(*r.Form)
	}
	rate, ok := s.rate(int(r.PokemonId), form, int(personal)-1)
	shinyCutoff, backgroundCutoff := s.shinyCutoff(rate, ok, r.Lat, r.Lon), s.backgroundCutoff(rate, ok, r.Lat, r.Lon)
	for _, player := range players {
		shiny, background := s.players[player].rolls(id)
		if shiny < shinyCutoff {
			r.ShinyFor = append(r.ShinyFor, player)
		}
		if background < backgroundCutoff {
			r.BackgroundFor = append(r.BackgroundFor, player)
		}
	}
}
