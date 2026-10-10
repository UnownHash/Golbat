package decoder

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/derive/javarandom"
	"golbat/tz"
)

const shinyScale = 1 << 24
const defaultShinyCutoff = shinyScale / 512

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

func newShinyRates() *shinyRates {
	return &shinyRates{variants: map[shinyVariant]shinyRate{}, maximum: map[int16]uint32{}, backgroundMaximum: map[int16]uint32{}}
}

var shinyRateCache struct {
	sync.Mutex
	rates   *shinyRates
	expires time.Time
}

// Fetch once per five minutes, shared by scans. A temporary outage retains the
// last snapshot; species without a rate use 1/512.
func loadShinyRates(ctx context.Context) *shinyRates {
	shinyRateCache.Lock()
	defer shinyRateCache.Unlock()
	if shinyRateCache.rates == nil {
		shinyRateCache.rates = newShinyRates()
	}
	if time.Now().Before(shinyRateCache.expires) {
		return shinyRateCache.rates
	}
	shinyRateCache.expires = time.Now().Add(30 * time.Second)
	c := config.Config.ShinyRates
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+"/v1/rates?compact=1", nil)
	if err != nil {
		return shinyRateCache.rates
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		log.Warn("Shiny rate refresh failed; retaining cached rates")
		return shinyRateCache.rates
	}
	defer res.Body.Close()
	var feed struct {
		Rates []shinyRate `json:"rates"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&feed) != nil {
		log.Warn("Shiny rate refresh failed; retaining cached rates")
		return shinyRateCache.rates
	}
	next := newShinyRates()
	for _, r := range feed.Rates {
		next.variants[shinyVariant{r.Pokemon, r.Form, r.Costume}] = r
		// Unknown costumes/forms still default to 1/512 shiny and no background.
		// This is just a cheap rejection before reading the full record to
		// check its exact variant.
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
	shinyRateCache.rates = next
	shinyRateCache.expires = time.Now().Add(5 * time.Minute)
	return next
}

// rolls packs the shiny roll (low 24 bits), the background roll (next 24
// bits) and a valid flag (bit 63), keeping each entry at 48 bytes.
type shinyRollEntry struct {
	player    [32]byte
	encounter uint64
	rolls     uint64
}
type shinyRollShard struct {
	sync.Mutex
	entries []shinyRollEntry
}

// Globally bounded to 96 MiB, allocated lazily; collisions evict, never match.
// Immutable rolls need no invalidation when a rate changes.
var shinyRollShards [64]shinyRollShard

type shinyPlayer struct {
	key   [32]byte
	input []byte
}

const shinyRollValid = 1 << 63

// rolls returns the shiny roll and the next value from the same generator,
// which decides the background. Both are 24-bit, compared against cutoffs.
func (p *shinyPlayer) rolls(encounter uint64) (shiny, background uint32) {
	index := ((encounter ^ binary.LittleEndian.Uint64(p.key[:8])) * 11400714819323198485) >> 43
	shard := &shinyRollShards[index&63]
	shard.Lock()
	defer shard.Unlock()
	if shard.entries == nil {
		shard.entries = make([]shinyRollEntry, 1<<15)
	}
	entry := &shard.entries[index>>6]
	if entry.rolls&shinyRollValid != 0 && entry.encounter == encounter && entry.player == p.key {
		return uint32(entry.rolls & (shinyScale - 1)), uint32(entry.rolls>>24) & (shinyScale - 1)
	}
	binary.LittleEndian.PutUint64(p.input[:8], encounter)
	digest := sha1.Sum(p.input)
	seed := binary.LittleEndian.Uint64(digest[:8])
	// Two java.util.Random nextFloat draws, kept as their 24-bit integers.
	rng := javarandom.New(seed)
	shiny, background = rng.Next(24), rng.Next(24)
	*entry = shinyRollEntry{player: p.key, encounter: encounter, rolls: shinyRollValid | uint64(background)<<24 | uint64(shiny)}
	return shiny, background
}

type shinyScan struct {
	ctx     context.Context
	rates   *shinyRates
	players map[string]*shinyPlayer
}

var shinyScanSlots = make(chan struct{}, 4)

// ScanPokemonWithShiny keeps the ordinary scan path intact. Only requests with
// shiny_for, background_for or annotate_for acquire a cancellable worker slot and load shiny rates.
func ScanPokemonWithShiny(ctx context.Context, req ApiPokemonScan3) (*ApiPokemonScanResultV3, error) {
	players := make(map[string]*shinyPlayer)
	add := func(ids []string) error {
		for _, id := range ids {
			if id == "" {
				return fmt.Errorf("empty player ID")
			}
			if players[id] == nil {
				input := make([]byte, 8+len(id))
				copy(input[8:], id)
				players[id] = &shinyPlayer{key: sha256.Sum256([]byte(id)), input: input}
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
	select {
	case shinyScanSlots <- struct{}{}:
		defer func() { <-shinyScanSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	req.shiny = &shinyScan{ctx: ctx, rates: loadShinyRates(ctx), players: players}
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

func shinyEligible(isDitto bool, pokemon, display int, seen NullSeenType) bool {
	return !isDitto && pokemon != 132 && (display == 0 || display == pokemon) &&
		(seen == SeenTypeFrom(SeenTypeCodeWild) || seen == SeenTypeFrom(SeenTypeCodeEncounter))
}

// cutoffs returns the shiny and background thresholds for a variant at a
// location. The timezone is looked up only when the variant has overrides.
func (s *shinyScan) cutoffs(pokemon, form, costume int, lat, lon float64) (shiny, background uint32) {
	rate, ok := s.rates.variants[shinyVariant{pokemon, form, costume}]
	if !ok {
		return defaultShinyCutoff, 0
	}
	zone := ""
	if len(rate.Overrides) > 0 || len(rate.BackgroundOverrides) > 0 {
		zone = tz.SearchTimezone(lat, lon)
	}
	shiny = rate.Prediction.cutoff(defaultShinyCutoff)
	if override, ok := rate.Overrides[zone]; ok {
		shiny = override.cutoff(defaultShinyCutoff)
	}
	if rate.Background != nil {
		background = rate.Background.cutoff(0)
	}
	if override, ok := rate.BackgroundOverrides[zone]; ok {
		background = override.cutoff(0)
	}
	return shiny, background
}

// lowest returns the smallest shiny and background rolls across players.
func (s *shinyScan) lowest(id uint64, players []string) (shiny, background uint32) {
	shiny, background = shinyScale, shinyScale
	for _, player := range players {
		a, b := s.players[player].rolls(id)
		shiny, background = min(shiny, a), min(background, b)
	}
	return shiny, background
}

// matchAny is the second pass over personal clauses for candidates the
// normal v3 selection did not match.
func (s *shinyScan) matchAny(id uint64, lookup *PokemonLookup, pvp *PokemonPvpLookup, clauses []ApiPokemonDnfFilter3) (bool, bool) {
	if s.ctx.Err() != nil {
		return false, true
	}
	for i := range clauses {
		if pokemonClauseApplies(&clauses[i], lookup) && isPokemonDnfMatch3(lookup, pvp, &clauses[i]) && s.match(id, lookup, &clauses[i]) {
			return true, false
		}
	}
	return false, false
}

func (s *shinyScan) match(id uint64, lookup *PokemonLookup, filter *ApiPokemonDnfFilter3) bool {
	if lookup.PokemonId == 132 {
		return false
	}
	shiny, _ := s.lowest(id, filter.ShinyFor)
	_, background := s.lowest(id, filter.BackgroundFor)
	if len(filter.ShinyFor) > 0 && shiny >= max(defaultShinyCutoff, s.rates.maximum[lookup.PokemonId]) ||
		len(filter.BackgroundFor) > 0 && background >= s.rates.backgroundMaximum[lookup.PokemonId] {
		return false
	}
	// Typically fewer than 1% of candidates reach the record lock. No decode
	// changes, extra lookup fields, SQL queries, or coordinate calls to the rates source.
	p, unlock, _ := peekPokemonRecordReadOnly(id, "API.ScanPokemon.shiny")
	if p == nil {
		return false
	}
	pokemon, form, costume := int(p.PokemonId), int(p.Form.ValueOrZero()), int(p.Costume.ValueOrZero())
	lat, lon := p.Lat, p.Lon
	eligible := shinyEligible(p.IsDitto, pokemon, int(p.DisplayPokemonId.ValueOrZero()), p.SeenType)
	unlock()
	if !eligible {
		return false
	}
	shinyCutoff, backgroundCutoff := s.cutoffs(pokemon, form, costume, lat, lon)
	return (len(filter.ShinyFor) == 0 || shiny < shinyCutoff) &&
		(len(filter.BackgroundFor) == 0 || background < backgroundCutoff)
}

// annotate fills ShinyFor and BackgroundFor from the already-built result, so
// it needs no record lock. Ineligible results get neither.
func (s *shinyScan) annotate(r *ApiPokemonResult, players []string) {
	id, err := strconv.ParseUint(r.Id, 10, 64)
	if err != nil {
		return
	}
	seen := NullSeenType{}
	if r.SeenType != nil {
		seen, _ = ParseSeenType(*r.SeenType)
	}
	display := 0
	if r.DisplayPokemonId != nil {
		display = int(*r.DisplayPokemonId)
	}
	pokemon := int(r.PokemonId)
	if !shinyEligible(r.IsDitto, pokemon, display, seen) {
		return
	}
	form, costume := 0, 0
	if r.Form != nil {
		form = int(*r.Form)
	}
	if r.Costume != nil {
		costume = int(*r.Costume)
	}
	shinyCutoff, backgroundCutoff := s.cutoffs(pokemon, form, costume, r.Lat, r.Lon)
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
