package decoder

import (
	"fmt"
	"math"

	"golbat/decoder/nulltypes"
	"golbat/grpc"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"
)

// PokemonData contains all database-persisted fields for Pokemon.
// This struct is embedded in Pokemon and can be safely copied for write-behind
// queueing.
//
// FIELD ORDER IS LOAD-BEARING in the sense that a careless ordering
// (interleaving fields of different alignments) can make this bigger than
// 256 bytes — but not smaller. The field payload sums to 251 bytes (8-byte
// group 56 + 4-byte group 48 + 2-byte group 26 + 1-byte group 25 + pointer
// group 96), and Go's struct alignment (8, driven by the uint64/float64/
// pointer fields) rounds any total up to the next multiple of 8: 256 either
// way. This order achieves that minimum — the pointer group's own 8-byte
// alignment forces exactly 5 bytes of mandatory padding immediately before
// it (offset 155->160), and every other ordering pays those same 5 bytes
// somewhere else instead (e.g. as trailing padding at the very end), not
// zero. See TestPokemonEntitySizes's comment for the full breakdown and the
// history behind this number (it dropped from 280 when task 5 narrowed
// SeenType out of the pointer group); that test guards the 256 result — if
// it fails after you add a field, read its doc comment before touching the
// constant.
//
// Types are narrowed to the actual column widths. Verify any change against
// sql/*.up.sql. The schema comment further down this file documents three
// known divergences from the original CREATE TABLE — it's a pointer to the
// relevant migrations, not a substitute for checking sql/*.up.sql directly.
type PokemonData struct {
	// --- 8-byte aligned ---
	Id      Uint64Str            `db:"id"`
	SpawnId nulltypes.NullUint64 `db:"spawn_id"`
	CellId  nulltypes.NullUint64 `db:"cell_id"`
	Lat     float64              `db:"lat"`
	Lon     float64              `db:"lon"`

	// --- 4-byte aligned ---
	FirstSeenTimestamp uint32                `db:"first_seen_timestamp"`
	Changed            uint32                `db:"changed"`
	ExpireTimestamp    nulltypes.NullUint32  `db:"expire_timestamp"`
	Updated            nulltypes.NullUint32  `db:"updated"`
	Weight             nulltypes.NullFloat32 `db:"weight"`
	Height             nulltypes.NullFloat32 `db:"height"`
	Iv                 nulltypes.NullFloat32 `db:"iv"`

	// --- 2-byte aligned ---
	PokemonId          int16                `db:"pokemon_id"`
	Move1              nulltypes.NullUint16 `db:"move_1"`
	Move2              nulltypes.NullUint16 `db:"move_2"`
	Cp                 nulltypes.NullUint16 `db:"cp"`
	Form               nulltypes.NullUint16 `db:"form"`
	DisplayPokemonId   nulltypes.NullUint16 `db:"display_pokemon_id"`
	DisplayPokemonForm nulltypes.NullUint16 `db:"display_pokemon_form"`

	// --- 1-byte ---
	Gender                  nulltypes.NullUint8 `db:"gender"`
	AtkIv                   nulltypes.NullUint8 `db:"atk_iv"`
	DefIv                   nulltypes.NullUint8 `db:"def_iv"`
	StaIv                   nulltypes.NullUint8 `db:"sta_iv"`
	Level                   nulltypes.NullUint8 `db:"level"`
	Weather                 nulltypes.NullUint8 `db:"weather"`
	Costume                 nulltypes.NullUint8 `db:"costume"`
	Size                    nulltypes.NullUint8 `db:"size"`
	IsStrong                nulltypes.NullBool  `db:"strong"`
	Shiny                   nulltypes.NullBool  `db:"shiny"`
	ExpireTimestampVerified bool                `db:"expire_timestamp_verified"`
	IsDitto                 bool                `db:"is_ditto"`
	IsEvent                 int8                `db:"is_event"`
	SeenType                NullSeenType        `db:"seen_type"`

	// --- pointer-carrying, last ---
	PokestopId     null.String `db:"pokestop_id"`
	Username       null.String `db:"username"`
	Pvp            null.String `db:"pvp"`
	GolbatInternal []byte      `db:"golbat_internal"`
}

// Pokemon struct.
//
// AtkIv/DefIv/StaIv: Should not be set directly. Use calculateIv
//
// GolbatInternal: internal data not exposed to frontend/users
//
// FirstSeenTimestamp: This field is used in IsNewRecord. It should only be set in savePokemonRecord.
type Pokemon struct {
	mu TrackedMutex[uint64] `db:"-"` // Object-level mutex with contention tracking

	PokemonData // Embedded data fields - can be copied for write-behind queue

	internal grpc.PokemonInternal `db:"-"` // Memory-only internal state

	dirty     bool `db:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-"`

	// debug accumulates per-field change descriptions for dbDebugLog, one
	// aggregated log line per save (see pokemon_state.go). Its type is
	// pokemonDebugState, defined once per build tag in db_debug.go /
	// db_debug_off.go: a real `[]string`-backed accumulator when built with
	// -tags dbdebug, and a zero-sized stub otherwise — so production builds
	// carry no bytes for it, unlike the [24]byte slice header this field
	// used to be unconditionally.
	debug pokemonDebugState `db:"-"`

	oldValues PokemonOldValues `db:"-"` // Old values for webhook comparison and stats
}

// PokemonOldValues holds old field values for webhook comparison, stats, and R-tree updates
type PokemonOldValues struct {
	PokemonId int16
	Weather   nulltypes.NullUint8
	Cp        nulltypes.NullUint16
	SeenType  NullSeenType
	Lat       float64
	Lon       float64
}

// The pokemon table's current shape is the composition of sql/1_rdmdb_tables.up.sql
// and every migration after it. Notable divergences from the original CREATE TABLE
// that used to be reproduced here:
//   - iv is a plain nullable float(5,2), not a generated column (sql/11_ivchanges.up.sql)
//   - seen_type is an eight-value enum (sql/45_tappables_seen_type_lure.up.sql)
//   - size is tinyint unsigned; the original double column was renamed to height
//     (sql/7_add_height_size.up.sql)
// Check sql/*.up.sql before relying on a column's type.

// IsDirty returns true if any field has been modified
func (pokemon *Pokemon) IsDirty() bool {
	return pokemon.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (pokemon *Pokemon) ClearDirty() {
	pokemon.dirty = false
}

// snapshotOldValues saves current values for webhook comparison, stats, and R-tree updates
// Call this after loading from cache/DB but before modifications
func (pokemon *Pokemon) snapshotOldValues() {
	pokemon.oldValues = PokemonOldValues{
		PokemonId: pokemon.PokemonId,
		Weather:   pokemon.Weather,
		Cp:        pokemon.Cp,
		SeenType:  pokemon.SeenType,
		Lat:       pokemon.Lat,
		Lon:       pokemon.Lon,
	}
}

// Lock acquires the Pokemon's mutex with caller tracking
func (pokemon *Pokemon) Lock(caller string) {
	pokemon.mu.Lock(caller, "Pokemon", uint64(pokemon.Id))
}

// Unlock releases the Pokemon's mutex
func (pokemon *Pokemon) Unlock() {
	pokemon.mu.Unlock("Pokemon", uint64(pokemon.Id))
}

// clampUint8 narrows a null.Int for storage in a tinyint-backed field.
//
// Values arrive from decoded game protos and are bounded in practice, so
// out-of-range means the protocol changed rather than a normal case. Clamping
// keeps the value at the boundary and counts the event; truncating would
// silently produce a plausible-looking wrong number, which is worse.
//
// Note this is the opposite policy to nulltypes' Scan, which rejects
// out-of-range values outright. That is deliberate: a bad value from our own
// database is a bug worth failing on, a bad value from a game server is a fact
// worth recording.
func clampUint8(v null.Int, field string) nulltypes.NullUint8 {
	if !v.Valid {
		return nulltypes.NullUint8{}
	}
	i := v.Int64
	switch {
	case i < 0:
		getStatsCollector().IncFieldClamped(field)
		i = 0
	case i > math.MaxUint8:
		getStatsCollector().IncFieldClamped(field)
		i = math.MaxUint8
	}
	return nulltypes.Uint8From(uint8(i))
}

func clampUint16(v null.Int, field string) nulltypes.NullUint16 {
	if !v.Valid {
		return nulltypes.NullUint16{}
	}
	i := v.Int64
	switch {
	case i < 0:
		getStatsCollector().IncFieldClamped(field)
		i = 0
	case i > math.MaxUint16:
		getStatsCollector().IncFieldClamped(field)
		i = math.MaxUint16
	}
	return nulltypes.Uint16From(uint16(i))
}

func clampUint32(v null.Int, field string) nulltypes.NullUint32 {
	if !v.Valid {
		return nulltypes.NullUint32{}
	}
	i := v.Int64
	switch {
	case i < 0:
		getStatsCollector().IncFieldClamped(field)
		i = 0
	case i > math.MaxUint32:
		getStatsCollector().IncFieldClamped(field)
		i = math.MaxUint32
	}
	return nulltypes.Uint32From(uint32(i))
}

// clampFloat32 narrows a null.Float. Range is not checked: the values are
// weight, height and iv, all far inside float32's range.
func clampFloat32(v null.Float) nulltypes.NullFloat32 {
	if !v.Valid {
		return nulltypes.NullFloat32{}
	}
	return nulltypes.Float32From(float32(v.Float64))
}

// nullBoolFrom converts a guregu/null.Bool into a nulltypes.NullBool.
func nullBoolFrom(v null.Bool) nulltypes.NullBool {
	if !v.Valid {
		return nulltypes.NullBool{}
	}
	return nulltypes.BoolFrom(v.Bool)
}

// nullIntFromUint is the reverse of clampUint8/16/32: it widens a narrowed
// nulltypes.NullUint[T] back into a guregu/null.Int. Used at call sites that
// still speak null.Int — a setter fed by another narrowed field (e.g.
// SetForm(nullIntFromUint(pokemon.DisplayPokemonForm))) and the webhook
// payload, whose null.Int fields are a public contract external consumers
// already depend on.
func nullIntFromUint[T ~uint8 | ~uint16 | ~uint32](n nulltypes.NullUint[T]) null.Int {
	if !n.Valid {
		return null.Int{}
	}
	return null.IntFrom(int64(n.V))
}

// nullFloatFromFloat32 widens a nulltypes.NullFloat32 back into a
// guregu/null.Float, for the same reason as nullIntFromUint.
func nullFloatFromFloat32(n nulltypes.NullFloat32) null.Float {
	if !n.Valid {
		return null.Float{}
	}
	return null.FloatFrom(float64(n.V))
}

// nullBoolFromNulltypes is nullBoolFrom's reverse: nulltypes.NullBool back
// into a guregu/null.Bool, for the webhook payload.
func nullBoolFromNulltypes(n nulltypes.NullBool) null.Bool {
	if !n.Valid {
		return null.Bool{}
	}
	return null.BoolFrom(n.V)
}

// --- Set methods with dirty tracking ---

func (pokemon *Pokemon) SetPokestopId(v null.String) {
	if pokemon.PokestopId != v {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("PokestopId:%s->%s", FormatNull(pokemon.PokestopId), FormatNull(v)))
		}
		pokemon.PokestopId = v
		pokemon.dirty = true
	}
}

// SetSpawnId stores the full 64-bit unsigned range; spawn_id is bigint
// unsigned and legitimate values use it all, so no clamping applies.
func (pokemon *Pokemon) SetSpawnId(v null.Int) {
	var next nulltypes.NullUint64
	if v.Valid {
		next = nulltypes.Uint64From(uint64(v.Int64))
	}
	if pokemon.SpawnId != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("SpawnId:%s->%s", FormatNull(pokemon.SpawnId), FormatNull(next)))
		}
		pokemon.SpawnId = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetLat(v float64) {
	if !floatAlmostEqual(pokemon.Lat, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Lat:%f->%f", pokemon.Lat, v))
		}
		pokemon.Lat = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetLon(v float64) {
	if !floatAlmostEqual(pokemon.Lon, v, floatTolerance) {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Lon:%f->%f", pokemon.Lon, v))
		}
		pokemon.Lon = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetPokemonId(v int16) {
	if pokemon.PokemonId != v {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("PokemonId:%d->%d", pokemon.PokemonId, v))
		}
		pokemon.PokemonId = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetForm(v null.Int) {
	next := clampUint16(v, "form")
	if pokemon.Form != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Form:%s->%s", FormatNull(pokemon.Form), FormatNull(next)))
		}
		pokemon.Form = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCostume(v null.Int) {
	next := clampUint8(v, "costume")
	if pokemon.Costume != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Costume:%s->%s", FormatNull(pokemon.Costume), FormatNull(next)))
		}
		pokemon.Costume = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetGender(v null.Int) {
	next := clampUint8(v, "gender")
	if pokemon.Gender != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(
				fmt.Sprintf("Gender:%s->%s", FormatNull(pokemon.Gender), FormatNull(next)))
		}
		pokemon.Gender = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetWeather(v null.Int) {
	next := clampUint8(v, "weather")
	if pokemon.Weather != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Weather:%s->%s", FormatNull(pokemon.Weather), FormatNull(next)))
		}
		pokemon.Weather = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetIsStrong(v null.Bool) {
	next := nullBoolFrom(v)
	if pokemon.IsStrong != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("IsStrong:%s->%s", FormatNull(pokemon.IsStrong), FormatNull(next)))
		}
		pokemon.IsStrong = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetExpireTimestamp(v null.Int) {
	next := clampUint32(v, "expire_timestamp")
	if pokemon.ExpireTimestamp != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("ExpireTimestamp:%s->%s", FormatNull(pokemon.ExpireTimestamp), FormatNull(next)))
		}
		pokemon.ExpireTimestamp = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetExpireTimestampVerified(v bool) {
	if pokemon.ExpireTimestampVerified != v {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("ExpireTimestampVerified:%t->%t", pokemon.ExpireTimestampVerified, v))
		}
		pokemon.ExpireTimestampVerified = v
		pokemon.dirty = true
	}
}

// SetSeenType takes the string form directly, since the decode path already
// produces the SeenType_* constants. An unrecognised value means the game
// added a seen type before the migrations caught up; it is logged and the
// field is left unchanged rather than corrupting scan statistics with a
// wrong code.
func (pokemon *Pokemon) SetSeenType(s string) {
	next, err := ParseSeenType(s)
	if err != nil {
		log.Warnf("SetSeenType(%d): %s", pokemon.Id, err)
		return
	}
	if pokemon.SeenType != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(
				fmt.Sprintf("SeenType:%s->%s", pokemon.SeenType.ValueOrZero(), next.ValueOrZero()))
		}
		pokemon.SeenType = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetUsername(v null.String) {
	if pokemon.Username != v {
		pokemon.Username = v
		//pokemon.dirty = true
	}
}

// SetCellId stores the raw bit pattern of a wrapped S2 cell id. cell_id is a
// signed bigint whose real values are frequently negative; converting via
// uint64(v.Int64) reinterprets the same bits rather than clamping, so the
// round trip through storage is lossless in both directions.
func (pokemon *Pokemon) SetCellId(v null.Int) {
	var next nulltypes.NullUint64
	if v.Valid {
		next = nulltypes.Uint64From(uint64(v.Int64))
	}
	if pokemon.CellId != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("CellId:%s->%s", FormatNull(pokemon.CellId), FormatNull(next)))
		}
		pokemon.CellId = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetIsEvent(v int8) {
	if pokemon.IsEvent != v {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("IsEvent:%d->%d", pokemon.IsEvent, v))
		}
		pokemon.IsEvent = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetShiny(v null.Bool) {
	next := nullBoolFrom(v)
	if pokemon.Shiny != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Shiny:%s->%s", FormatNull(pokemon.Shiny), FormatNull(next)))
		}
		pokemon.Shiny = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetCp(v null.Int) {
	next := clampUint16(v, "cp")
	if pokemon.Cp != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Cp:%s->%s", FormatNull(pokemon.Cp), FormatNull(next)))
		}
		pokemon.Cp = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetLevel(v null.Int) {
	next := clampUint8(v, "level")
	if pokemon.Level != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Level:%s->%s", FormatNull(pokemon.Level), FormatNull(next)))
		}
		pokemon.Level = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetMove1(v null.Int) {
	next := clampUint16(v, "move_1")
	if pokemon.Move1 != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Move1:%s->%s", FormatNull(pokemon.Move1), FormatNull(next)))
		}
		pokemon.Move1 = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetMove2(v null.Int) {
	next := clampUint16(v, "move_2")
	if pokemon.Move2 != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Move2:%s->%s", FormatNull(pokemon.Move2), FormatNull(next)))
		}
		pokemon.Move2 = next
		pokemon.dirty = true
	}
}

// SetHeight compares the clamped float32 values for exact equality rather
// than the null.Float tolerance comparison the pre-narrowing setter used
// (nullFloatAlmostEqual, floatTolerance=1e-6). That tolerance existed to
// absorb float64 promotion jitter on values not yet narrowed; here both the
// stored and incoming value are already float32 by the time they're
// compared, so sub-float32-precision jitter has already been collapsed by
// clampFloat32 itself, and two "same" readings land on the identical float32
// bit pattern. Dropping the tolerance was a deliberate simplification, not
// an oversight — flag it for review if a case turns up where it isn't.
func (pokemon *Pokemon) SetHeight(v null.Float) {
	next := clampFloat32(v)
	if pokemon.Height != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Height:%s->%s", FormatNull(pokemon.Height), FormatNull(next)))
		}
		pokemon.Height = next
		pokemon.dirty = true
	}
}

// SetWeight: see SetHeight's doc comment — same deliberate drop of the
// tolerance comparison, same reasoning.
func (pokemon *Pokemon) SetWeight(v null.Float) {
	next := clampFloat32(v)
	if pokemon.Weight != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Weight:%s->%s", FormatNull(pokemon.Weight), FormatNull(next)))
		}
		pokemon.Weight = next
		pokemon.dirty = true
	}
}

// SetIv sets the overall IV percentage. Unlike the other narrowed setters,
// callers previously wrote pokemon.Iv directly (see calculateIv/clearIv in
// pokemon_decode.go); routing it through a setter here closes that gap so it
// gets clamping and dirty tracking like everything else.
func (pokemon *Pokemon) SetIv(v null.Float) {
	next := clampFloat32(v)
	if pokemon.Iv != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Iv:%s->%s", FormatNull(pokemon.Iv), FormatNull(next)))
		}
		pokemon.Iv = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetSize(v null.Int) {
	next := clampUint8(v, "size")
	if pokemon.Size != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Size:%s->%s", FormatNull(pokemon.Size), FormatNull(next)))
		}
		pokemon.Size = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetIsDitto(v bool) {
	if pokemon.IsDitto != v {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("IsDitto:%t->%t", pokemon.IsDitto, v))
		}
		pokemon.IsDitto = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetDisplayPokemonId(v null.Int) {
	next := clampUint16(v, "display_pokemon_id")
	if pokemon.DisplayPokemonId != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("DisplayPokemonId:%s->%s", FormatNull(pokemon.DisplayPokemonId), FormatNull(next)))
		}
		pokemon.DisplayPokemonId = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetDisplayPokemonForm(v null.Int) {
	next := clampUint16(v, "display_pokemon_form")
	if pokemon.DisplayPokemonForm != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("DisplayPokemonForm:%s->%s", FormatNull(pokemon.DisplayPokemonForm), FormatNull(next)))
		}
		pokemon.DisplayPokemonForm = next
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetPvp(v null.String) {
	if pokemon.Pvp != v {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Pvp:%s->%s", FormatNull(pokemon.Pvp), FormatNull(v)))
		}
		pokemon.Pvp = v
		pokemon.dirty = true
	}
}

func (pokemon *Pokemon) SetUpdated(v null.Int) {
	next := clampUint32(v, "updated")
	if pokemon.Updated != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Updated:%s->%s", FormatNull(pokemon.Updated), FormatNull(next)))
		}
		pokemon.Updated = next
		pokemon.dirty = true
	}
}

// SetChanged stores a Unix timestamp. changed is an unsigned int column;
// timestamps fit uint32 until year 2106, so this converts without clamping
// (same reasoning as SetSpawnId/SetCellId).
func (pokemon *Pokemon) SetChanged(v int64) {
	next := uint32(v)
	if pokemon.Changed != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(fmt.Sprintf("Changed:%d->%d", pokemon.Changed, next))
		}
		pokemon.Changed = next
		pokemon.dirty = true
	}
}
