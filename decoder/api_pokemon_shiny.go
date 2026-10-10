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
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/tz"
)

const shinyScale = 1 << 24
const defaultShinyCutoff = shinyScale / 512

type shinyPrediction struct {
	Probability float64 `json:"probability"`
	Supported   bool    `json:"supported"`
}

func (p shinyPrediction) cutoff() uint32 {
	if !p.Supported {
		return 0
	}
	if math.IsNaN(p.Probability) || p.Probability < 0 || p.Probability > 1 {
		return defaultShinyCutoff
	}
	return uint32(math.Ceil(p.Probability * shinyScale))
}

type shinyVariant struct{ Pokemon, Form, Costume int }
type shinyRate struct {
	Pokemon    int                        `json:"pokemon"`
	Form       int                        `json:"form"`
	Costume    int                        `json:"costume"`
	Prediction shinyPrediction            `json:"prediction"`
	Overrides  map[string]shinyPrediction `json:"overrides"`
}
type shinyRates struct {
	variants map[shinyVariant]shinyRate
	maximum  map[int16]uint32
}

var shinxRateCache struct {
	sync.Mutex
	rates   *shinyRates
	expires time.Time
}

// Fetch once per five minutes, shared by scans. A temporary outage retains the
// last snapshot; species without a rate use 1/512.
func loadShinyRates(ctx context.Context) *shinyRates {
	shinxRateCache.Lock()
	defer shinxRateCache.Unlock()
	if shinxRateCache.rates == nil {
		shinxRateCache.rates = &shinyRates{variants: map[shinyVariant]shinyRate{}, maximum: map[int16]uint32{}}
	}
	if time.Now().Before(shinxRateCache.expires) {
		return shinxRateCache.rates
	}
	shinxRateCache.expires = time.Now().Add(30 * time.Second)
	c := config.Config.Shinx
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+"/v1/rates?compact=1", nil)
	if err != nil {
		return shinxRateCache.rates
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		log.Warn("Shinx rate refresh failed; retaining cached rates")
		return shinxRateCache.rates
	}
	defer res.Body.Close()
	var feed struct {
		Rates []shinyRate `json:"rates"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&feed) != nil {
		log.Warn("Shinx rate refresh failed; retaining cached rates")
		return shinxRateCache.rates
	}
	next := &shinyRates{variants: make(map[shinyVariant]shinyRate), maximum: make(map[int16]uint32)}
	for _, r := range feed.Rates {
		next.variants[shinyVariant{r.Pokemon, r.Form, r.Costume}] = r
		// Unknown costumes/forms still default to 1/512. This is just a cheap
		// rejection before reading the full record to check its exact variant.
		bound := max(defaultShinyCutoff, r.Prediction.cutoff())
		for _, p := range r.Overrides {
			bound = max(bound, p.cutoff())
		}
		next.maximum[int16(r.Pokemon)] = max(next.maximum[int16(r.Pokemon)], bound)
	}
	shinxRateCache.rates = next
	shinxRateCache.expires = time.Now().Add(5 * time.Minute)
	return next
}

type shinyRollEntry struct {
	player    [32]byte
	encounter uint64
	roll      uint32
	valid     bool
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

func (p *shinyPlayer) roll(encounter uint64) uint32 {
	index := ((encounter ^ binary.LittleEndian.Uint64(p.key[:8])) * 11400714819323198485) >> 43
	shard := &shinyRollShards[index&63]
	shard.Lock()
	defer shard.Unlock()
	if shard.entries == nil {
		shard.entries = make([]shinyRollEntry, 1<<15)
	}
	entry := &shard.entries[index>>6]
	if entry.valid && entry.encounter == encounter && entry.player == p.key {
		return entry.roll
	}
	binary.LittleEndian.PutUint64(p.input[:8], encounter)
	digest := sha1.Sum(p.input)
	seed := binary.LittleEndian.Uint64(digest[:8])
	roll := uint32((((seed^0x5deece66d)*0x5deece66d + 11) & ((1 << 48) - 1)) >> 24)
	*entry = shinyRollEntry{player: p.key, encounter: encounter, roll: roll, valid: true}
	return roll
}

type shinyScan struct {
	ctx     context.Context
	rates   *shinyRates
	players map[string]*shinyPlayer
}

var shinyScanSlots = make(chan struct{}, 4)

// ScanPokemonWithShiny keeps the ordinary scan path intact. Only requests with
// shiny_for acquire a cancellable worker slot and load Shinx rates.
func ScanPokemonWithShiny(ctx context.Context, req ApiPokemonScan3) (*ApiPokemonScanResultV3, error) {
	players := make(map[string]*shinyPlayer)
	for _, f := range req.DnfFilters {
		for _, id := range f.ShinyFor {
			if id == "" {
				return nil, fmt.Errorf("empty player ID")
			}
			if players[id] == nil {
				input := make([]byte, 8+len(id))
				copy(input[8:], id)
				players[id] = &shinyPlayer{key: sha256.Sum256([]byte(id)), input: input}
			}
		}
	}
	if len(players) == 0 {
		return GetPokemonInArea3Clean(req), nil
	}
	if config.Config.Shinx.URL == "" {
		return nil, fmt.Errorf("shinx is not configured")
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
	return result, nil
}

func (s *shinyScan) match(id uint64, lookup *PokemonLookup, filter *ApiPokemonDnfFilter3) (bool, bool) {
	if s.ctx.Err() != nil {
		return false, true
	}
	if len(filter.ShinyFor) == 0 {
		return true, false
	}
	if lookup.PokemonId == 132 {
		return false, false
	}
	bound := max(defaultShinyCutoff, s.rates.maximum[lookup.PokemonId])
	lowest := uint32(shinyScale)
	for _, player := range filter.ShinyFor {
		lowest = min(lowest, s.players[player].roll(id))
	}
	if lowest >= bound {
		return false, false
	}
	// Typically fewer than 1% of candidates reach the record lock. No decode
	// changes, extra lookup fields, SQL queries, or coordinate calls to Shinx.
	p, unlock, _ := peekPokemonRecordReadOnly(id, "API.ScanPokemon.shiny")
	if p == nil {
		return false, false
	}
	pokemon, form, costume := int(p.PokemonId), int(p.Form.ValueOrZero()), int(p.Costume.ValueOrZero())
	lat, lon := p.Lat, p.Lon
	eligible := !p.IsDitto && pokemon != 132 && (p.DisplayPokemonId.ValueOrZero() == 0 || int(p.DisplayPokemonId.ValueOrZero()) == pokemon) && (p.SeenType == SeenTypeFrom(SeenTypeCodeWild) || p.SeenType == SeenTypeFrom(SeenTypeCodeEncounter))
	unlock()
	if !eligible {
		return false, false
	}
	cutoff := uint32(defaultShinyCutoff)
	if rate, ok := s.rates.variants[shinyVariant{pokemon, form, costume}]; ok {
		cutoff = rate.Prediction.cutoff()
		if len(rate.Overrides) > 0 {
			if override, ok := rate.Overrides[tz.SearchTimezone(lat, lon)]; ok {
				cutoff = override.cutoff()
			}
		}
	}
	return lowest < cutoff, false
}
