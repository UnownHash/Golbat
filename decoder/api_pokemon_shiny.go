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
	"golbat/derive/encounter"
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

var shinyScanSlots = make(chan struct{}, 4)

// ScanPokemonWithShiny keeps the ordinary scan path intact. Only requests with
// shiny_for, background_for or annotate_for acquire a cancellable worker slot.
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
	select {
	case shinyScanSlots <- struct{}{}:
		defer func() { <-shinyScanSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	req.shiny = &shinyScan{ctx: ctx, rates: rates, players: players}
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

func (s *shinyScan) rate(pokemon, form, costume int) (shinyRate, bool) {
	rate, ok := s.rates.variants[shinyVariant{pokemon, form, costume}]
	return rate, ok
}

func (s *shinyScan) zoneAt(overrides map[string]shinyPrediction, lat, lon float64) string {
	if len(overrides) == 0 {
		return ""
	}
	// Resolved per spawn: matching bbox corners do not imply the whole box
	// shares a zone (e.g. enclaves), and only variants with overrides pay.
	return tz.SearchTimezone(lat, lon)
}

// shinyCutoff resolves the shiny threshold at a location. capable reports
// whether the variant can be shiny there at all; only then does the game take
// a draw for the shiny roll, which shifts every later draw.
func (s *shinyScan) shinyCutoff(rate shinyRate, ok bool, lat, lon float64) (cutoff uint32, capable bool) {
	if !ok {
		return defaultShinyCutoff, true
	}
	prediction := rate.Prediction
	if override, found := rate.Overrides[s.zoneAt(rate.Overrides, lat, lon)]; found {
		prediction = override
	}
	return prediction.cutoff(defaultShinyCutoff), prediction.Supported
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
	rate, ok := s.rate(int(lookup.PokemonId), int(lookup.Form), int(lookup.RollEligibleCostume)-1)
	if len(filter.ShinyFor) > 0 {
		lowest := uint32(shinyScale)
		for _, player := range filter.ShinyFor {
			lowest = min(lowest, first(s.players[player].rolls(id)))
		}
		if lowest >= max(defaultShinyCutoff, s.rates.maximum[lookup.PokemonId]) {
			return false
		}
		if cutoff, _ := s.shinyCutoff(rate, ok, lat, lon); lowest >= cutoff {
			return false
		}
	}
	if len(filter.BackgroundFor) > 0 {
		// Most species can never have a background: skip hashing entirely.
		if s.rates.backgroundMaximum[lookup.PokemonId] == 0 {
			return false
		}
		_, capable := s.shinyCutoff(rate, ok, lat, lon)
		lowest := uint32(shinyScale)
		for _, player := range filter.BackgroundFor {
			lowest = min(lowest, backgroundRoll(capable, s.players[player], id))
		}
		if lowest >= s.rates.backgroundMaximum[lookup.PokemonId] || lowest >= s.backgroundCutoff(rate, ok, lat, lon) {
			return false
		}
	}
	return true
}

// backgroundRoll is the draw after the shiny roll, which the game takes only
// when the variant can be shiny: the second draw if so, otherwise the first.
func backgroundRoll(shinyCapable bool, player *shinyPlayer, encounter uint64) uint32 {
	first, second := player.rolls(encounter)
	if shinyCapable {
		return second
	}
	return first
}

func first(a, _ uint32) uint32 { return a }

// annotate fills ShinyFor, BackgroundFor and SizeFor from the already-built
// result, so it needs no record lock. Ineligible results get none of them.
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
	shinyCutoff, shinyCapable := s.shinyCutoff(rate, ok, r.Lat, r.Lon)
	backgroundCutoff := s.backgroundCutoff(rate, ok, r.Lat, r.Lon)
	shared, sized := sharedSize(r, id, int16(form))
	for _, player := range players {
		rng := s.players[player].generator(id)
		if shinyCapable && rng.Next(24) < shinyCutoff {
			r.ShinyFor = append(r.ShinyFor, player)
		}
		// A background is on offer exactly when its cutoff is nonzero.
		if backgroundCutoff > 0 && rng.Next(24) < backgroundCutoff {
			r.BackgroundFor = append(r.BackgroundFor, player)
			// A rolled background (location card) takes two more draws.
			rng.Next(24)
			rng.Next(24)
		}
		if sized {
			height, weight := shared.personal(rng)
			if r.SizeFor == nil {
				r.SizeFor = make(map[string]ApiPersonalSize, len(players))
			}
			r.SizeFor[player] = ApiPersonalSize{Height: float32(height), Weight: float32(weight)}
		}
	}
}

// encounterSize is the player-independent part of a size: the class and
// weight noise from the encounter's own generator, and the masterfile data.
type encounterSize struct {
	class int32
	noise float64
	MasterFileSize
}

// sharedSize derives the class and weight noise the way the encounter's IVs
// are derived, checking the result against the IVs and level already known.
// ok is false without masterfile size data or when the derivation does not
// describe this pokemon.
func sharedSize(r *ApiPokemonResult, id uint64, form int16) (encounterSize, bool) {
	if r.AtkIv == nil || r.DefIv == nil || r.StaIv == nil || r.Level == nil {
		return encounterSize{}, false
	}
	size, ok := sizeFor(getMasterFileData(), r.PokemonId, form)
	if !ok {
		return encounterSize{}, false
	}
	boosted := r.Weather != nil && *r.Weather != 0
	draw := encounter.For(id, boosted, int32(form))
	if draw.Attack != int32(*r.AtkIv) || draw.Defense != int32(*r.DefIv) || draw.Stamina != int32(*r.StaIv) || draw.Level != int32(*r.Level) {
		return encounterSize{}, false
	}
	return encounterSize{class: draw.Size, noise: draw.WeightNoise, MasterFileSize: size}, true
}

// personal draws one player's height inside the class's bounds, continuing
// that player's generator after the shiny and background draws, and derives
// the weight from it. XXS and XXL take one more draw that puts the height in
// the far 20% of the band (below 0.05) or the near 80%.
func (e encounterSize) personal(rng *javarandom.Random) (height, weight float64) {
	b := e.Bounds
	var lo, hi float64
	switch e.class {
	case encounter.SizeXXS:
		lo, hi = b[0], b[1]
	case encounter.SizeXS:
		lo, hi = b[1], b[2]
	case encounter.SizeXL:
		lo, hi = b[3], b[4]
	case encounter.SizeXXL:
		lo, hi = b[4], b[5]
	default:
		lo, hi = b[2], b[3]
	}
	u := float64(rng.Float())
	if e.class == encounter.SizeXXS || e.class == encounter.SizeXXL {
		far := rng.Float() < 0.05
		split := lo + 0.2*(hi-lo) // XXS: far end is the bottom
		if e.class == encounter.SizeXXL {
			split = lo + 0.8*(hi-lo) // XXL: far end is the top
		}
		if far == (e.class == encounter.SizeXXS) {
			hi = split
		} else {
			lo = split
		}
	}
	height = lo + (hi-lo)*u
	h := height / e.Height
	scale := h * h
	if e.class == encounter.SizeXXL {
		scale = h
	}
	return height, e.Weight * (scale + 0.125*e.noise)
}
