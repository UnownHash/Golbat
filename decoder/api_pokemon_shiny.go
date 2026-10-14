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

// Cost is candidates x players x ID length, so both are bounded. 256 bytes
// covers every known player ID format with room to spare.
const maxPersonalPlayers = 50
const maxPersonalPlayerIDBytes = 256

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

// shinyCutoffs is a variant's rates in scan-ready form, built once per
// refresh: integer cutoffs, and only the overrides that differ from the base
// (nil when none do). A zero shiny cutoff means not shiny-capable, so such a
// variant keeps no shiny overrides.
type shinyCutoffs struct {
	shiny               uint32
	shinyOverrides      map[string]uint32
	background          uint32
	backgroundOverrides map[string]uint32
}

type shinyRates struct {
	variants          map[shinyVariant]shinyCutoffs
	unsupported       map[int16]bool // species without a released shiny
	maximum           map[int16]uint32
	backgroundMaximum map[int16]uint32
	zoned             bool // some variant has an effective override
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
		Rates       *[]shinyRate `json:"rates"`
		Unsupported []int        `json:"unsupportedPokemon"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&feed); err != nil {
		return err
	}
	// {} , null or an error object would otherwise replace good rates with none.
	if feed.Rates == nil {
		return fmt.Errorf("rates response has no rates list")
	}
	shinyRatesSnapshot.Store(newShinyRates(*feed.Rates, feed.Unsupported))
	return nil
}

// newShinyRates normalizes a feed so scans compare integers and resolve a
// timezone only where the answer depends on it.
func newShinyRates(rates []shinyRate, unsupported []int) *shinyRates {
	next := &shinyRates{variants: make(map[shinyVariant]shinyCutoffs, len(rates)), maximum: make(map[int16]uint32), backgroundMaximum: make(map[int16]uint32)}
	next.unsupported = make(map[int16]bool, len(unsupported))
	for _, p := range unsupported {
		next.unsupported[int16(p)] = true
	}
	for _, r := range rates {
		c := shinyCutoffs{shiny: r.Prediction.cutoff(defaultShinyCutoff)}
		if c.shiny > 0 {
			c.shinyOverrides = effectiveOverrides(r.Overrides, c.shiny, defaultShinyCutoff)
		}
		if r.Background != nil {
			c.background = r.Background.cutoff(0)
		}
		c.backgroundOverrides = effectiveOverrides(r.BackgroundOverrides, c.background, 0)
		next.zoned = next.zoned || c.shinyOverrides != nil || c.backgroundOverrides != nil
		next.variants[shinyVariant{r.Pokemon, r.Form, r.Costume}] = c
		// The per-species maxima are a cheap rejection before the exact variant.
		species := int16(r.Pokemon)
		shiny, background := c.shiny, c.background
		for _, cutoff := range c.shinyOverrides {
			shiny = max(shiny, cutoff)
		}
		for _, cutoff := range c.backgroundOverrides {
			background = max(background, cutoff)
		}
		next.maximum[species] = max(next.maximum[species], shiny)
		next.backgroundMaximum[species] = max(next.backgroundMaximum[species], background)
	}
	return next
}

// effectiveOverrides converts overrides to cutoffs and drops those equal to
// the base cutoff, comparing integers so near-equal probabilities collapse.
func effectiveOverrides(overrides map[string]shinyPrediction, base, fallback uint32) map[string]uint32 {
	var effective map[string]uint32
	for zone, p := range overrides {
		if cutoff := p.cutoff(fallback); cutoff != base {
			if effective == nil {
				effective = make(map[string]uint32)
			}
			effective[zone] = cutoff
		}
	}
	return effective
}

// shinyMaximum bounds the shiny cutoff of any variant of a species, counting
// unlisted variants: 1/512 unless the species is listed as never shiny.
func (r *shinyRates) shinyMaximum(pokemon int16) uint32 {
	if r.unsupported[pokemon] {
		return r.maximum[pokemon]
	}
	return max(r.maximum[pokemon], defaultShinyCutoff)
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

// shinyPlayer is used by one scan goroutine. It remembers only the last
// encounter's rolls, so a player listed in several clauses is hashed at most
// once per candidate, and only if a clause gets that far.
type shinyPlayer struct {
	input                 []byte
	last                  uint64
	lastValid             bool
	lastFirst, lastSecond uint32
}

// rolls returns the first two java.util.Random nextFloat draws of the
// player's generator for an encounter, kept as their 24-bit integers. The
// shiny roll is the first; the background is the next draw after it, so the
// first when the variant cannot be shiny (see backgroundRoll). Recomputing
// (one SHA-1 block) is cheaper than a cache.
func (p *shinyPlayer) rolls(encounter uint64) (first, second uint32) {
	if p.lastValid && p.last == encounter {
		return p.lastFirst, p.lastSecond
	}
	rng := p.generator(encounter)
	first, second = rng.Next(24), rng.Next(24)
	p.last, p.lastValid, p.lastFirst, p.lastSecond = encounter, true, first, second
	return first, second
}

// generator is the player's fresh generator for an encounter.
func (p *shinyPlayer) generator(encounter uint64) *javarandom.Random {
	binary.LittleEndian.PutUint64(p.input[:8], encounter)
	digest := sha1.Sum(p.input)
	return javarandom.New(binary.LittleEndian.Uint64(digest[:8]))
}

type shinyScan struct {
	ctx     context.Context
	rates   *shinyRates
	players map[string]*shinyPlayer
}

// ScanPokemonWithShiny keeps the ordinary scan path intact; requests without
// shiny_for, background_for or annotate_for take it unchanged.
func ScanPokemonWithShiny(ctx context.Context, req ApiPokemonScan3) (*ApiPokemonScanResultV3, error) {
	players := make(map[string]*shinyPlayer)
	// add registers players and returns the list without duplicates, so the
	// matching and annotation loops are bounded by distinct IDs too.
	add := func(ids []string) ([]string, error) {
		var unique []string
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if id == "" || len(id) > maxPersonalPlayerIDBytes {
				return nil, fmt.Errorf("%w: player IDs must be 1-%d bytes", ErrPersonalRequest, maxPersonalPlayerIDBytes)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			unique = append(unique, id)
			if players[id] == nil {
				if len(players) == maxPersonalPlayers {
					return nil, fmt.Errorf("%w: at most %d distinct player IDs per request", ErrPersonalRequest, maxPersonalPlayers)
				}
				input := make([]byte, 8+len(id))
				copy(input[8:], id)
				players[id] = &shinyPlayer{input: input}
			}
		}
		return unique, nil
	}
	// Copy the clauses before normalizing so the caller's request is untouched.
	req.DnfFilters = append([]ApiPokemonDnfFilter3(nil), req.DnfFilters...)
	var err error
	for i := range req.DnfFilters {
		f := &req.DnfFilters[i]
		if f.ShinyFor, err = add(f.ShinyFor); err != nil {
			return nil, err
		}
		if f.BackgroundFor, err = add(f.BackgroundFor); err != nil {
			return nil, err
		}
	}
	if req.AnnotateFor, err = add(req.AnnotateFor); err != nil {
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
	req.shiny = &shinyScan{ctx: ctx, rates: rates, players: players}
	result := GetPokemonInArea3Clean(req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.AnnotateFor) > 0 {
		for i := range result.Pokemon {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			req.shiny.annotate(&result.Pokemon[i], req.AnnotateFor)
		}
	}
	return result, nil
}

// rate returns a variant's cutoffs. Unknown costumes/forms default to 1/512
// shiny and no background, unless the species is listed as never shiny.
func (s *shinyScan) rate(pokemon, form, costume int) shinyCutoffs {
	if rate, ok := s.rates.variants[shinyVariant{pokemon, form, costume}]; ok {
		return rate
	}
	if s.rates.unsupported[int16(pokemon)] {
		return shinyCutoffs{}
	}
	return shinyCutoffs{shiny: defaultShinyCutoff}
}

func (s *shinyScan) zoneAt(overrides map[string]uint32, lat, lon float64) string {
	if !s.rates.zoned || len(overrides) == 0 {
		return ""
	}
	// Resolved per spawn: matching bbox corners do not imply the whole box
	// shares a zone (e.g. enclaves), and only variants with overrides pay.
	return tz.SearchTimezone(lat, lon)
}

// capable reports whether the variant has a shiny roll at all; only then
// does the game take a draw for it, which shifts every later draw. A shiny is
// released per species and form, not per location, so this reads the base
// cutoff only and never needs a timezone. A zero cutoff (probability 0, even
// when marked supported) means no shiny is available, so no draw.
func (c shinyCutoffs) capable() bool { return c.shiny > 0 }

// shinyCutoff resolves the shiny threshold at a location. Overrides change
// only the threshold; a variant that is not capable has none and stays 0.
func (s *shinyScan) shinyCutoff(rate shinyCutoffs, lat, lon float64) uint32 {
	if override, found := rate.shinyOverrides[s.zoneAt(rate.shinyOverrides, lat, lon)]; found {
		return override
	}
	return rate.shiny
}

func (s *shinyScan) backgroundCutoff(rate shinyCutoffs, lat, lon float64) uint32 {
	if override, found := rate.backgroundOverrides[s.zoneAt(rate.backgroundOverrides, lat, lon)]; found {
		return override
	}
	return rate.background
}

// matchClause applies a clause's personal conditions once its ordinary
// conditions have matched. Clauses without any pass straight through.
func (s *shinyScan) matchClause(id uint64, lookup *PokemonLookup, location [2]float64, filter *ApiPokemonDnfFilter3) (match, stop bool) {
	if len(filter.ShinyFor) == 0 && len(filter.BackgroundFor) == 0 {
		return true, false
	}
	if s.ctx.Err() != nil {
		return false, true
	}
	if lookup.RollEligibleCostume == 0 {
		return false, false
	}
	return s.match(id, lookup, location, filter), false
}

// match needs no record lock: eligibility, form and costume come from the
// lookup and the location from the spatial index. Each check first compares
// the lowest roll with the species maximum, so most candidates stop there.
func (s *shinyScan) match(id uint64, lookup *PokemonLookup, location [2]float64, filter *ApiPokemonDnfFilter3) bool {
	lon, lat := location[0], location[1]
	rate := s.rate(int(lookup.PokemonId), int(lookup.Form), int(lookup.RollEligibleCostume)-1)
	if len(filter.ShinyFor) > 0 {
		maximum := s.rates.shinyMaximum(lookup.PokemonId)
		if maximum == 0 {
			return false
		}
		lowest := uint32(shinyScale)
		for _, player := range filter.ShinyFor {
			lowest = min(lowest, first(s.players[player].rolls(id)))
		}
		if lowest >= maximum {
			return false
		}
		if lowest >= s.shinyCutoff(rate, lat, lon) {
			return false
		}
	}
	if len(filter.BackgroundFor) > 0 {
		// Most species can never have a background: skip hashing entirely.
		if s.rates.backgroundMaximum[lookup.PokemonId] == 0 {
			return false
		}
		capable := rate.capable()
		lowest := uint32(shinyScale)
		for _, player := range filter.BackgroundFor {
			lowest = min(lowest, backgroundRoll(capable, s.players[player], id))
		}
		if lowest >= s.rates.backgroundMaximum[lookup.PokemonId] || lowest >= s.backgroundCutoff(rate, lat, lon) {
			return false
		}
	}
	return true
}

// backgroundRoll is the draw after the shiny roll, which the game takes only
// when the variant can be shiny: the second draw if so, otherwise the first.
func backgroundRoll(capable bool, player *shinyPlayer, encounter uint64) uint32 {
	first, second := player.rolls(encounter)
	if capable {
		return second
	}
	return first
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
	rate := s.rate(int(r.PokemonId), form, int(personal)-1)
	capable := rate.capable()
	shinyCutoff := s.shinyCutoff(rate, r.Lat, r.Lon)
	backgroundCutoff := s.backgroundCutoff(rate, r.Lat, r.Lon)
	for _, player := range players {
		rng := s.players[player].generator(id)
		if capable && rng.Next(24) < shinyCutoff {
			r.ShinyFor = append(r.ShinyFor, player)
		}
		// A background is on offer exactly when its cutoff is nonzero.
		if backgroundCutoff > 0 && rng.Next(24) < backgroundCutoff {
			r.BackgroundFor = append(r.BackgroundFor, player)
		}
	}
}
