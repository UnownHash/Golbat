package decoder

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"golbat/db"
	"golbat/grpc"
	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// populateInternal hydrates the in-memory scan history from the protobuf bytes
// held in the golbat_internal column. This is the read boundary: the only place
// grpc.PokemonInternal is unmarshaled.
func (pokemon *Pokemon) populateInternal() {
	if len(pokemon.GolbatInternal) == 0 || len(pokemon.scanHistory) != 0 {
		return
	}
	var internal grpc.PokemonInternal
	if err := proto.Unmarshal(pokemon.GolbatInternal, &internal); err != nil {
		log.Warnf("Failed to parse internal data for %d: %s", pokemon.Id, err)
		pokemon.scanHistory = nil
		return
	}
	pokemon.scanHistory = scanHistoryFromProto(&internal)
}

func (pokemon *Pokemon) locateScan(isStrong bool, isBoosted bool) (*pokemonScan, bool) {
	pokemon.populateInternal()
	var bestMatching *pokemonScan
	for _, entry := range pokemon.scanHistory {
		if entry.Strong != isStrong {
			continue
		}
		if entry.Weather != int32(pogo.GameplayWeatherProto_NONE) == isBoosted {
			return entry, true
		} else {
			bestMatching = entry
		}
	}
	return bestMatching, false
}

func (pokemon *Pokemon) locateAllScans() (unboosted, boosted, strong *pokemonScan) {
	pokemon.populateInternal()
	for _, entry := range pokemon.scanHistory {
		if entry.Strong {
			strong = entry
		} else if entry.Weather != int32(pogo.GameplayWeatherProto_NONE) {
			boosted = entry
		} else {
			unboosted = entry
		}
	}
	return
}

func (pokemon *Pokemon) isNewRecord() bool {
	return pokemon.newRecord
}

const (
	// pokemonUnverifiedTTL (+ jitter) replaces the flat cache-default TTL
	// for pokemon without a verified despawn. The jitter spreads
	// restart-synchronized cohorts (preload + cold-cache ingest all
	// stamped in the same few minutes) so their expiry an hour later
	// arrives as a stream of tree deletes and eviction events rather than
	// one burst.
	pokemonUnverifiedTTL       = 55 * time.Minute
	pokemonUnverifiedTTLJitter = 10 * time.Minute
)

func (pokemon *Pokemon) remainingDuration(now int64) time.Duration {
	if pokemon.ExpireTimestampVerified {
		timeLeft := 60 + int64(pokemon.ExpireTimestamp.ValueOrZero()) - now
		if timeLeft > 60 {
			return time.Duration(timeLeft) * time.Second
		}
		// At/past despawn: keep briefly for late queries rather than
		// granting a fresh hour to a corpse.
		return time.Minute
	}
	return pokemonUnverifiedTTL + rand.N(pokemonUnverifiedTTLJitter)
}

// encounterStatsDuration is the TTL for the encounter-dedup stats cache.
// Distinct from the pokemon-cache TTL: past-despawn and unverified entries
// must keep the cache's full default window so late or retried protos still
// deduplicate instead of inflating per-area encounter/shiny stats.
func (pokemon *Pokemon) encounterStatsDuration(now int64) time.Duration {
	if pokemon.ExpireTimestampVerified {
		if timeLeft := 60 + int64(pokemon.ExpireTimestamp.ValueOrZero()) - now; timeLeft > 60 {
			return time.Duration(timeLeft) * time.Second
		}
	}
	return 0 // the encounter cache interprets 0 as its default TTL
}

func (pokemon *Pokemon) addWildPokemon(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, timestampMs int64, trustworthyTimestamp bool) {
	if wildPokemon.EncounterId != uint64(pokemon.Id) {
		panic("Unmatched EncounterId")
	}
	pokemon.SetLat(wildPokemon.Latitude)
	pokemon.SetLon(wildPokemon.Longitude)

	spawnId, err := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	if err != nil {
		panic(err)
	}
	pokemon.SetSpawnId(null.IntFrom(spawnId))

	pokemon.setExpireTimestampFromSpawnpoint(ctx, db, timestampMs, trustworthyTimestamp)
	pokemon.setPokemonDisplay(int16(wildPokemon.Pokemon.PokemonId), wildPokemon.Pokemon.PokemonDisplay)
}

// wildSignificantUpdate returns true if the wild pokemon is significantly different from the current pokemon and
// should be written.
func (pokemon *Pokemon) wildSignificantUpdate(wildPokemon *pogo.WildPokemonProto, time int64) bool {
	pokemonDisplay := wildPokemon.Pokemon.PokemonDisplay
	// We would accept a wild update if the pokemon has changed; or to extend an unknown spawn time that is expired

	return pokemon.SeenType.ValueOrZero() == SeenType_Cell ||
		pokemon.SeenType.ValueOrZero() == SeenType_NearbyStop ||
		pokemon.PokemonId != int16(wildPokemon.Pokemon.PokemonId) ||
		int64(pokemon.Form.ValueOrZero()) != int64(pokemonDisplay.Form) ||
		int64(pokemon.Weather.ValueOrZero()) != int64(pokemonDisplay.WeatherBoostedCondition) ||
		int64(pokemon.Costume.ValueOrZero()) != int64(pokemonDisplay.Costume) ||
		int64(pokemon.Gender.ValueOrZero()) != int64(pokemonDisplay.Gender) ||
		(!pokemon.ExpireTimestampVerified && int64(pokemon.ExpireTimestamp.ValueOrZero()) < time)
}

// nearbySignificantUpdate returns true if the wild pokemon is significantly different from the current pokemon and
// should be written.
func (pokemon *Pokemon) nearbySignificantUpdate(wildPokemon *pogo.NearbyPokemonProto, time int64) bool {
	pokemonDisplay := wildPokemon.PokemonDisplay
	// We would accept a wild update if the pokemon has changed; or to extend an unknown spawn time that is expired

	pokemonChanged := pokemon.PokemonId != int16(pokemonDisplay.DisplayId) ||
		int64(pokemon.Form.ValueOrZero()) != int64(pokemonDisplay.Form) ||
		int64(pokemon.Weather.ValueOrZero()) != int64(pokemonDisplay.WeatherBoostedCondition) ||
		int64(pokemon.Costume.ValueOrZero()) != int64(pokemonDisplay.Costume) ||
		int64(pokemon.Gender.ValueOrZero()) != int64(pokemonDisplay.Gender)

	if pokemonChanged {
		return true
	}

	hasExpired := (!pokemon.ExpireTimestampVerified && int64(pokemon.ExpireTimestamp.ValueOrZero()) < time)

	if hasExpired {
		return true
	}

	if pokemon.SeenType.ValueOrZero() == SeenType_Cell {
		return true
	}

	// if it's at a nearby stop, or encounter and no other details have changed update is not worthwhile
	return false
}

func (pokemon *Pokemon) updateFromWild(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string) {
	pokemon.SetIsEvent(0)
	switch pokemon.SeenType.ValueOrZero() {
	case "", SeenType_Cell, SeenType_NearbyStop:
		pokemon.SetSeenType(SeenType_Wild)
	}
	pokemon.addWildPokemon(ctx, db, wildPokemon, timestampMs, true)
	pokemon.recomputeCpIfNeeded(ctx, db, weather)
	pokemon.SetUsername(null.StringFrom(username))
	pokemon.SetCellId(null.IntFrom(cellId))
}

// updateFromMap applies a GMO lure sighting (fort.ActivePokemon) to this
// pokemon. The fort's identity and coordinates are captured at GMO
// extraction (RawMapPokemonData), so placement never depends on the
// pokestop cache. Returns true when the record changed and needs saving.
func (pokemon *Pokemon) updateFromMap(ctx context.Context, db db.DbDetails, mapPokemon RawMapPokemonData, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, username string) bool {
	if pokemon.isNewRecord() {
		pokemon.SetIsEvent(0)
		pokemon.SetPokestopId(null.StringFrom(mapPokemon.FortId))
		pokemon.SetLat(mapPokemon.Lat)
		pokemon.SetLon(mapPokemon.Lon)
		pokemon.SetSeenType(SeenType_LureWild)

		if mapPokemon.Data.PokemonDisplay != nil {
			pokemon.setPokemonDisplay(int16(mapPokemon.Data.PokedexTypeId), mapPokemon.Data.PokemonDisplay)
			pokemon.recomputeCpIfNeeded(ctx, db, weather)
			// The mapPokemon and nearbyPokemon GMOs don't contain actual shininess.
			// shiny = mapPokemon.pokemonDisplay.shiny
		} else {
			log.Warnf("[POKEMON] MapPokemonProto missing PokemonDisplay for %d", pokemon.Id)
		}
		pokemon.SetUsername(null.StringFrom(username))

		if mapPokemon.Data.ExpirationTimeMs > 0 {
			pokemon.SetExpireTimestamp(null.IntFrom(mapPokemon.Data.ExpirationTimeMs / 1000))
			pokemon.SetExpireTimestampVerified(true)
			// if we have cached an encounter for this pokemon, update the TTL.
			encounterCache.UpdateTTL(uint64(pokemon.Id), pokemon.encounterStatsDuration(mapPokemon.Timestamp/1000))
		} else {
			pokemon.SetExpireTimestampVerified(false)
		}
		pokemon.SetCellId(null.IntFrom(int64(mapPokemon.Cell)))
		return true
	}

	// Existing record: the GMO contributes only what it alone knows — the
	// verified despawn time. Never touch encounter data and never downgrade
	// lure_encounter to lure_wild.
	switch pokemon.SeenType.ValueOrZero() {
	case SeenType_LureWild, SeenType_LureEncounter:
	default:
		return false
	}

	changed := false
	if mapPokemon.Data.ExpirationTimeMs > 0 && !pokemon.ExpireTimestampVerified {
		pokemon.SetExpireTimestamp(null.IntFrom(mapPokemon.Data.ExpirationTimeMs / 1000))
		pokemon.SetExpireTimestampVerified(true)
		encounterCache.UpdateTTL(uint64(pokemon.Id), pokemon.encounterStatsDuration(mapPokemon.Timestamp/1000))
		changed = true
	}
	if !pokemon.CellId.Valid {
		pokemon.SetCellId(null.IntFrom(int64(mapPokemon.Cell)))
		changed = true
	}
	if !pokemon.Username.Valid {
		pokemon.SetUsername(null.StringFrom(username))
		changed = true
	}
	return changed
}

// calculateIv is the sole entry point that mutates AtkIv/DefIv/StaIv — they
// have no exported setters (see the Pokemon doc comment) so that all three
// stay consistent with each other and with Iv. Clamped directly here rather
// than through Set*Iv methods that don't exist.
func (pokemon *Pokemon) calculateIv(a int64, d int64, s int64) {
	if int64(pokemon.AtkIv.ValueOrZero()) != a || int64(pokemon.DefIv.ValueOrZero()) != d || int64(pokemon.StaIv.ValueOrZero()) != s ||
		!pokemon.AtkIv.Valid || !pokemon.DefIv.Valid || !pokemon.StaIv.Valid {
		pokemon.AtkIv = clampUint8(null.IntFrom(a), "atk_iv")
		pokemon.DefIv = clampUint8(null.IntFrom(d), "def_iv")
		pokemon.StaIv = clampUint8(null.IntFrom(s), "sta_iv")
		pokemon.SetIv(null.FloatFrom(float64(a+d+s) / .45))
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) updateFromNearby(ctx context.Context, db db.DbDetails, nearbyPokemon *pogo.NearbyPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string) {
	pokemon.SetIsEvent(0)
	pokestopId := nearbyPokemon.FortId
	pokemon.setPokemonDisplay(int16(nearbyPokemon.PokedexNumber), nearbyPokemon.PokemonDisplay)
	pokemon.recomputeCpIfNeeded(ctx, db, weather)
	pokemon.SetUsername(null.StringFrom(username))

	var lat, lon float64
	overrideLatLon := pokemon.isNewRecord()
	useCellLatLon := true
	if pokestopId != "" {
		switch pokemon.SeenType.ValueOrZero() {
		case "", SeenType_Cell:
			overrideLatLon = true // a better estimate is available
		case SeenType_NearbyStop:
		default:
			return
		}
		pokestop, unlock, _ := getPokestopRecordReadOnly(ctx, db, pokestopId, "updateFromNearby")
		if pokestop == nil {
			// Unrecognised pokestop, rollback changes
			overrideLatLon = pokemon.isNewRecord()
		} else {
			pokemon.SetSeenType(SeenType_NearbyStop)
			pokemon.SetPokestopId(null.StringFrom(pokestopId))
			lat, lon = pokestop.Lat, pokestop.Lon
			useCellLatLon = false
			unlock()
		}
	}
	if useCellLatLon {
		// Cell Pokemon
		if !overrideLatLon && pokemon.SeenType.ValueOrZero() != SeenType_Cell {
			// do not downgrade to nearby cell
			return
		}

		s2cell := s2.CellFromCellID(s2.CellID(cellId))
		lat = s2cell.CapBound().RectBound().Center().Lat.Degrees()
		lon = s2cell.CapBound().RectBound().Center().Lng.Degrees()

		pokemon.SetSeenType(SeenType_Cell)
	}
	if overrideLatLon {
		pokemon.SetLat(lat)
		pokemon.SetLon(lon)
	} else {
		midpoint := s2.LatLngFromPoint(s2.Point{Vector: s2.PointFromLatLng(s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon)).
			Add(s2.PointFromLatLng(s2.LatLngFromDegrees(lat, lon)).Vector)})
		pokemon.SetLat(midpoint.Lat.Degrees())
		pokemon.SetLon(midpoint.Lng.Degrees())
	}
	pokemon.SetCellId(null.IntFrom(cellId))
	pokemon.setUnknownTimestamp(timestampMs / 1000)
}

const SeenType_Cell string = "nearby_cell"                              // Pokemon was seen in a cell (without accurate location)
const SeenType_NearbyStop string = "nearby_stop"                        // Pokemon was seen at a nearby Pokestop, location set to lon, lat of pokestop
const SeenType_Wild string = "wild"                                     // Pokemon was seen in the wild, accurate location but with no IV details
const SeenType_Encounter string = "encounter"                           // Pokemon has been encountered giving exact details of current IV
const SeenType_LureWild string = "lure_wild"                            // Pokemon was seen at a lure
const SeenType_LureEncounter string = "lure_encounter"                  // Pokemon has been encountered at a lure
const SeenType_TappableEncounter string = "tappable_encounter"          // Pokemon has been encountered from tappable
const SeenType_TappableLureEncounter string = "tappable_lure_encounter" // Pokemon has been encountered from a lured tappable

// SeenTypeCode is the in-memory representation of the seen_type enum column.
//
// The column holds one of eight strings; storing it as a null.String costs a
// 24-byte header plus a heap pointer per cached pokemon. The code is one byte.
// NullSeenType converts at the database and JSON boundaries so both wire
// formats are unchanged.
//
// Code 0 is reserved as SeenTypeCodeUnset, not a real seen type. Without
// this, `SeenTypeCodeWild = iota` would make Wild — the enum's most common
// real value — indistinguishable from a NullSeenType's Go zero value, so any
// code that read .Code without checking .Valid would silently treat a NULL
// seen_type as Wild. Reserving 0 makes that misread produce Unset instead,
// which has no string form and which Value() refuses to write.
type SeenTypeCode uint8

const (
	SeenTypeCodeUnset SeenTypeCode = iota // zero value: not a real seen type
	SeenTypeCodeWild
	SeenTypeCodeEncounter
	SeenTypeCodeNearbyStop
	SeenTypeCodeCell
	SeenTypeCodeLureWild
	SeenTypeCodeLureEncounter
	SeenTypeCodeTappableEncounter
	SeenTypeCodeTappableLureEncounter
)

// seenTypeStrings maps codes to the exact strings in the enum column. The
// order must match the constants above, including the leading "" at index 0
// for SeenTypeCodeUnset — it is never a value the enum column holds, and
// String() relies on it to make Unset (and any other out-of-range code)
// report as "". Adding a real value here requires a migration widening the
// enum — see sql/45_tappables_seen_type_lure.up.sql.
var seenTypeStrings = [...]string{
	"", // SeenTypeCodeUnset
	SeenType_Wild,
	SeenType_Encounter,
	SeenType_NearbyStop,
	SeenType_Cell,
	SeenType_LureWild,
	SeenType_LureEncounter,
	SeenType_TappableEncounter,
	SeenType_TappableLureEncounter,
}

// seenTypeCodes maps the eight real enum strings to their codes. The empty
// string at seenTypeStrings[0] is deliberately excluded — SeenTypeCodeUnset
// must never be reachable by parsing a string, only by the Go zero value.
var seenTypeCodes = func() map[string]SeenTypeCode {
	m := make(map[string]SeenTypeCode, len(seenTypeStrings)-1)
	for i, s := range seenTypeStrings {
		if s == "" {
			continue
		}
		m[s] = SeenTypeCode(i)
	}
	return m
}()

// String returns the database representation of the code.
func (c SeenTypeCode) String() string {
	if int(c) >= len(seenTypeStrings) {
		return ""
	}
	return seenTypeStrings[c]
}

// NullSeenType is a nullable seen_type, stored as a code and presented as a
// string at every boundary.
type NullSeenType struct {
	Code  SeenTypeCode
	Valid bool
}

// SeenTypeFrom builds a valid NullSeenType from a known code.
func SeenTypeFrom(c SeenTypeCode) NullSeenType {
	return NullSeenType{Code: c, Valid: true}
}

// ParseSeenType converts a database or proto string into a NullSeenType.
// An empty string is treated as NULL; an unrecognised value is an error,
// because silently mapping it to a valid code would corrupt scan statistics.
func ParseSeenType(s string) (NullSeenType, error) {
	if s == "" {
		return NullSeenType{}, nil
	}
	c, ok := seenTypeCodes[s]
	if !ok {
		return NullSeenType{}, fmt.Errorf("unknown seen_type %q", s)
	}
	return SeenTypeFrom(c), nil
}

// ValueOrZero returns the string form, or "" if null.
func (n NullSeenType) ValueOrZero() string {
	if !n.Valid {
		return ""
	}
	return n.Code.String()
}

// Ptr returns a pointer to the string form, or nil if null. The API response
// type is *string and stays that way.
func (n NullSeenType) Ptr() *string {
	if !n.Valid {
		return nil
	}
	s := n.Code.String()
	return &s
}

func (n NullSeenType) IsZero() bool { return !n.Valid }

func (n *NullSeenType) Scan(value any) error {
	if value == nil {
		n.Code, n.Valid = 0, false
		return nil
	}
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("cannot scan %T into NullSeenType", value)
	}
	parsed, err := ParseSeenType(s)
	if err != nil {
		return err
	}
	*n = parsed
	return nil
}

// Value rejects any code with no string form — that includes both
// SeenTypeCodeUnset and any code past the end of the table. Writing "" to
// the seen_type ENUM column is accepted silently by MariaDB, so this must
// fail loudly instead: the same data-loss shape ParseSeenType already
// guards against on the read side.
func (n NullSeenType) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	s := n.Code.String()
	if s == "" {
		return nil, fmt.Errorf("seen_type code %d has no string representation", n.Code)
	}
	return s, nil
}

func (n NullSeenType) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Code.String())
}

func (n *NullSeenType) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		n.Code, n.Valid = 0, false
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseSeenType(s)
	if err != nil {
		return err
	}
	*n = parsed
	return nil
}

// A lure spits out a new pokemon every 3 minutes, and each lasts 3 minutes.
// Worst-case remaining life when a lure pokemon is first seen via a disk
// encounter, before any GMO has supplied the real despawn time.
const lureSpawnLifetimeSeconds = 180

// setExpireTimestampFromSpawnpoint sets the current Pokemon object ExpireTimeStamp, and ExpireTimeStampVerified from the Spawnpoint
// information held.
// db - the database connection to be used
// timestampMs - the timestamp to be used for calculations
// trustworthyTimestamp - whether this timestamp is fully trustworthy (ie comes from GMO server time)
func (pokemon *Pokemon) setExpireTimestampFromSpawnpoint(ctx context.Context, db db.DbDetails, timestampMs int64, trustworthyTimestamp bool) {
	if !trustworthyTimestamp && pokemon.ExpireTimestampVerified {
		// If our time is not trustworthy, and we have already set a time from some other source (eg a GMO)
		// don't modify it

		return
	}

	spawnId := pokemon.SpawnId.ValueOrZero()
	if spawnId == 0 {
		return
	}

	pokemon.ExpireTimestampVerified = false

	// Lock-free fast path: this runs once per wild/nearby pokemon and needs
	// only the spawnpoint's despawn second. The atomic mirror avoids the
	// entity mutex entirely — readers no longer queue behind writers that
	// hold it across DB loads. Mirror not yet synced (0) falls through to
	// the locked path below.
	if sp, ok := spawnpointCache.Get(spawnId); ok {
		if despawnSecond, known, synced := sp.DespawnSecFast(); synced {
			if known {
				pokemon.applyVerifiedDespawn(despawnSecond, timestampMs)
			} else {
				pokemon.setUnknownTimestamp(timestampMs / 1000)
			}
			return
		}
	}

	spawnPoint, unlock, _ := getSpawnpointRecord(ctx, db, spawnId, "setExpireTimestampFromSpawnpoint")
	if spawnPoint != nil && spawnPoint.DespawnSec.Valid {
		despawnSecond := int(spawnPoint.DespawnSec.ValueOrZero())
		unlock()

		pokemon.applyVerifiedDespawn(despawnSecond, timestampMs)
	} else {
		if unlock != nil {
			unlock()
		}
		pokemon.setUnknownTimestamp(timestampMs / 1000)
	}
}

// applyVerifiedDespawn converts a spawnpoint despawn second-of-hour into a
// verified expire timestamp for this pokemon.
func (pokemon *Pokemon) applyVerifiedDespawn(despawnSecond int, timestampMs int64) {
	date := time.Unix(timestampMs/1000, 0)
	secondOfHour := date.Second() + date.Minute()*60

	despawnOffset := despawnSecond - secondOfHour
	if despawnOffset < 0 {
		despawnOffset += 3600
	}
	pokemon.SetExpireTimestamp(null.IntFrom(int64(timestampMs)/1000 + int64(despawnOffset)))
	pokemon.SetExpireTimestampVerified(true)
}

func (pokemon *Pokemon) setUnknownTimestamp(now int64) {
	if !pokemon.ExpireTimestamp.Valid {
		pokemon.SetExpireTimestamp(null.IntFrom(now + 20*60)) // should be configurable, add on 20min
	} else {
		if int64(pokemon.ExpireTimestamp.ValueOrZero()) < now {
			pokemon.SetExpireTimestamp(null.IntFrom(now + 10*60)) // should be configurable, add on 10min
		}
	}
}

func checkScans(old *pokemonScan, new *pokemonScan) error {
	if old == nil || old.CompressedIv() == new.CompressedIv() {
		return nil
	}
	return fmt.Errorf("unexpected IV mismatch %s != %s", old, new)
}

func (pokemon *Pokemon) setDittoAttributes(mode string, isDitto bool, old, new *pokemonScan) {
	if isDitto {
		log.Debugf("[POKEMON] %d: %s Ditto found %s -> %s", pokemon.Id, mode, old, new)
		pokemon.SetIsDitto(true)
		pokemon.SetDisplayPokemonId(null.IntFrom(int64(pokemon.PokemonId)))
		pokemon.SetDisplayPokemonForm(nullIntFromUint(pokemon.Form))
		pokemon.SetPokemonId(int16(pogo.HoloPokemonId_DITTO))
		pokemon.SetForm(null.IntFrom(0))
	} else {
		log.Debugf("[POKEMON] %d: %s not Ditto found %s -> %s", pokemon.Id, mode, old, new)
	}
}
func (pokemon *Pokemon) resetDittoAttributes(mode string, old, aux, new *pokemonScan) (*pokemonScan, error) {
	log.Debugf("[POKEMON] %d: %s Ditto was reset %s (%s) -> %s", pokemon.Id, mode, old, aux, new)
	pokemon.SetIsDitto(false)
	pokemon.SetPokemonId(int16(pokemon.DisplayPokemonId.ValueOrZero()))
	pokemon.SetForm(nullIntFromUint(pokemon.DisplayPokemonForm))
	pokemon.SetDisplayPokemonId(null.NewInt(0, false))
	pokemon.SetDisplayPokemonForm(null.NewInt(0, false))
	return new, checkScans(old, new)
}

// As far as I'm concerned, wild Ditto only depends on species but not costume/gender/form
var dittoDisguises sync.Map

func confirmDitto(scan *pokemonScan) {
	now := time.Now()
	lastSeen, exists := dittoDisguises.Swap(scan.Pokemon, now)
	if exists {
		log.Debugf("[DITTO] Disguise %s reseen after %s", scan, now.Sub(lastSeen.(time.Time)))
	} else {
		var sb strings.Builder
		sb.WriteString("[DITTO] New disguise ")
		sb.WriteString(scan.String())
		sb.WriteString(" found. Current disguises ")
		dittoDisguises.Range(func(disguise, lastSeen interface{}) bool {
			sb.WriteString(strconv.FormatInt(int64(disguise.(int32)), 10))
			sb.WriteString(" (")
			sb.WriteString(now.Sub(lastSeen.(time.Time)).String())
			sb.WriteString(") ")
			return true
		})
		log.Info(sb.String())
	}
}

// detectDitto returns the IV/level set that should be used for persisting to db/seen if caught.
// error is set if something unexpected happened and the scan history should be cleared.
func (pokemon *Pokemon) detectDitto(scan *pokemonScan) (*pokemonScan, error) {
	unboostedScan, boostedScan, strongScan := pokemon.locateAllScans()
	if scan.Strong {
		if strongScan != nil {
			expectedLevel := strongScan.Level
			isBoosted := scan.Weather != int32(pogo.GameplayWeatherProto_NONE)
			if strongScan.Weather != int32(pogo.GameplayWeatherProto_NONE) != isBoosted {
				if isBoosted {
					expectedLevel += 5
				} else {
					expectedLevel -= 5
				}
			}
			if scan.Level != expectedLevel || scan.CompressedIv() != strongScan.CompressedIv() {
				return scan, fmt.Errorf("unexpected strong Pokemon (Ditto?), %s -> %s",
					strongScan, scan)
			}
		}
		return scan, nil
	}

	// Here comes the Ditto logic. Embrace yourself :)
	// Ditto weather can be split into 4 categories:
	//  - 00: No weather boost
	//  - 0P: No weather boost but Ditto is actually boosted by partly cloudy causing seen IV to be boosted [atypical]
	//  - B0: Weather boosts disguise but not Ditto causing seen IV to be unboosted [atypical]
	//  - PP: Weather being partly cloudy boosts both disguise and Ditto
	//
	// We will also use 0N/BN/PN to denote a normal non-Ditto spawn with corresponding weather boosts.
	// Disguise IV depends on Ditto weather boost instead, and caught Ditto is boosted only in PP state.
	if pokemon.IsDitto {
		var unboostedLevel int32
		if boostedScan != nil {
			unboostedLevel = boostedScan.Level - 5
		} else if unboostedScan != nil {
			unboostedLevel = unboostedScan.Level
		} else {
			_, _ = pokemon.resetDittoAttributes("?", nil, nil, scan)
			return scan, errors.New("missing past scans; Ditto will be reset")
		}
		// If IsDitto = true, then the IV sets in history are ALWAYS confirmed
		scan.Confirmed = true
		switch scan.Weather {
		case int32(pogo.GameplayWeatherProto_NONE):
			if scan.CellWeather == int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
				switch scan.Level {
				case unboostedLevel:
					return pokemon.resetDittoAttributes("0N", unboostedScan, boostedScan, scan)
				case unboostedLevel + 5:
					// For a confirmed Ditto, we persist IV in inactive only in 0P state
					// when disguise is boosted, it has same IV as Ditto
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					return unboostedScan, checkScans(boostedScan, scan)
				}
				return scan, fmt.Errorf("unexpected 0P Ditto level change, %s/%s -> %s",
					unboostedScan, boostedScan, scan)
			}
			return scan, checkScans(unboostedScan, scan)
		case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
			return scan, checkScans(boostedScan, scan)
		}
		switch scan.Level {
		case unboostedLevel:
			scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			return scan, checkScans(unboostedScan, scan)
		case unboostedLevel + 5:
			return pokemon.resetDittoAttributes("BN", boostedScan, unboostedScan, scan)
		}
		return scan, fmt.Errorf("unexpected B0 Ditto level change, %s/%s -> %s",
			unboostedScan, boostedScan, scan)
	}

	isBoosted := scan.Weather != int32(pogo.GameplayWeatherProto_NONE)
	var matchingScan *pokemonScan
	if unboostedScan != nil || boostedScan != nil {
		if unboostedScan != nil && boostedScan != nil { // if we have both IVs then they must be correct
			if unboostedScan.Level == scan.Level {
				if isBoosted {
					pokemon.setDittoAttributes(">B0", true, unboostedScan, scan)
					confirmDitto(scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
					return scan, nil
				}
				return scan, checkScans(unboostedScan, scan)
			} else if boostedScan.Level == scan.Level {
				if isBoosted {
					return scan, checkScans(boostedScan, scan)
				}
				pokemon.setDittoAttributes(">0P", true, boostedScan, scan)
				confirmDitto(scan)
				scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				return unboostedScan, nil
			}
			return scan, fmt.Errorf("unexpected third level found %s, %s vs %s",
				unboostedScan, boostedScan, scan)
		}

		levelAdjustment := int32(0)
		if isBoosted {
			if boostedScan != nil {
				matchingScan = boostedScan
			} else {
				matchingScan = unboostedScan
				levelAdjustment = 5
			}
		} else {
			if unboostedScan != nil {
				matchingScan = unboostedScan
			} else {
				matchingScan = boostedScan
				levelAdjustment = -5
			}
		}
		// There are 10 total possible transitions among these states, i.e. all 12 of them except for 0P <-> PP.
		// A Ditto in 00/PP state is undetectable. We try to detect them in the remaining possibilities.
		// Now we try to detect all 10 possible conditions where we could identify Ditto with certainty
		switch scan.Level - (matchingScan.Level + levelAdjustment) {
		case 0:
		// the Pokémon has been encountered before, but we find an unexpected level when reencountering it => Ditto
		// note that at this point the level should have been already readjusted according to the new weather boost
		case 5:
			switch scan.Weather {
			case int32(pogo.GameplayWeatherProto_NONE):
				switch matchingScan.Weather {
				case int32(pogo.GameplayWeatherProto_NONE):
					pokemon.setDittoAttributes("00/0N>0P", true, matchingScan, scan)
					confirmDitto(scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					return unboostedScan, nil
				case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
					if err := checkScans(matchingScan, scan); err != nil {
						return scan, err
					}
					pokemon.setDittoAttributes("PN>0P", true, matchingScan, scan)
					confirmDitto(scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					scan.Confirmed = true
					return unboostedScan, nil
				}
				if err := checkScans(matchingScan, scan); err != nil {
					return scan, err
				}
				if scan.CellWeather != int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
					if scan.MustHaveRerolled(matchingScan) {
						pokemon.setDittoAttributes("B0>00/[0N]", false, matchingScan, scan)
					} else {
						// set Ditto as it is most likely B0>00 if species did not reroll
						pokemon.setDittoAttributes("B0>[00]/0N", true, matchingScan, scan)
					}
					scan.Confirmed = true
				} else if matchingScan.Confirmed || scan.MustBeBoosted() {
					pokemon.setDittoAttributes("BN>0P", true, matchingScan, scan)
					confirmDitto(scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					scan.Confirmed = true
					return unboostedScan, nil
					// scan.MustBeUnboosted() need not be checked since matchingScan would not have been in B0
				} else {
					// in case of BN>0P, we set Ditto to be a hidden 0P state, hoping we rediscover later
					// setting 0P Ditto would also mean that we have a Ditto with unconfirmed IV which is a bad idea
					if _, possible := dittoDisguises.Load(scan.Pokemon); possible {
						if _, possible := dittoDisguises.Load(matchingScan.Pokemon); !possible {
							// this guess is most likely to be correct except when Ditto pool just rerolled
							pokemon.setDittoAttributes("BN>[0P] or B0>0N", true, matchingScan, scan)
							scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
							return unboostedScan, nil
						}
					}
					pokemon.setDittoAttributes("BN>0P or B0>[0N]", false, matchingScan, scan)
				}
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
				// we can never be sure if this is a Ditto or rerolling into non-Ditto
				if scan.MustHaveRerolled(matchingScan) {
					pokemon.setDittoAttributes("B0>PP/[PN]", false, matchingScan, scan)
				} else {
					pokemon.setDittoAttributes("B0>[PP]/PN", true, matchingScan, scan)
				}
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			default:
				pokemon.setDittoAttributes("B0>BN", false, matchingScan, scan)
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			}
			return scan, nil
		case -5:
			switch scan.Weather {
			case int32(pogo.GameplayWeatherProto_NONE):
				// we can never be sure if this is a Ditto or rerolling into non-Ditto
				if scan.MustHaveRerolled(matchingScan) {
					pokemon.setDittoAttributes("0P>00/[0N]", false, matchingScan, scan)
				} else {
					pokemon.setDittoAttributes("0P>[00]/0N", true, matchingScan, scan)
				}
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				return scan, nil
			case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
				pokemon.setDittoAttributes("0P>PN", false, matchingScan, scan)
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				scan.Confirmed = true
				return scan, checkScans(matchingScan, scan)
			}
			if matchingScan.Weather != int32(pogo.GameplayWeatherProto_NONE) {
				pokemon.setDittoAttributes("BN/PP/PN>B0", true, matchingScan, scan)
				confirmDitto(scan)
				scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
				return scan, nil
			}
			if err := checkScans(matchingScan, scan); err != nil {
				return scan, err
			}
			if scan.MustBeBoosted() {
				pokemon.setDittoAttributes("0P>BN", false, matchingScan, scan)
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				scan.Confirmed = true
			} else if matchingScan.Confirmed || // this covers scan.MustBeUnboosted()
				matchingScan.CellWeather != int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
				pokemon.setDittoAttributes("00/0N>B0", true, matchingScan, scan)
				confirmDitto(scan)
				scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
				scan.Confirmed = true
			} else {
				// same rationale as BN>0P or B0>[0N]
				if _, possible := dittoDisguises.Load(scan.Pokemon); possible {
					if _, possible := dittoDisguises.Load(matchingScan.Pokemon); !possible {
						// this guess is most likely to be correct except when Ditto pool just rerolled
						pokemon.setDittoAttributes("0N>[B0] or 0P>BN", true, matchingScan, scan)
						scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
						return scan, nil
					}
				}
				pokemon.setDittoAttributes("0N>B0 or 0P>[BN]", false, matchingScan, scan)
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			}
			return scan, nil
		case 10:
			pokemon.setDittoAttributes("B0>0P", true, matchingScan, scan)
			confirmDitto(scan)
			matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			return matchingScan, nil // unboostedScan is a wrong guess in this case
		case -10:
			pokemon.setDittoAttributes("0P>B0", true, matchingScan, scan)
			confirmDitto(scan)
			matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			return scan, nil
		default:
			return scan, fmt.Errorf("unexpected level %s -> %s", matchingScan, scan)
		}
	}
	if isBoosted {
		if scan.MustBeUnboosted() {
			pokemon.setDittoAttributes("B0", true, matchingScan, scan)
			confirmDitto(scan)
			scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			scan.Confirmed = true
			return scan, checkScans(unboostedScan, scan)
		}
		scan.Confirmed = scan.MustBeBoosted()
		return scan, checkScans(boostedScan, scan)
	} else if scan.MustBeBoosted() {
		pokemon.setDittoAttributes("0P", true, matchingScan, scan)
		confirmDitto(scan)
		scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
		scan.Confirmed = true
		return unboostedScan, checkScans(boostedScan, scan)
	}
	scan.Confirmed = scan.MustBeUnboosted()
	return scan, checkScans(unboostedScan, scan)
}

func (pokemon *Pokemon) clearIv(cp bool) {
	if pokemon.AtkIv.Valid || pokemon.DefIv.Valid || pokemon.StaIv.Valid || pokemon.Iv.Valid {
		pokemon.dirty = true
	}
	pokemon.AtkIv = null.Value[uint8]{}
	pokemon.DefIv = null.Value[uint8]{}
	pokemon.StaIv = null.Value[uint8]{}
	pokemon.SetIv(null.Float{})
	if cp {
		switch pokemon.SeenType.ValueOrZero() {
		case SeenType_LureEncounter:
			pokemon.SetSeenType(SeenType_LureWild)
		case SeenType_Encounter:
			pokemon.SetSeenType(SeenType_Wild)
		}
		pokemon.SetCp(null.NewInt(0, false))
		pokemon.SetPvp(null.NewString("", false))
	}
}

// caller should setPokemonDisplay prior to calling this
func (pokemon *Pokemon) addEncounterPokemon(ctx context.Context, db db.DbDetails, proto *pogo.PokemonProto, username string) {
	pokemon.SetUsername(null.StringFrom(username))
	pokemon.SetShiny(null.BoolFrom(proto.PokemonDisplay.Shiny))
	pokemon.SetCp(null.IntFrom(int64(proto.Cp)))
	pokemon.SetMove1(null.IntFrom(int64(proto.Move1)))
	pokemon.SetMove2(null.IntFrom(int64(proto.Move2)))
	pokemon.SetHeight(null.FloatFrom(float64(proto.HeightM)))
	pokemon.SetSize(null.IntFrom(int64(proto.Size)))
	pokemon.SetWeight(null.FloatFrom(float64(proto.WeightKg)))

	scan := pokemonScan{
		Weather:     int32(pokemon.Weather.ValueOrZero()),
		Strong:      pokemon.IsStrong.ValueOrZero(),
		Attack:      proto.IndividualAttack,
		Defense:     proto.IndividualDefense,
		Stamina:     proto.IndividualStamina,
		CellWeather: int32(pokemon.Weather.ValueOrZero()),
		Pokemon:     int32(proto.PokemonId),
		Costume:     int32(proto.PokemonDisplay.Costume),
		Gender:      int32(proto.PokemonDisplay.Gender),
		Form:        int32(proto.PokemonDisplay.Form),
	}
	if scan.CellWeather == int32(pogo.GameplayWeatherProto_NONE) {
		weather, unlock, err := peekWeatherRecord(weatherCellIdFromLatLon(pokemon.Lat, pokemon.Lon), "addEncounterPokemon")
		if weather == nil || !weather.GameplayCondition.Valid {
			log.Warnf("Failed to obtain weather for Pokemon %d: %s", pokemon.Id, err)
		} else {
			scan.CellWeather = int32(weather.GameplayCondition.Int64)
		}
		if unlock != nil {
			unlock()
		}
	}
	if proto.CpMultiplier < 0.734 {
		scan.Level = int32((58.215688455154954*proto.CpMultiplier-2.7012478057856497)*proto.CpMultiplier + 1.3220677708486794)
	} else if proto.CpMultiplier < .795 {
		scan.Level = int32(171.34093607855277*proto.CpMultiplier - 94.95626666368578)
	} else {
		scan.Level = int32(199.99995231630976*proto.CpMultiplier - 117.55996066890287)
	}

	caughtIv, err := pokemon.detectDitto(&scan)
	if err != nil {
		caughtIv = &scan
		log.Errorf("[POKEMON] Unexpected %d: %s", pokemon.Id, err)
	}
	if caughtIv == nil { // this can only happen for a 0P Ditto
		pokemon.SetLevel(null.IntFrom(int64(scan.Level - 5)))
		pokemon.clearIv(false)
	} else {
		pokemon.SetLevel(null.IntFrom(int64(caughtIv.Level)))
		pokemon.calculateIv(int64(caughtIv.Attack), int64(caughtIv.Defense), int64(caughtIv.Stamina))
	}
	if err == nil {
		newScans := make([]*pokemonScan, len(pokemon.scanHistory)+1)
		entriesCount := 0
		for _, oldEntry := range pokemon.scanHistory {
			if oldEntry.Strong != scan.Strong || !oldEntry.Strong &&
				oldEntry.Weather == int32(pogo.GameplayWeatherProto_NONE) !=
					(scan.Weather == int32(pogo.GameplayWeatherProto_NONE)) {
				newScans[entriesCount] = oldEntry
				entriesCount++
			}
		}
		newScans[entriesCount] = &scan
		pokemon.scanHistory = newScans[:entriesCount+1]
	} else {
		// undo possible changes
		scan.Confirmed = false
		scan.Weather = int32(pokemon.Weather.ValueOrZero())
		pokemon.scanHistory = make([]*pokemonScan, 1)
		pokemon.scanHistory[0] = &scan
	}
}

func (pokemon *Pokemon) updatePokemonFromEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.EncounterOutProto, username string, timestampMs int64) {
	pokemon.SetIsEvent(0)
	pokemon.addWildPokemon(ctx, db, encounterData.Pokemon, timestampMs, false)
	// tappable encounter can also be available in seen as normal encounter once tapped
	if pokemon.isSeenFromTappable() {
		pokemon.SetSeenType(SeenType_Encounter)
	}
	pokemon.addEncounterPokemon(ctx, db, encounterData.Pokemon.Pokemon, username)

	if !pokemon.CellId.Valid {
		centerCoord := s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon)
		cellID := s2.CellIDFromLatLng(centerCoord).Parent(15)
		pokemon.SetCellId(null.IntFrom(int64(cellID)))
	}
}

func (pokemon *Pokemon) isSeenFromTappable() bool {
	return pokemon.SeenType.ValueOrZero() != SeenType_TappableEncounter && pokemon.SeenType.ValueOrZero() != SeenType_TappableLureEncounter
}

func (pokemon *Pokemon) updatePokemonFromDiskEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.DiskEncounterOutProto, username string) {
	pokemon.SetIsEvent(0)
	pokemon.setPokemonDisplay(int16(encounterData.Pokemon.PokemonId), encounterData.Pokemon.PokemonDisplay)
	pokemon.SetSeenType(SeenType_LureEncounter)
	pokemon.addEncounterPokemon(ctx, db, encounterData.Pokemon, username)
}

func (pokemon *Pokemon) updatePokemonFromTappableEncounterProto(ctx context.Context, db db.DbDetails, request *pogo.ProcessTappableProto, encounterData *pogo.TappableEncounterProto, username string, timestampMs int64) {
	pokemon.SetIsEvent(0)
	pokemon.SetLat(request.LocationHintLat)
	pokemon.SetLon(request.LocationHintLng)

	if spawnpointId := request.GetLocation().GetSpawnpointId(); spawnpointId != "" {
		pokemon.SetSeenType(SeenType_TappableEncounter)

		spawnId, err := strconv.ParseInt(spawnpointId, 16, 64)
		if err != nil {
			panic(err)
		}

		pokemon.SetSpawnId(null.IntFrom(spawnId))
		pokemon.setExpireTimestampFromSpawnpoint(ctx, db, timestampMs, false)
	} else if fortId := request.GetLocation().GetFortId(); fortId != "" {
		pokemon.SetSeenType(SeenType_TappableLureEncounter)

		pokemon.SetPokestopId(null.StringFrom(fortId))
		// we don't know any despawn times from lured/fort tappables
		pokemon.SetExpireTimestamp(null.IntFrom(int64(timestampMs)/1000 + int64(120)))
		pokemon.SetExpireTimestampVerified(false)
	}
	if !pokemon.Username.Valid {
		pokemon.SetUsername(null.StringFrom(username))
	}
	pokemon.setPokemonDisplay(int16(encounterData.Pokemon.PokemonId), encounterData.Pokemon.PokemonDisplay)
	pokemon.addEncounterPokemon(ctx, db, encounterData.Pokemon, username)
}

func (pokemon *Pokemon) setPokemonDisplay(pokemonId int16, display *pogo.PokemonDisplayProto) {
	if !pokemon.isNewRecord() {
		// If we would like to support detect A/B spawn in the future, fill in more code here from Chuck
		var oldId int16
		var oldForm null.Value[uint16]
		if pokemon.IsDitto {
			oldId = int16(pokemon.DisplayPokemonId.ValueOrZero())
			oldForm = pokemon.DisplayPokemonForm
		} else {
			oldId = pokemon.PokemonId
			oldForm = pokemon.Form
		}
		// Narrowed-vs-raw-proto comparisons: compare against the stored
		// (already-clamped) value's ValueOrZero() rather than re-clamping the
		// incoming display.* fields here too — clamping is a counted event,
		// and re-clamping the same value for this comparison and again when
		// SetForm/SetCostume/SetGender run below would double-count it.
		formChanged := !oldForm.Valid || int64(oldForm.ValueOrZero()) != int64(display.Form)
		costumeChanged := !pokemon.Costume.Valid || int64(pokemon.Costume.ValueOrZero()) != int64(display.Costume)
		genderChanged := !pokemon.Gender.Valid || int64(pokemon.Gender.ValueOrZero()) != int64(display.Gender)
		if oldId != pokemonId || formChanged ||
			costumeChanged ||
			genderChanged ||
			pokemon.IsStrong.ValueOrZero() != display.IsStrongPokemon {
			log.Debugf("Pokemon %d changed from (%d,%d,%d,%d,%t) to (%d,%d,%d,%d,%t)", pokemon.Id, oldId,
				pokemon.Form.ValueOrZero(), pokemon.Costume.ValueOrZero(), pokemon.Gender.ValueOrZero(),
				pokemon.IsStrong.ValueOrZero(),
				pokemonId, display.Form, display.Costume, display.Gender, display.IsStrongPokemon)
			pokemon.SetWeight(null.NewFloat(0, false))
			pokemon.SetHeight(null.NewFloat(0, false))
			pokemon.SetSize(null.NewInt(0, false))
			pokemon.SetMove1(null.NewInt(0, false))
			pokemon.SetMove2(null.NewInt(0, false))
			pokemon.SetCp(null.NewInt(0, false))
			pokemon.SetShiny(null.NewBool(false, false))
			pokemon.SetIsDitto(false)
			pokemon.SetDisplayPokemonId(null.NewInt(0, false))
			pokemon.SetDisplayPokemonForm(null.NewInt(0, false))
			pokemon.SetPvp(null.NewString("", false))
		}
	}
	if pokemon.isNewRecord() || !pokemon.IsDitto {
		pokemon.SetPokemonId(pokemonId)
	}
	if pokemon.IsDitto {
		pokemon.SetDisplayPokemonForm(null.IntFrom(int64(display.Form)))
		pokemon.SetForm(null.IntFrom(0))
	} else {
		pokemon.SetForm(null.IntFrom(int64(display.Form)))
	}
	pokemon.SetGender(null.IntFrom(int64(display.Gender)))
	pokemon.SetCostume(null.IntFrom(int64(display.Costume)))
	if !pokemon.isNewRecord() {
		pokemon.repopulateIv(int64(display.WeatherBoostedCondition), display.IsStrongPokemon)
	}
	pokemon.SetWeather(null.IntFrom(int64(display.WeatherBoostedCondition)))
	pokemon.SetIsStrong(null.BoolFrom(display.IsStrongPokemon))
}

func (pokemon *Pokemon) repopulateIv(weather int64, isStrong bool) {
	var isBoosted bool
	if !pokemon.IsDitto {
		isBoosted = weather != int64(pogo.GameplayWeatherProto_NONE)
		if isStrong == pokemon.IsStrong.ValueOrZero() &&
			int64(pokemon.Weather.ValueOrZero()) != int64(pogo.GameplayWeatherProto_NONE) == isBoosted {
			return
		}
	} else if isStrong {
		log.Errorf("Strong Ditto??? I can't handle this fml %d", pokemon.Id)
		pokemon.clearIv(true)
		return
	} else {
		isBoosted = weather == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
		// both Ditto and disguise are boosted and Ditto was not boosted: none -> boosted
		// or both Ditto and disguise were boosted and Ditto is not boosted: boosted -> none
		if int64(pokemon.Weather.ValueOrZero()) == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) == isBoosted {
			return
		}
	}
	matchingScan, isBoostedMatches := pokemon.locateScan(isStrong, isBoosted)
	var oldAtk, oldDef, oldSta int64
	if matchingScan == nil {
		pokemon.SetLevel(null.NewInt(0, false))
		pokemon.clearIv(true)
	} else {
		oldLevel := int64(pokemon.Level.ValueOrZero())
		if pokemon.AtkIv.Valid {
			oldAtk = int64(pokemon.AtkIv.ValueOrZero())
			oldDef = int64(pokemon.DefIv.ValueOrZero())
			oldSta = int64(pokemon.StaIv.ValueOrZero())
		} else {
			oldAtk = -1
			oldDef = -1
			oldSta = -1
		}
		newLevel := int64(matchingScan.Level)
		if isBoostedMatches || isStrong { // strong Pokemon IV is unaffected by weather
			pokemon.calculateIv(int64(matchingScan.Attack), int64(matchingScan.Defense), int64(matchingScan.Stamina))
			switch pokemon.SeenType.ValueOrZero() {
			case SeenType_LureWild:
				pokemon.SetSeenType(SeenType_LureEncounter)
			case SeenType_Wild:
				pokemon.SetSeenType(SeenType_Encounter)
			}
		} else {
			pokemon.clearIv(true)
		}
		if !isBoostedMatches {
			if isBoosted {
				newLevel += 5
			} else {
				newLevel -= 5
			}
		}
		pokemon.SetLevel(null.IntFrom(newLevel))
		if newLevel != oldLevel || pokemon.AtkIv.Valid &&
			(int64(pokemon.AtkIv.ValueOrZero()) != oldAtk || int64(pokemon.DefIv.ValueOrZero()) != oldDef || int64(pokemon.StaIv.ValueOrZero()) != oldSta) {
			pokemon.SetCp(null.NewInt(0, false))
			pokemon.SetPvp(null.NewString("", false))
		}
	}
}

func (pokemon *Pokemon) recomputeCpIfNeeded(ctx context.Context, db db.DbDetails, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition) {
	if pokemon.Cp.Valid || ohbem == nil {
		return
	}
	var displayPokemon int
	var displayPokemonForm int
	shouldOverrideIv := false
	var overrideIv *pokemonScan
	if pokemon.IsDitto {
		displayPokemon = int(pokemon.DisplayPokemonId.ValueOrZero())
		displayPokemonForm = int(pokemon.DisplayPokemonForm.ValueOrZero())
		if int64(pokemon.Weather.ValueOrZero()) == int64(pogo.GameplayWeatherProto_NONE) {
			cellId := weatherCellIdFromLatLon(pokemon.Lat, pokemon.Lon)
			cellWeather, found := weather[cellId]
			if !found {
				record, unlock, err := getWeatherRecordReadOnly(ctx, db, cellId, "recomputeCpIfNeeded")
				if err != nil || record == nil || !record.GameplayCondition.Valid {
					log.Warnf("[POKEMON] Failed to obtain weather for Pokemon %d: %s", pokemon.Id, err)
				} else {
					log.Warnf("[POKEMON] Weather not found locally for %d at %d", pokemon.Id, cellId)
					cellWeather = pogo.GameplayWeatherProto_WeatherCondition(record.GameplayCondition.Int64)
					found = true
				}
				if unlock != nil {
					unlock()
				}
			}
			if found && cellWeather == pogo.GameplayWeatherProto_PARTLY_CLOUDY {
				shouldOverrideIv = true
				scan, isBoostedMatches := pokemon.locateScan(false, false)
				if scan != nil && isBoostedMatches {
					overrideIv = scan
				}
			}
		}
	} else {
		displayPokemon = int(pokemon.PokemonId)
		displayPokemonForm = int(pokemon.Form.ValueOrZero())
	}
	var cp int
	var err error
	if shouldOverrideIv {
		if overrideIv == nil {
			return
		}
		// You should see boosted IV for 0P Ditto
		cp, err = ohbem.CalculateCp(displayPokemon, displayPokemonForm, 0,
			int(overrideIv.Attack), int(overrideIv.Defense), int(overrideIv.Stamina), float64(overrideIv.Level))
	} else {
		if !pokemon.AtkIv.Valid || !pokemon.Level.Valid {
			return
		}
		cp, err = ohbem.CalculateCp(displayPokemon, displayPokemonForm, 0,
			int(pokemon.AtkIv.ValueOrZero()), int(pokemon.DefIv.ValueOrZero()), int(pokemon.StaIv.ValueOrZero()),
			float64(pokemon.Level.ValueOrZero()))
	}
	if err == nil {
		pokemon.SetCp(null.IntFrom(int64(cp)))
	} else {
		log.Warnf("Pokemon %d %d CP unset due to error %s", pokemon.Id, displayPokemon, err)
	}
}
