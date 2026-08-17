package decoder

import (
	"fmt"
	"math"

	"github.com/guregu/null/v6"
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
	Id      Uint64Str         `db:"id"`
	SpawnId null.Value[int64] `db:"spawn_id"`
	CellId  null.Value[int64] `db:"cell_id"`
	Lat     float64           `db:"lat"`
	Lon     float64           `db:"lon"`

	// --- 4-byte aligned ---
	FirstSeenTimestamp uint32              `db:"first_seen_timestamp"`
	Changed            uint32              `db:"changed"`
	ExpireTimestamp    null.Value[uint32]  `db:"expire_timestamp"`
	Updated            null.Value[uint32]  `db:"updated"`
	Weight             null.Value[float32] `db:"weight"`
	Height             null.Value[float32] `db:"height"`
	Iv                 null.Value[float32] `db:"iv"`

	// --- 2-byte aligned ---
	PokemonId          int16              `db:"pokemon_id"`
	Move1              null.Value[uint16] `db:"move_1"`
	Move2              null.Value[uint16] `db:"move_2"`
	Cp                 null.Value[uint16] `db:"cp"`
	Form               null.Value[uint16] `db:"form"`
	DisplayPokemonId   null.Value[uint16] `db:"display_pokemon_id"`
	DisplayPokemonForm null.Value[uint16] `db:"display_pokemon_form"`

	// --- 1-byte ---
	Gender                  null.Value[uint8] `db:"gender"`
	AtkIv                   null.Value[uint8] `db:"atk_iv"`
	DefIv                   null.Value[uint8] `db:"def_iv"`
	StaIv                   null.Value[uint8] `db:"sta_iv"`
	Level                   null.Value[uint8] `db:"level"`
	Weather                 null.Value[uint8] `db:"weather"`
	Costume                 null.Value[uint8] `db:"costume"`
	Size                    null.Value[uint8] `db:"size"`
	IsStrong                null.Value[bool]  `db:"strong"`
	Shiny                   null.Value[bool]  `db:"shiny"`
	ExpireTimestampVerified bool              `db:"expire_timestamp_verified"`
	IsDitto                 bool              `db:"is_ditto"`
	IsEvent                 int8              `db:"is_event"`
	SeenType                NullSeenType      `db:"seen_type"`

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

	// scanHistory is memory-only Ditto-detection state, hydrated from the
	// golbat_internal column on demand (see populateInternal) and converted
	// back to protobuf only when writing that column. Elements stay behind
	// pointers because the Ditto code holds one across other work and
	// mutates through it.
	scanHistory []*pokemonScan `db:"-"`

	dirty     bool `db:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-"`

	// debug accumulates per-field change descriptions for dbDebugLog, one
	// aggregated log line per save (see pokemon_state.go). Its type is
	// debugChangeAccumulator, defined once per build tag in db_debug.go /
	// db_debug_off.go: a real `[]string`-backed accumulator when built with
	// -tags dbdebug, and a zero-sized stub otherwise — so production builds
	// carry no bytes for it, unlike the [24]byte slice header this field
	// used to be unconditionally. It is placed here, before oldValues, not
	// last: a zero-sized field placed last forces Go to add a word of
	// padding to keep a past-the-end pointer valid, which would cancel this
	// saving.
	debug debugChangeAccumulator `db:"-"`

	oldValues PokemonOldValues `db:"-"` // Old values for webhook comparison and stats
}

// PokemonOldValues holds old field values for webhook comparison, stats, and R-tree updates
type PokemonOldValues struct {
	PokemonId int16
	Weather   null.Value[uint8]
	Cp        null.Value[uint16]
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

// clampUint narrows a null.Int for storage in a tinyint/smallint/int
// unsigned-backed field, saturating into [0, limit] over the shared
// saturate. clampUint8/16/32 below are its only callers — thin
// instantiations that fix T and limit together, so every setter still names
// its own width at the call site instead of spelling out a type parameter.
//
// Values arrive from decoded game protos and are bounded in practice, so
// out-of-range means the protocol changed rather than a normal case. Clamping
// keeps the value at the boundary and counts the event; truncating would
// silently produce a plausible-looking wrong number, which is worse.
//
// Comparisons against an already-stored value must not come through here:
// use the non-counting narrowUint8/16/32 instead, and see their doc comment
// for why the two exist separately.
//
// golbat_field_clamped_total does not mean quite the same thing for every
// field, which matters before comparing two of its label values. The
// setters clamp above their own equality check, so every field they own
// (form, costume, gender, weather, cp, level, size, move_1, move_2,
// expire_timestamp, updated, display_pokemon_id, display_pokemon_form)
// counts once per setter call — a repeat store of an unchanged out-of-range
// value counts again, so the rate tracks sightings. calculateIv, the only
// caller that is not a setter, clamps below its comparison, so atk_iv,
// def_iv and sta_iv count once per value that actually reaches the record:
// five identical out-of-range encounters on one pokemon count one, five
// distinct pokemon count five. Both are honest per-store rates. They are
// just not the same rate.
//
// Note this is the opposite policy to null.Value[T]'s Scan (via sql.Null[T]),
// which rejects out-of-range values outright. That is deliberate: a bad value
// from our own database is a bug worth failing on, a bad value from a game
// server is a fact worth recording.
func clampUint[T ~uint8 | ~uint16 | ~uint32](v null.Int, field string, limit int64) null.Value[T] {
	if !v.Valid {
		return null.Value[T]{}
	}
	i := saturate(v.Int64, limit)
	if i != v.Int64 {
		getStatsCollector().IncFieldClamped(field)
	}
	return null.ValueFrom(T(i))
}

func clampUint8(v null.Int, field string) null.Value[uint8] {
	return clampUint[uint8](v, field, math.MaxUint8)
}

func clampUint16(v null.Int, field string) null.Value[uint16] {
	return clampUint[uint16](v, field, math.MaxUint16)
}

func clampUint32(v null.Int, field string) null.Value[uint32] {
	return clampUint[uint32](v, field, math.MaxUint32)
}

// narrowUint8, narrowUint16 and narrowUint32 saturate an int64 into the
// range of a tinyint / smallint / int unsigned column and count nothing.
// They are the comparison-side twins of clampUint8/16/32, which perform the
// identical saturation on the write side and do count it.
//
// The split exists because storing a value and comparing against a stored
// value are different events. Storage narrows: a costume of 300 lands in
// the tinyint as 255. Comparing that stored 255 against the raw proto's 300
// is then unequal forever — the two sides can never converge — and the
// branches these comparisons guard ("has this pokemon's display changed?")
// wipe the encounter's IVs, CP and PVP data on every single sighting.
// Narrowing the raw side first makes both sides 255, so the comparison
// settles exactly as it did when these fields still stored a raw int64.
//
// So: narrow wherever a stored field meets a value that has not been
// through a setter yet, clamp where the value is actually stored. Do not
// collapse the two back into one helper. Routing comparisons through the
// counting clamp would inflate golbat_field_clamped_total to one clamp per
// sighting instead of one per store (which is why they were written that
// way originally); routing stores through the non-counting narrow would
// silence the metric altogether.
func narrowUint8(v int64) int64 { return saturate(v, math.MaxUint8) }

func narrowUint16(v int64) int64 { return saturate(v, math.MaxUint16) }

func narrowUint32(v int64) int64 { return saturate(v, math.MaxUint32) }

// saturate clamps v into [0, limit]: the shared body of narrowUint8/16/32
// and, through them, of clampUint8/16/32 — one definition of where each
// column's range ends.
func saturate(v, limit int64) int64 {
	switch {
	case v < 0:
		return 0
	case v > limit:
		return limit
	default:
		return v
	}
}

// clampFloat32 narrows a null.Float. Range is not checked: the values are
// weight, height and iv, all far inside float32's range.
func clampFloat32(v null.Float) null.Value[float32] {
	if !v.Valid {
		return null.Value[float32]{}
	}
	return null.ValueFrom(float32(v.Float64))
}

// nullBoolFrom converts a guregu/null.Bool into a null.Value[bool].
func nullBoolFrom(v null.Bool) null.Value[bool] {
	if !v.Valid {
		return null.Value[bool]{}
	}
	return null.ValueFrom(v.Bool)
}

// nullIntFromUint is the reverse of clampUint8/16/32: it widens a narrowed
// null.Value[T] back into a guregu/null.Int. Used at call sites that still
// speak null.Int — a setter fed by another narrowed field (e.g.
// SetForm(nullIntFromUint(pokemon.DisplayPokemonForm))) and the stats
// collector interface (UpdateVerifiedTtl), which is shared with non-pokemon
// callers and speaks guregu/null throughout.
//
// The webhook payload used to widen through here too, to keep webhook bytes
// identical to before the struct-packing PR. The maintainer judged that
// requirement misattributed, so PokemonWebhook's IV/stat fields now hold
// the narrowed types directly — see the doc comment on PokemonWebhook.
func nullIntFromUint[T ~uint8 | ~uint16 | ~uint32](n null.Value[T]) null.Int {
	if !n.Valid {
		return null.Int{}
	}
	return null.IntFrom(int64(n.V))
}

// int64OrZero widens a narrowed null.Value[T] straight to int64, 0 if
// invalid — the single-term form of the ~45 int64(x.ValueOrZero()) casts
// scattered across comparisons and assignments in this package. It exists
// only to replace a standalone cast; it must never appear inside an
// arithmetic expression.
//
// The reason is the exact shape of a real bug: int64(a.ValueOrZero()) -
// int64(b.ValueOrZero()) is correct (each operand is cast to the signed
// width before the subtraction runs), but
// int64(a.ValueOrZero() - b.ValueOrZero()) wraps whenever b > a, because
// the subtraction then happens in T's unsigned width first and only the
// (already-wrapped) result gets widened. Taking null.Value[T] here rather
// than a bare T closes off the obvious way to write that second form by
// accident with this helper — there is no raw unsigned value in scope to
// subtract before the cast — but it does not make the helper safe to place
// next to a `+` or `-` regardless: call it once per operand into a local,
// then do the arithmetic on the resulting int64s. A handful of sites that
// already did that correctly with explicit int64(...) casts (the tth
// calculations in stats.go, remainingDuration/encounterStatsDuration's
// `60 + ... - now`) were deliberately left on the explicit form rather than
// converted, so nothing here ever sits one edit away from the wrapped form.
func int64OrZero[T ~uint8 | ~uint16 | ~uint32](n null.Value[T]) int64 {
	if !n.Valid {
		return 0
	}
	return int64(n.V)
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

// SetSpawnId stores the value directly; no clamping applies.
//
// SpawnId's Go type is null.Value[int64], not [uint64], even though
// spawn_id is bigint unsigned. sql.Null[uint64]'s Value() (via
// driver.DefaultParameterConverter) refuses to write any value with the
// high bit set ("uint64 values with high bit set are not supported"), so a
// [uint64] field can Scan the column's full unsigned range but cannot
// always persist it back. [int64] has no such restriction and this
// column's real values are ~48-bit, far below the boundary either type
// would need to worry about — so storing v.Int64 as-is is both simpler and
// strictly more capable than a [uint64] field would be.
func (pokemon *Pokemon) SetSpawnId(v null.Int) {
	var next null.Value[int64]
	if v.Valid {
		next = null.ValueFrom(v.Int64)
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

// SetSeenType takes the code directly — the decode path passes one of the
// SeenTypeCode* constants, so a typo'd name fails to compile instead of
// silently no-opping the way a runtime string-to-code lookup would. This
// also drops the ParseSeenType map lookup from the decode hot path: turning
// a known-valid code into a NullSeenType is a direct assignment.
func (pokemon *Pokemon) SetSeenType(c SeenTypeCode) {
	next := SeenTypeFrom(c)
	if pokemon.SeenType != next {
		if dbDebugEnabled {
			pokemon.debug.recordChange(
				fmt.Sprintf("SeenType:%s->%s", FormatNull(pokemon.SeenType), FormatNull(next)))
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

// SetCellId stores the signed S2 cell id directly. cell_id is a signed
// bigint whose real values are frequently negative, and CellId's type
// (null.Value[int64]) matches the column exactly, so v.Int64 is stored as-is
// with no clamping or bit-reinterpretation.
func (pokemon *Pokemon) SetCellId(v null.Int) {
	var next null.Value[int64]
	if v.Valid {
		next = null.ValueFrom(v.Int64)
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
