package decoder

import (
	"encoding/json"
	"errors"
	"fmt"
	"golbat/pogo"
	"golbat/util"
	"net/http"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// master-latest-golbat.json carries the species, form and type data plus the
// quick_moves/charged_moves pools that move derivation indexes into.
const masterFileURL = "https://raw.githubusercontent.com/WatWowMap/Masterfile-Generator/master/master-latest-golbat.json"

// masterFile is the masterfile as a remote document: fetched from
// masterFileURL, cached, refreshed hourly, and bundled in the binary.
var masterFile = &remoteFile{
	name:      "MasterFile",
	url:       masterFileURL,
	cachePath: "cache/master-latest-golbat.json",
	bundled:   pogo.BundledMasterFile,
	client:    &http.Client{Timeout: 15 * time.Second},
	apply:     loadMasterFileBytes,
}

type MasterFileData struct {
	Initialized bool                      `json:"-"`
	Pokemon     map[int]MasterFilePokemon `json:"pokemon"`
	Costumes    map[int]bool              `json:"costumes"`
}

type MasterFilePokemon struct {
	Name  string `json:"name"`
	Types []int  `json:"types"`
	// QuickMoves/ChargedMoves are the species' ordered move pools, indexed
	// by encounter.Draw.MoveIndices (see movePoolsFor and derivedMoves).
	QuickMoves   []int                  `json:"quick_moves"`
	ChargedMoves []int                  `json:"charged_moves"`
	Forms        map[int]MasterFileForm `json:"forms"`
	// Size is optional: personal height/weight annotation needs it.
	Size MasterFileSize `json:"-"`
}

type MasterFileForm struct {
	Types []int `json:"types"`
	// QuickMoves/ChargedMoves override the species' own lists for this form
	// only when present and non-empty, and each does so independently: a
	// form may carry charged_moves and no quick_moves. Resolve each list
	// separately, not as a pair.
	QuickMoves   []int `json:"quick_moves"`
	ChargedMoves []int `json:"charged_moves"`
	// Size overrides the species' own field by field, where present.
	Size MasterFileSize `json:"-"`
}

// MasterFileSize is a species' or form's average height (m) and weight (kg)
// and its size-class bounds (m) from the game master's size settings. Zero
// means absent.
type MasterFileSize struct {
	Height, Weight float64
	Bounds         sizeBounds
}

// sizeBounds are XXS lower, XS lower, M lower, M upper, XL upper and XXL upper.
type sizeBounds [6]float64

var sizeBoundNames = [6]string{"xxsLowerBound", "xsLowerBound", "mLowerBound", "mUpperBound", "xlUpperBound", "xxlUpperBound"}

// UnmarshalJSON accepts the generator's size_settings either as an object
// keyed by setting name or as its raw list of {name, value} pairs. Other
// settings (scale multipliers, display flags) are ignored.
func (b *sizeBounds) UnmarshalJSON(data []byte) error {
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		var list []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		for _, setting := range list {
			values[setting.Name] = setting.Value
		}
	}
	for i, name := range sizeBoundNames {
		value, ok := values[name]
		if !ok {
			value = values[snakeCase(name)]
		}
		if f, ok := value.(float64); ok {
			b[i] = f
		}
	}
	return nil
}

func snakeCase(name string) string {
	var out []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			out = append(out, '_', c+'a'-'A')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}

// valid reports whether all six bounds are present and ordered.
func (b sizeBounds) valid() bool {
	if b[0] <= 0 {
		return false
	}
	for i := 1; i < len(b); i++ {
		if b[i] < b[i-1] {
			return false
		}
	}
	return true
}

// sizeFor resolves a species/form's size data, the form overriding the
// species field by field. ok is false unless all of it is present.
func sizeFor(data MasterFileData, pokemonId, form int16) (size MasterFileSize, ok bool) {
	pokemon, found := data.Pokemon[int(pokemonId)]
	if !found {
		return size, false
	}
	size = pokemon.Size
	if formData, found := pokemon.Forms[int(form)]; found && form > 0 {
		if formData.Size.Height > 0 {
			size.Height = formData.Size.Height
		}
		if formData.Size.Weight > 0 {
			size.Weight = formData.Size.Weight
		}
		if formData.Size.Bounds.valid() {
			size.Bounds = formData.Size.Bounds
		}
	}
	return size, size.Height > 0 && size.Weight > 0 && size.Bounds.valid()
}

type rawMasterFile struct {
	Pokemon  map[string]rawMasterFilePokemon `json:"pokemon"`
	Costumes map[string]json.RawMessage      `json:"costumes"`
}

type rawMasterFilePokemon struct {
	Name         string                              `json:"name,omitempty"`
	Types        []int                               `json:"types"`
	QuickMoves   []int                               `json:"quick_moves"`
	ChargedMoves []int                               `json:"charged_moves"`
	Forms        map[string]rawMasterFilePokemonForm `json:"forms"`
	rawMasterFileSize
}

type rawMasterFilePokemonForm struct {
	Types        []int `json:"types"`
	QuickMoves   []int `json:"quick_moves"`
	ChargedMoves []int `json:"charged_moves"`
	rawMasterFileSize
}

type rawMasterFileSize struct {
	Height       float64    `json:"height"`
	Weight       float64    `json:"weight"`
	SizeSettings sizeBounds `json:"size_settings"`
}

func (r rawMasterFileSize) size() MasterFileSize {
	return MasterFileSize{Height: r.Height, Weight: r.Weight, Bounds: r.SizeSettings}
}

var (
	masterFileMu   sync.RWMutex
	masterFileData MasterFileData
)

// EnsureMasterFileData loads the masterfile at startup; see remoteFile.ensure.
func EnsureMasterFileData() error {
	source, err := masterFile.ensure()
	if err != nil {
		return err
	}
	log.Infof("MasterFile loaded from %s", source)
	return nil
}

// WatchMasterFileData refreshes the masterfile hourly, reloading Ohbem when it
// changes.
func WatchMasterFileData() error {
	masterFile.onRefresh = reloadOhbemFromMasterFile
	return masterFile.watch(60 * time.Minute)
}

func loadMasterFileBytes(data []byte) error {
	var raw rawMasterFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("can't unmarshal MasterFile: %w", err)
	}

	parsed := MasterFileData{
		Pokemon:  make(map[int]MasterFilePokemon, len(raw.Pokemon)),
		Costumes: make(map[int]bool, len(raw.Costumes)),
	}

	for pid, pokemon := range raw.Pokemon {
		intPid, err := strconv.Atoi(pid)
		if err != nil {
			continue
		}
		forms := make(map[int]MasterFileForm, len(pokemon.Forms))
		for fid, form := range pokemon.Forms {
			intFid, err := strconv.Atoi(fid)
			if err != nil {
				continue
			}
			forms[intFid] = MasterFileForm{
				Types:        append([]int(nil), form.Types...),
				QuickMoves:   append([]int(nil), form.QuickMoves...),
				ChargedMoves: append([]int(nil), form.ChargedMoves...),
				Size:         form.size(),
			}
		}
		parsed.Pokemon[intPid] = MasterFilePokemon{
			Name:         pokemon.Name,
			Types:        append([]int(nil), pokemon.Types...),
			QuickMoves:   append([]int(nil), pokemon.QuickMoves...),
			ChargedMoves: append([]int(nil), pokemon.ChargedMoves...),
			Forms:        forms,
			Size:         pokemon.size(),
		}
	}

	for cid := range raw.Costumes {
		if intCid, err := strconv.Atoi(cid); err == nil {
			parsed.Costumes[intCid] = true
		}
	}

	parsed.Initialized = true

	if len(parsed.Pokemon) == 0 {
		return errors.New("MasterFile has no pokemon")
	}

	masterFileMu.Lock()
	masterFileData = parsed
	masterFileMu.Unlock()
	return nil
}

func getMasterFileData() MasterFileData {
	masterFileMu.RLock()
	data := masterFileData
	masterFileMu.RUnlock()
	return data
}

// movePoolLookupDrops rate-limits the unknown-species and unknown-form
// warnings. movePoolsFor runs for every sighting whose moves are derived and
// every encounter checked, not once per species, so a species or form the
// local masterfile has not caught up to — a new release, a new event form —
// would otherwise emit one line per sighting per GMO.
var movePoolLookupDrops util.DropReporter

// movePoolsFor resolves the quick- and charged-move ID pools for one
// species/form, mirroring findBoostedWeathers' lookup and unknown-form
// warning. Unlike Types, which travels as one unit, a form's quick_moves and
// charged_moves override independently, so each list falls back to the
// species' own separately.
//
// ok is false when the species is unknown, or when either resolved list
// (after the per-list fallback) is still empty — encounter.Draw.MoveIndices
// requires a positive size for both pools (javarandom.Int panics on a bound
// <= 0), so a caller must check ok before calling it and must not guess a
// move when it is false.
func movePoolsFor(data MasterFileData, pokemonId, form int16) (quick, charged []int, ok bool) {
	pokemon, found := data.Pokemon[int(pokemonId)]
	if !found {
		movePoolLookupDrops.Report(func(dropped int64) {
			log.Warnf("[MOVEPOOL] %d move-pool lookups missed, most recently PokemonId %d", dropped, pokemonId)
		})
		return nil, nil, false
	}
	quick, charged = pokemon.QuickMoves, pokemon.ChargedMoves

	if form > 0 {
		formData, formFound := pokemon.Forms[int(form)]
		if !formFound {
			movePoolLookupDrops.Report(func(dropped int64) {
				log.Warnf("[MOVEPOOL] %d move-pool lookups missed, most recently Form %d for PokemonId %d", dropped, form, pokemonId)
			})
		} else {
			if len(formData.QuickMoves) > 0 {
				quick = formData.QuickMoves
			}
			if len(formData.ChargedMoves) > 0 {
				charged = formData.ChargedMoves
			}
		}
	}

	if len(quick) == 0 || len(charged) == 0 {
		return nil, nil, false
	}
	return quick, charged, true
}
