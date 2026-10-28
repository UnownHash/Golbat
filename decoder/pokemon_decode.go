package decoder

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"golbat/config"
	"golbat/db"
	"golbat/derive/despawn"
	"golbat/derive/encounter"
	"golbat/geo"
	"golbat/grpc"
	"golbat/pogo"
	"golbat/util"

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

	// Restore how this record's Ditto identity was reached. Without it a
	// reloaded roll-marked record keeps IsDitto but looks like a
	// detectDitto-confirmed one with no history — the shape detectDitto's
	// missing-history branch resets, discarding a prediction nothing had
	// contradicted. Only ever present when pokemon_internal_to_db wrote it;
	// a deployment that leaves that off loses the distinction on reload,
	// which is tolerable because a pokemon lives at most an hour.
	//
	// Conditional on IsDitto, because the serialised bytes outlive the live
	// flag. SetIsDitto(false) clears dittoRollMarked, but it does not rewrite
	// GolbatInternal, and an empty scanHistory still passes the guard above —
	// so a display change that cleared the mark would have it restored by the
	// next hydration, and a later independent confirmation would then be
	// recorded as roll_only. The invariant this field documents is that it is
	// true only while IsDitto is, which makes IsDitto the authority here.
	if internal.GetDittoRollMarked() && pokemon.IsDitto {
		pokemon.dittoRollMarked = true
	}
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

// hadCrossBoostScanHistory reports whether this record's scan history
// (hydrated from GolbatInternal if needed) currently holds both an unboosted
// and a boosted non-strong scan — the condition under which detectDitto's
// "unboostedScan != nil && boostedScan != nil" branch decides Ditto-ness from
// evidence rather than guessing.
//
// It feeds the ditto_detect log field (derivationLogPayload.DittoDetect),
// which records whether detectDitto's "not a Ditto" conclusion was backed by
// that evidence. It must be called where addEncounterPokemon takes its other
// pre-detectDitto snapshots (observedScan, wasRollMarked), not from
// checkDerivation/logDerivationOutcome: by then this encounter's own scan is
// in scanHistory, and a record with only one boost state in its prior
// history would be reported as having cross-boost evidence whenever this
// encounter landed in the other state — the false "rejected" that
// ditto_detect's no_evidence state exists to rule out. Pinned by
// TestLogDerivationOutcomeDittoDetectNoEvidenceWithSingleBoostStateHistory.
func (pokemon *Pokemon) hadCrossBoostScanHistory() bool {
	unboosted, boosted, _ := pokemon.locateAllScans()
	return unboosted != nil && boosted != nil
}

func (pokemon *Pokemon) isNewRecord() bool {
	return pokemon.newRecord
}

const (
	// pokemonUnverifiedTTL (+ jitter) is used instead of the flat
	// cache-default TTL for pokemon whose expiry is a guess (see
	// hasSpawnpointExpiry). The jitter spreads
	// restart-synchronized cohorts (preload + cold-cache ingest all
	// stamped in the same few minutes) so their expiry an hour later
	// arrives as a stream of tree deletes and eviction events rather than
	// one burst.
	pokemonUnverifiedTTL       = 55 * time.Minute
	pokemonUnverifiedTTLJitter = 10 * time.Minute
)

// derivedExpiryCacheGrace is how long a record is kept past a DERIVED expiry.
// A pokemon listed again past that expiry is re-derived and its lifetime
// recomputed, and one evicted first is re-derived correctly as a new record,
// so this only has to cover a normal rescan interval — enough to keep an
// encountered 60-minute pokemon's observed values when it turns up again.
const derivedExpiryCacheGrace = 5 * 60

// hasSpawnpointExpiry reports whether the record's expiry came from its
// spawnpoint — a stored despawn_sec (verified) or a derivation from the spawn
// id — rather than being a guess. With derive_despawn on, every record with a
// spawn id gets its expiry one of those two ways; those without one
// (nearby-only, a lure seen before its GMO) carry a guess. With it off, an
// unverified expiry is always a guess.
func (pokemon *Pokemon) hasSpawnpointExpiry() bool {
	return pokemon.ExpireTimestamp.Valid &&
		(pokemon.ExpireTimestampVerified || (pokemon.SpawnId.Valid && config.Config.DeriveDespawn))
}

// expiryCacheGrace is how long past its expiry a record stays cached.
func (pokemon *Pokemon) expiryCacheGrace() int64 {
	if pokemon.ExpireTimestampVerified {
		return 60
	}
	return derivedExpiryCacheGrace
}

func (pokemon *Pokemon) remainingDuration(now int64) time.Duration {
	if pokemon.hasSpawnpointExpiry() {
		grace := pokemon.expiryCacheGrace()
		timeLeft := grace + int64(pokemon.ExpireTimestamp.ValueOrZero()) - now
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
// Distinct from the pokemon-cache TTL: past-despawn and guessed-expiry entries
// must keep the cache's full default window so late or retried protos still
// deduplicate instead of inflating per-area encounter/shiny stats.
func (pokemon *Pokemon) encounterStatsDuration(now int64) time.Duration {
	if pokemon.hasSpawnpointExpiry() {
		if timeLeft := pokemon.expiryCacheGrace() + int64(pokemon.ExpireTimestamp.ValueOrZero()) - now; timeLeft > 60 {
			return time.Duration(timeLeft) * time.Second
		}
	}
	return 0 // the encounter cache interprets 0 as its default TTL
}

// undecodableWildPokemonIds aggregates dropped wild/encounter sightings
// whose spawnpoint id could not be turned into a location, one log line per
// second. Sibling of undecodableSpawnpointIds: the spawnpoint and the pokemon
// are placed independently from the same proto.
var undecodableWildPokemonIds util.DropReporter

// unparseableSpawnpointIds aggregates dropped wild/encounter sightings whose
// spawnpoint id is not a hex integer, one log line per second.
var unparseableSpawnpointIds util.DropReporter

// addWildPokemon applies the shared WildPokemonProto fields — location, spawn
// id, despawn time, display — to this pokemon. Location comes from
// wildPokemonLocation, so a proto at 0,0 places the pokemon at its spawnpoint
// id's cell centre exactly as the spawnpoint row is placed. It returns false,
// having changed nothing, when the spawnpoint id is not parseable or when
// the proto is at 0,0 and the id does not decode: the sighting cannot be
// placed and the caller must drop it. Callers therefore run it before any
// other mutation of the record.
func (pokemon *Pokemon) addWildPokemon(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, timestampMs int64, trustworthyTimestamp bool) bool {
	if wildPokemon.EncounterId != uint64(pokemon.Id) {
		panic("Unmatched EncounterId")
	}

	spawnId, err := strconv.ParseInt(wildPokemon.SpawnPointId, 16, 64)
	if err != nil {
		unparseableSpawnpointIds.Report(func(dropped int64) {
			log.Errorf("[POKEMON] dropped %d wild sighting(s) with an unparseable spawnpoint id in the last second (most recently pokemon %d, spawnpoint %q: %s)",
				dropped, pokemon.Id, wildPokemon.SpawnPointId, err)
		})
		return false
	}
	lat, lon, ok := wildPokemonLocation(spawnId, wildPokemon)
	if !ok {
		undecodableWildPokemonIds.Report(func(dropped int64) {
			log.Warnf("[POKEMON] dropped %d wild sighting(s) at 0,0 whose spawnpoint id does not decode to a level-%d cell in the last second (most recently pokemon %d, spawnpoint %s)",
				dropped, geo.SpawnpointCellLevel, pokemon.Id, wildPokemon.SpawnPointId)
		})
		return false
	}
	pokemon.SetLat(lat)
	pokemon.SetLon(lon)
	pokemon.SetSpawnId(null.IntFrom(spawnId))

	pokemon.setExpireTimestampFromSpawnpoint(ctx, db, timestampMs, trustworthyTimestamp)
	pokemon.setPokemonDisplay(int16(wildPokemon.Pokemon.PokemonId), wildPokemon.Pokemon.PokemonDisplay)
	return true
}

// wildSignificantUpdate returns true if the wild pokemon is significantly different from the current pokemon and
// should be written.
func (pokemon *Pokemon) wildSignificantUpdate(wildPokemon *pogo.WildPokemonProto, time int64) bool {
	pokemonDisplay := wildPokemon.Pokemon.PokemonDisplay
	// We would accept a wild update if the pokemon has changed; or to extend an unknown spawn time that is expired

	// The display fields are stored narrowed, so the incoming readings are
	// narrowed too before they are compared — see narrowUint8's doc comment.
	return pokemon.SeenType.Code == SeenTypeCodeCell ||
		pokemon.SeenType.Code == SeenTypeCodeNearbyStop ||
		pokemon.PokemonId != int16(wildPokemon.Pokemon.PokemonId) ||
		int64OrZero(pokemon.Form) != narrowUint16(int64(pokemonDisplay.Form)) ||
		int64OrZero(pokemon.Weather) != narrowUint8(int64(pokemonDisplay.WeatherBoostedCondition)) ||
		int64OrZero(pokemon.Costume) != narrowUint8(int64(pokemonDisplay.Costume)) ||
		int64OrZero(pokemon.Gender) != narrowUint8(int64(pokemonDisplay.Gender)) ||
		(!pokemon.ExpireTimestampVerified && int64OrZero(pokemon.ExpireTimestamp) < time) ||
		// A sighting that carries the real despawn time confirms an
		// unverified one. spawnpointUpdateFromWild has just stored it, and
		// the pokemon must be decoded to pick it up; an unchanged display with
		// a future expiry would otherwise keep it unverified until it passed.
		(!pokemon.ExpireTimestampVerified && wildHasTTH(wildPokemon)) ||
		// A pool refresh can make an unchanged record eligible for the roll.
		pokemon.rollWouldMark()
}

// nearbySignificantUpdate returns true if the wild pokemon is significantly different from the current pokemon and
// should be written.
func (pokemon *Pokemon) nearbySignificantUpdate(wildPokemon *pogo.NearbyPokemonProto, time int64) bool {
	pokemonDisplay := wildPokemon.PokemonDisplay
	// We would accept a wild update if the pokemon has changed; or to extend an unknown spawn time that is expired

	// Narrowed on both sides, as in wildSignificantUpdate above. The species
	// is PokedexNumber: PokemonDisplay.DisplayId carries the encounter id.
	pokemonChanged := pokemon.PokemonId != int16(wildPokemon.PokedexNumber) ||
		int64OrZero(pokemon.Form) != narrowUint16(int64(pokemonDisplay.Form)) ||
		int64OrZero(pokemon.Weather) != narrowUint8(int64(pokemonDisplay.WeatherBoostedCondition)) ||
		int64OrZero(pokemon.Costume) != narrowUint8(int64(pokemonDisplay.Costume)) ||
		int64OrZero(pokemon.Gender) != narrowUint8(int64(pokemonDisplay.Gender))

	if pokemonChanged {
		return true
	}

	hasExpired := (!pokemon.ExpireTimestampVerified && int64OrZero(pokemon.ExpireTimestamp) < time)

	if hasExpired {
		return true
	}

	if pokemon.SeenType.Code == SeenTypeCodeCell {
		return true
	}

	// A pool refresh can make an unchanged record eligible for the roll.
	if pokemon.rollWouldMark() {
		return true
	}

	// if it's at a nearby stop, or encounter and no other details have changed update is not worthwhile
	return false
}

// applySightingDerivation runs the roll-marking-then-derive pair that every
// sighting path needs. maybeMarkDittoFromRoll runs first so that
// deriveIvIfAbsent derives for the identity the record has settled on —
// the Ditto's own values once the roll has marked it — rather than writing
// the disguise's values for marking to replace.
//
// Both halves read the display the sighting has just applied — PokemonId,
// Form, and Weather, the boost condition the game reports for the displayed
// species. Callers must therefore invoke this after setPokemonDisplay or
// addWildPokemon, and before recomputeCpIfNeeded, which needs the IVs it
// fills in. TestUpdateFromWildMarksDittoAndDerivesItsOwnValues covers the
// whole sequence through the real entry point.
func (pokemon *Pokemon) applySightingDerivation() {
	wasDitto := pokemon.IsDitto
	cpBefore := pokemon.cpInputs()
	pokemon.maybeMarkDittoFromRoll()
	// A record the roll has just marked may already hold values derived for
	// its disguise — a species added to the pool after it was first seen.
	// Marking replaces its IVs and level with the Ditto's and keeps the
	// disguise's moves, which a Ditto carries. CP is cleared when marking
	// actually moved the IVs or level, so the recompute that follows every
	// sighting describes them — CP is latched, and a valid one would
	// otherwise be published unchanged. With derive_iv off marking moves
	// nothing, and a CP observed earlier is kept.
	if pokemon.IsDitto && !wasDitto && pokemon.cpInputs() != cpBefore {
		pokemon.SetCp(null.NewInt(0, false))
	}
	pokemon.deriveIvIfAbsent()
}

// updateFromWild applies a GMO wild sighting. It returns false, with the
// record untouched, when the sighting cannot be placed (see addWildPokemon);
// the caller must then skip the save.
func (pokemon *Pokemon) updateFromWild(ctx context.Context, db db.DbDetails, wildPokemon *pogo.WildPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string) bool {
	if !pokemon.addWildPokemon(ctx, db, wildPokemon, timestampMs, true) {
		return false
	}
	pokemon.SetIsEvent(0)
	switch pokemon.SeenType.Code {
	case SeenTypeCodeUnset, SeenTypeCodeCell, SeenTypeCodeNearbyStop:
		pokemon.SetSeenType(SeenTypeCodeWild)
	}
	pokemon.applySightingDerivation()
	pokemon.recomputeCpIfNeeded(ctx, db, weather)
	pokemon.SetCellId(null.IntFrom(cellId))
	pokemon.setUsernameIfStored(username)
	return true
}

// updateFromMap applies a GMO lure sighting (fort.ActivePokemon) to this
// pokemon. The fort's identity and coordinates are captured at GMO
// extraction (RawMapPokemonData), so placement never depends on the
// pokestop cache. Returns true when the record changed and needs saving.
func (pokemon *Pokemon) updateFromMap(ctx context.Context, db db.DbDetails, mapPokemon RawMapPokemonData, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, username string) bool {
	if pokemon.isNewRecord() {
		pokemon.SetIsEvent(0)
		if fortId, ok := ParseFortId(mapPokemon.FortId); ok {
			pokemon.SetPokestopId(fortId)
		} else if mapPokemon.FortId != "" {
			FortIdParseDrops.Report(func(dropped int64) {
				log.Errorf("[POKEMON] dropped %d unparseable lure-pokemon fort id(s) in the last second (most recently pokemon %d, id %q)",
					dropped, pokemon.Id, mapPokemon.FortId)
			})
		}
		pokemon.SetLat(mapPokemon.Lat)
		pokemon.SetLon(mapPokemon.Lon)
		pokemon.SetSeenType(SeenTypeCodeLureWild)

		if mapPokemon.Data.PokemonDisplay != nil {
			pokemon.setPokemonDisplay(int16(mapPokemon.Data.PokedexTypeId), mapPokemon.Data.PokemonDisplay)
			pokemon.applySightingDerivation()
			pokemon.recomputeCpIfNeeded(ctx, db, weather)
			// The mapPokemon and nearbyPokemon GMOs don't contain actual shininess.
			// shiny = mapPokemon.pokemonDisplay.shiny
		} else {
			log.Warnf("[POKEMON] MapPokemonProto missing PokemonDisplay for %d", pokemon.Id)
		}

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

	// Existing record: the GMO contributes what it alone knows — the
	// verified despawn time — plus the cell and, for a lure_wild record, the
	// display (below). Never touch encounter data and never downgrade
	// lure_encounter to lure_wild.
	switch pokemon.SeenType.Code {
	case SeenTypeCodeLureWild, SeenTypeCodeLureEncounter:
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
	// A lure pokemon that has never been encountered carries DERIVED IVs,
	// level, size and moves, and those belong to one boost branch. The
	// new-record branch above is the only place this path reads the display,
	// so without this a weather flip leaves values that are now wrong and,
	// by design, indistinguishable from scanned ones.
	//
	// LureWild only: a lure_encounter record holds real encounter data, which
	// this path must never touch or downgrade. setPokemonDisplay reaches
	// repopulateIv, which no-ops when the boost state has not moved, so a
	// repeat sighting stays a genuine no-op and `changed` stays false.
	//
	// ProactiveIVSwitch covers the same flip when proactive_iv_switching is
	// on (the default) by sweeping the cache; this is what makes the record
	// correct when it is off, and is idempotent alongside it.
	//
	// Only with derive_iv on: without derivation a lure_wild record carries
	// no stats to go stale, and the display is read once, at creation.
	if config.Config.DeriveIv && pokemon.SeenType.Code == SeenTypeCodeLureWild && mapPokemon.Data.PokemonDisplay != nil {
		wasDirty := pokemon.dirty
		pokemon.setPokemonDisplay(int16(mapPokemon.Data.PokedexTypeId), mapPokemon.Data.PokemonDisplay)
		// A display change clears size and moves, and with the weather
		// unchanged nothing else refills them; derivation does, as on the
		// wild and nearby paths.
		pokemon.applySightingDerivation()
		pokemon.recomputeCpIfNeeded(ctx, db, weather)
		if pokemon.dirty && !wasDirty {
			changed = true
		}
	}

	pokemon.setUsernameIfStored(username)
	return changed
}

// calculateIv is the sole entry point that mutates AtkIv/DefIv/StaIv — they
// have no exported setters (see the Pokemon doc comment) so that all three
// stay consistent with each other and with Iv. Clamped directly here rather
// than through Set*Iv methods that don't exist.
//
// The clamp happens once, up front, and the three clamped values are the
// only thing read afterwards: the comparison, the stores and the Iv sum all
// come from them, so they cannot disagree. Comparing the stored (clamped)
// IVs against the raw a/d/s instead would never converge — an out-of-range
// encounter would re-write and re-dirty the record forever — and an Iv
// computed from the raw sum would disagree with the clamped columns.
//
// The clamp is clampIv, not clampUint8: 15 rather than the tinyint's 255.
// That is what bounds Iv. iv is float(5,2) unsigned and tops out at 999.99,
// and under MariaDB's default STRICT_TRANS_TABLES an out-of-range value
// fails the entire multi-row batch upsert rather than the one row — so a
// bound that holds only for most inputs is not a bound. Clamping at the
// column's 255 leaves a sum of up to 765 and a quotient of up to 1700; two
// out-of-range IVs alone reach 1133. Clamping at the game's 15 caps the sum
// at 45 and Iv at exactly 100, for every possible input, with no arithmetic
// in the argument. See clampIv's doc comment for the two other invariants
// that ride on the same ceiling.
//
// Clamping above the comparison also moves atk_iv/def_iv/sta_iv onto the
// same golbat_field_clamped_total rate as every other field — one count per
// call rather than one per value that reached the record. See clampUint's
// doc comment.
func (pokemon *Pokemon) calculateIv(a int64, d int64, s int64) {
	atk := clampIv(null.IntFrom(a), "atk_iv")
	def := clampIv(null.IntFrom(d), "def_iv")
	sta := clampIv(null.IntFrom(s), "sta_iv")

	// Comparing the whole null.Value covers validity too: an unset field
	// never equals a clamped one.
	if pokemon.AtkIv != atk || pokemon.DefIv != def || pokemon.StaIv != sta {
		pokemon.AtkIv, pokemon.DefIv, pokemon.StaIv = atk, def, sta
		// .45 is 45/100, and 45 is 3 * maxIvPerStat — the same ceiling the
		// clamp above uses, and the one the iv column's own generated
		// definition used before migration 11 made it a plain column.
		pokemon.SetIv(null.FloatFrom(float64(int64OrZero(atk)+int64OrZero(def)+int64OrZero(sta)) / .45))
		pokemon.dirty = true
	}
}

// applyDerivedValues writes one encounter.For Draw onto this record: IVs (via
// calculateIv), Level and Size, then continues the same Draw's walk (via
// Draw.MoveIndices) for move_1/move_2. It is the single derive-then-write
// sequence shared by maybeMarkDittoFromRoll, deriveIvIfAbsent and
// repopulateIv's re-derive branch, so the three cannot diverge in what they
// write. repopulateIv resets CP/PVP itself after calling this; that reset
// belongs to a boost-state flip, not to applying a derived value.
//
// v is taken by value because MoveIndices advances its embedded generator
// state; the caller's copy is unaffected.
//
// Moves are derived for pokemon.PokemonId/Form as they stand, which at every
// call site is the species/form being derived for, except that a Ditto's are
// derived for its disguise (see applyDerivedMoves).
//
// When the species/form's move pools do not resolve (unknown species, empty
// pool, or an uninitialized masterfile — the same Initialized guard
// disguiseBoostedWeathers uses), move_1/move_2 are left untouched rather than
// guessed: a wrong move is indistinguishable from an observed one downstream.
// Pool resolution is derivedMoves (derive_validation.go), shared with
// checkDerivation so the two cannot resolve different pools for the same
// species/form.
func (pokemon *Pokemon) applyDerivedValues(v encounter.Draw) {
	// The gate lives here rather than at the three call sites because this is
	// the only place derived IVs, level, size or moves are written. A caller
	// that forgets the check cannot write them anyway.
	if !config.Config.DeriveIv || hasPlayerSpecificStats(pokemon.PokemonId) {
		return
	}

	pokemon.calculateIv(int64(v.Attack), int64(v.Defense), int64(v.Stamina))
	pokemon.SetLevel(null.IntFrom(int64(v.Level)))

	// Size and moves are filled only when absent, unlike the IVs and level
	// above. clearIv drops the IVs on a weather flip it cannot match but
	// deliberately leaves move_1, move_2 and size alone, so a record reaching
	// here after a flip still carries the ones the game actually reported.
	// Overwriting an observed move with a derived one is the worst case this
	// package has: the game GRANTS moves outside the pool the derivation draws
	// from (event and legacy moves), so for that population the derived value
	// is known to be wrong — checkDerivation refuses to even score it, via
	// moveOutOfPool. A derived value is indistinguishable from an observed one
	// once written, so the observation must win.
	if !pokemon.Size.Valid {
		pokemon.SetSize(null.IntFrom(int64(v.Size)))
	}

	pokemon.applyDerivedMoves(v)
}

// applyDerivedMoves fills whichever move slots are absent from the draw. It is
// the moves half of applyDerivedValues, on its own so that a record whose
// identity has just changed can have its moves re-derived without its IVs
// and level being touched — see resetDittoAttributes.
func (pokemon *Pokemon) applyDerivedMoves(v encounter.Draw) {
	if !config.Config.DeriveIv || hasPlayerSpecificStats(pokemon.PokemonId) {
		return
	}
	if pokemon.Move1.Valid && pokemon.Move2.Valid {
		return
	}
	// A Ditto carries its disguise's moves, which is what an encounter
	// reports for it, and what you catch when the disguise is shiny.
	species, form := pokemon.PokemonId, int16(int64OrZero(pokemon.Form))
	if pokemon.IsDitto {
		species, form = int16(pokemon.DisplayPokemonId.ValueOrZero()), int16(pokemon.DisplayPokemonForm.ValueOrZero())
	}
	moves := derivedMoves(&v, species, form)
	if !moves.Resolved {
		return
	}
	if !pokemon.Move1.Valid {
		pokemon.SetMove1(null.IntFrom(int64(moves.Move1)))
	}
	if !pokemon.Move2.Valid {
		pokemon.SetMove2(null.IntFrom(int64(moves.Move2)))
	}
}

// deriveSizeAndMovesIfAbsent fills whichever of size and moves are absent, for
// the species and form the record displays, under the same exclusions as
// deriveIvIfAbsent, on a record whose IVs are already settled. The draw is
// taken on the record's own boost branch: the two branches' IV draws almost
// always advance the generator equally, but not when the boosted branch's
// nextInt(12) hits Java's rejection path.
func (pokemon *Pokemon) deriveSizeAndMovesIfAbsent() {
	if !config.Config.DeriveIv || pokemon.IsStrong.ValueOrZero() || hasPlayerSpecificStats(pokemon.PokemonId) {
		return
	}
	v := encounter.For(uint64(pokemon.Id), pokemon.statsBoosted(), int32(int64OrZero(pokemon.Form)))
	if !pokemon.Size.Valid {
		pokemon.SetSize(null.IntFrom(int64(v.Size)))
	}
	pokemon.applyDerivedMoves(v)
}

// gmoCellLevel is the S2 level of the cells a GetMapObjects response is
// organised by, and so the level nearby pokemon cells are expressed at.
const gmoCellLevel = 15

// updateFromNearby applies a GMO nearby sighting. A pokemon attached to a fort
// is placed at that pokestop; a fort-less one (a cell pokemon) at its cell
// centre. It returns false, with the record untouched, when there is nowhere
// to place it: the pokestop is unknown or has no location, or a cell pokemon
// has no cell. The caller must then skip the save; a later GMO can place it
// once the pokestop is known. A pokemon already placed more precisely (wild,
// encounter, lure) keeps its location and takes only the display.
func (pokemon *Pokemon) updateFromNearby(ctx context.Context, db db.DbDetails, nearbyPokemon *pogo.NearbyPokemonProto, cellId int64, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, timestampMs int64, username string) bool {
	// applyDisplay runs on every path that applies the sighting and on none
	// that refuses it, which is why derivation lives here rather than at the
	// end of the function: derived values are part of a sighting being
	// applied, and the paths that return false leave the record untouched for
	// the caller to skip. Within it the order is fixed — the display that
	// derivation reads, then derivation, then the CP that needs the IVs it
	// fills in.
	//
	// It runs AFTER the record's coordinates are settled. recomputeCpIfNeeded
	// resolves a Ditto's cell weather from Lat/Lon, so on the placement path
	// below it is called once the location is set; on a new record before
	// that, Lat/Lon are 0,0 and the lookup misses the batch weather and goes
	// to the database under the pokemon lock. The paths that keep an existing
	// location call it first, since that location is already the right one.
	applyDisplay := func() {
		pokemon.SetIsEvent(0)
		pokemon.setPokemonDisplay(int16(nearbyPokemon.PokedexNumber), nearbyPokemon.PokemonDisplay)
		pokemon.applySightingDerivation()
		pokemon.recomputeCpIfNeeded(ctx, db, weather)
	}

	var lat, lon float64
	overrideLatLon := pokemon.isNewRecord()
	seenType := SeenTypeCodeCell
	var fortId FortId
	if pokestopId := nearbyPokemon.FortId; pokestopId != "" {
		switch pokemon.SeenType.Code {
		case SeenTypeCodeUnset, SeenTypeCodeCell:
			overrideLatLon = true // a better estimate is available
		case SeenTypeCodeNearbyStop:
		default:
			applyDisplay()
			return true
		}
		var ok bool
		fortId, ok = ParseFortId(pokestopId)
		if !ok {
			FortIdParseDrops.Report(func(dropped int64) {
				log.Errorf("[POKEMON] dropped %d unparseable updateFromNearby fort id(s) in the last second (most recently pokemon %d, id %q)",
					dropped, pokemon.Id, pokestopId)
			})
			return false
		}
		pokestop, unlock, _ := getPokestopRecordReadOnly(ctx, db, fortId, "updateFromNearby")
		if pokestop == nil {
			return false
		}
		lat, lon = pokestop.Lat, pokestop.Lon
		unlock()
		if lat == 0 && lon == 0 {
			return false
		}
		if cellId == 0 {
			// Snapshot entry whose fort was not listed in the response:
			// the pokestop's own location says which cell it is in.
			cellId = int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(lat, lon)).Parent(gmoCellLevel))
		}
		seenType = SeenTypeCodeNearbyStop
	} else {
		if cellId == 0 {
			return false
		}
		if !overrideLatLon && pokemon.SeenType.Code != SeenTypeCodeCell {
			// do not downgrade to nearby cell
			applyDisplay()
			return true
		}
		s2cell := s2.CellFromCellID(s2.CellID(cellId))
		lat = s2cell.CapBound().RectBound().Center().Lat.Degrees()
		lon = s2cell.CapBound().RectBound().Center().Lng.Degrees()
	}

	pokemon.SetSeenType(seenType)
	if seenType == SeenTypeCodeNearbyStop {
		pokemon.SetPokestopId(fortId)
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
	pokemon.setUsernameIfStored(username)
	applyDisplay()
	return true
}

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
//
// These are the only named constants for the enum — the decode path works
// entirely in SeenTypeCode (SetSeenType takes one directly), so a typo'd
// name fails to compile instead of silently no-opping the way a runtime
// string-to-code lookup would. A string form only exists at seenTypeStrings
// below and at the output boundaries (Value, MarshalJSON) that read it.
type SeenTypeCode uint8

const (
	SeenTypeCodeUnset                 SeenTypeCode = iota // zero value: not a real seen type
	SeenTypeCodeWild                                      // Pokemon was seen in the wild, accurate location but with no IV details
	SeenTypeCodeEncounter                                 // Pokemon has been encountered giving exact details of current IV
	SeenTypeCodeNearbyStop                                // Pokemon was seen at a nearby Pokestop, location set to lon, lat of pokestop
	SeenTypeCodeCell                                      // Pokemon was seen in a cell (without accurate location)
	SeenTypeCodeLureWild                                  // Pokemon was seen at a lure
	SeenTypeCodeLureEncounter                             // Pokemon has been encountered at a lure
	SeenTypeCodeTappableEncounter                         // Pokemon has been encountered from tappable
	SeenTypeCodeTappableLureEncounter                     // Pokemon has been encountered from a lured tappable
)

// SeenTypeCodeUnknown is the code for a seen_type this binary does not
// recognise: a value a newer binary wrote after a migration widened the
// enum, read back by this one during a rollback or from a lagging replica.
// Scan degrades to it (see Scan's doc comment for why it must not error).
//
// It is deliberately not Unset. Unset means "never set", and two decode
// switches act on that: updateFromWild's `case Unset, Cell, NearbyStop`
// rewrites the record to wild, and updateFromNearby's `case Unset, Cell`
// replaces precise coordinates with a pokestop's or a cell centre's — then
// the save persists the downgrade over the value the newer binary wrote. An
// unrecognised value must instead behave the way the opaque null.String it
// replaced behaved: equal to none of the eight real codes, so every switch
// falls through to default and the record round-trips unharmed.
//
// 255 rather than the next iota so a future enum member (there have been
// three widenings already: migrations 3, 43, 45) can be appended above
// without colliding with it, and so no seenTypeStrings index can ever reach
// it. String() therefore reports "" for it, and it never reaches the column:
// Scan leaves Valid false so Value returns NULL, and both pokemon write
// paths spell seen_type as COALESCE(<incoming>, seen_type) so that NULL
// leaves the stored value alone instead of erasing it.
const SeenTypeCodeUnknown SeenTypeCode = 255

// seenTypeStrings maps codes to the exact strings in the enum column. The
// order must match the constants above, including the leading "" at index 0
// for SeenTypeCodeUnset — it is never a value the enum column holds, and
// String() relies on it to make Unset (and any other out-of-range code)
// report as "". Adding a real value here requires a migration widening the
// enum — see sql/45_tappables_seen_type_lure.up.sql.
var seenTypeStrings = [...]string{
	"",                        // SeenTypeCodeUnset
	"wild",                    // SeenTypeCodeWild
	"encounter",               // SeenTypeCodeEncounter
	"nearby_stop",             // SeenTypeCodeNearbyStop
	"nearby_cell",             // SeenTypeCodeCell
	"lure_wild",               // SeenTypeCodeLureWild
	"lure_encounter",          // SeenTypeCodeLureEncounter
	"tappable_encounter",      // SeenTypeCodeTappableEncounter
	"tappable_lure_encounter", // SeenTypeCodeTappableLureEncounter
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

// ParseSeenType converts a database or proto string into a NullSeenType. An
// empty string is treated as NULL; an unrecognised value is an error. What
// happens to that error is the caller's call — Scan below degrades it to a
// warning because the read path must tolerate an enum value this binary
// doesn't know yet, while Value and UnmarshalJSON stay strict. See Scan's
// doc comment for why.
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

// Scan degrades on an unrecognised enum value instead of failing: it sets
// Code = SeenTypeCodeUnknown and Valid = false, and logs a throttled
// warning, rather than returning an error.
//
// Both halves of that matter. Valid = false keeps every existing null check
// working and keeps the value out of the column (Value returns NULL, and
// the write paths COALESCE a NULL into "leave the stored value alone").
// Code = Unknown rather than Unset is what stops the degrade from being
// destructive: Unset means "never set" and two decode switches rewrite the
// record on it — see SeenTypeCodeUnknown's doc comment.
//
// This is deliberately asymmetric with Value below. The seen_type enum has
// been widened three times (migrations 3, 43, 45); a newer binary can write
// a value this one's ParseSeenType doesn't recognise yet — a rollback or a
// lagging replica during a mixed deployment, both routine. Erroring on that
// case would protect scan statistics from a wrong code, but an error here
// fails the row load, which leaves getOrCreatePokemonRecord's cache entry stuck marked
// newRecord — every subsequent sighting retries and fails the same load, so
// that pokemon silently stops being processed for as long as the
// deployment is mixed. Degrading avoids that: the row loads with seen_type
// unknown and the pokemon keeps being processed.
//
// The in-memory value then stays unknown until an encounter arrives.
// updatePokemonFromEncounterProto and its disk and tappable siblings are the
// only paths that set a seen type without first asking what the current one
// is; wild, lure and nearby sightings all check, and their switches list
// Unset, Cell and NearbyStop, so Unknown reaches no case. That is not an
// oversight to fix — adding `case SeenTypeCodeUnknown:` to updateFromWild to
// make it heal there is exactly the downgrade the sentinel exists to
// prevent, and TestScanUnknownSeenTypeIsInertInTheDecodeSwitches is what
// fails if someone does. Waiting costs nothing: the stored column is never
// overwritten while the value is unrecognised, so the record also comes back
// whole on the next load once this binary understands the enum again.
//
// Value stays strict on purpose, not by oversight: writing an out-of-enum
// string to the seen_type ENUM column is silently stored as "" by MariaDB,
// invisible data loss with no read-side self-heal to fall back on — the
// write side has no equivalent of "try again next sighting." Read
// permissive, write strict. Do not "fix" this back to symmetric; see
// TestNullSeenTypeScanUnknownValueDegrades.
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
		n.Code, n.Valid = SeenTypeCodeUnknown, false
		seenTypeScanWarns.Report(func(rows int64) {
			log.Warnf("[POKEMON] NullSeenType.Scan: %s; %d row(s) in the last second held a seen_type this binary does not recognise. "+
				"Their records are left with seen_type unknown and the stored value is not overwritten", err, rows)
		})
		return nil
	}
	*n = parsed
	return nil
}

// seenTypeScanWarns aggregates Scan's unrecognised-value warning to one
// line a second. Scan runs once per row loaded — millions during preload,
// and under the entity lock at runtime — so a warning per row would be log
// I/O proportional to the pokemon table for a condition that is one fact
// repeated, not many. util.DropReporter is the codebase's aggregator for
// exactly this (raw_limiter.go, fort_tracker.go, stats.go).
//
// Value-typed, like every other reporter in the codebase. A test that needs
// a fresh window calls Reset on it rather than swapping the variable, which
// would be an unsynchronised write to a global this decode path reads.
var seenTypeScanWarns util.DropReporter

// Value rejects any code with no string form — that includes
// SeenTypeCodeUnset and any code past the end of the table. It is never
// reached for SeenTypeCodeUnknown, which Scan pairs with Valid = false, so
// the unrecognised case returns NULL above and the write paths' COALESCE
// turns that into "leave the stored value alone". Writing "" to
// the seen_type ENUM column is accepted silently by MariaDB, so this must
// fail loudly instead of writing it. Kept strict on purpose even though
// Scan (above) degrades instead of erroring on the mirror-image read
// failure — see Scan's doc comment for why the two are not symmetric.
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

// UnmarshalJSON decodes a JSON null to SeenTypeCodeUnknown, not to the Go
// zero value, and the difference matters. MarshalJSON is lossy in one
// direction: it emits null for any invalid NullSeenType, so both Unset
// ("never set") and Unknown ("set to something this binary cannot name")
// come back as the same three characters. A decoder cannot tell them apart
// and has to pick one, so it picks the one that cannot do damage — Unknown
// matches no case in either decode switch, while Unset is what licenses
// updateFromWild's rewrite to "wild" and updateFromNearby's replacement of
// precise coordinates. Round-tripping a degraded record through the webhook
// payload (decoder/pokemon_state.go's PokemonWebhook) must not turn the
// inert sentinel into the destructive one.
//
// Deliberately not symmetric with Scan(nil), which does yield Unset. A SQL
// NULL is unambiguous — the column has never held a value — so a wild or
// nearby sighting filling it in is the correct behaviour there, and
// degrading it to Unknown would strand rows that are simply new.
//
// Also deliberately not symmetric with Scan on an unrecognised string, which
// degrades and warns where this returns an error. Scan must not fail, because
// a failed row load leaves getOrCreatePokemonRecord's cache entry marked
// newRecord and that pokemon stops being processed (see Scan's doc comment).
// A JSON decode has no such cache entry to strand: the error goes back to
// whatever is reading the payload, loudly and recoverably.
func (n *NullSeenType) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		n.Code, n.Valid = SeenTypeCodeUnknown, false
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

	// A stored despawn second was observed (a TTH sighting), so it is used
	// whenever present. Cached and synced with no stored value: nothing to
	// look up, derive.
	if sp, ok := spawnpointCache.Get(spawnId); ok {
		if despawnSecond, known, synced := sp.DespawnSecFast(); synced {
			if known {
				pokemon.applyVerifiedDespawn(despawnSecond, timestampMs)
			} else {
				pokemon.applyDerivedDespawn(spawnId, timestampMs, trustworthyTimestamp)
			}
			return
		}
	}

	// Cache miss or mid-load. The table holds every spawnpoint's row, so a
	// miss may be hiding a stored value worth loading.
	spawnPoint, unlock, err := getSpawnpointRecord(ctx, db, spawnId, "setExpireTimestampFromSpawnpoint")
	lookupFailed := spawnpointLookupFailed(spawnPoint, err)
	if spawnPoint != nil && spawnPoint.DespawnSec.Valid {
		despawnSecond := int(spawnPoint.DespawnSec.ValueOrZero())
		unlock()
		pokemon.applyVerifiedDespawn(despawnSecond, timestampMs)
		return
	}
	if unlock != nil {
		unlock()
	}
	// A failed lookup is not an unknown despawn. The row may hold the
	// despawn time a record's verified expiry came from — a restored record
	// meeting a cold cache and a database timeout — and deriving would
	// replace it with an unverified prediction half an hour early for an
	// hour-long spawn. An expiry still in the future is kept; derivation
	// answers only when there is nothing usable.
	if lookupFailed && pokemon.ExpireTimestamp.Valid && int64OrZero(pokemon.ExpireTimestamp) > timestampMs/1000 {
		return
	}

	pokemon.applyDerivedDespawn(spawnId, timestampMs, trustworthyTimestamp)
}

// applyDerivedDespawn sets this pokemon's expiry from its spawn id alone.
//
// The despawn position within any half hour is a function of the spawn id, so
// the next occurrence of that position after the sighting is the answer for
// every spawnpoint whose pokemon live half an hour — roughly 94% of them — and
// half an hour early for the rest.
//
// It is written UNVERIFIED, which is what corrects the rest. A pokemon still
// listed after an unverified expiry is re-admitted by wildSignificantUpdate,
// and deriving again from that later sighting lands on the next half hour. No
// despawn time is written to the spawnpoint: only a sighting that shows the
// real despawn (spawnpointUpdateFromWild's TTH) does that, so a stored
// despawn_sec is always observed, never inferred.
//
// An untrustworthy timestamp — an encounter's receipt time, which can trail
// the game's clock — does not replace an expiry already derived for this
// spawnpoint unless that expiry passed more than despawnClockSkewTolerance
// ago. Deriving from a receipt that arrived just after the despawn would move
// a pokemon the game still showed half an hour on. A guess (a nearby or
// pre-GMO lure sighting's) is not on the spawnpoint's phase, and is replaced.
//
// With no such expiry, a receipt time derives like any other: the pokemon was
// there when the game served it, so a phase moment more than
// derivedDespawnTolerance before the receipt is its spawn, not its despawn.
//
// With derive_despawn off it sets the guess setUnknownTimestamp sets instead.
// This is the only place a derived pokemon expiry is written, so the gate
// lives here.
func (pokemon *Pokemon) applyDerivedDespawn(spawnId int64, timestampMs int64, trustworthyTimestamp bool) {
	nowSec := timestampMs / 1000
	if !config.Config.DeriveDespawn {
		pokemon.setUnknownTimestamp(nowSec)
		pokemon.SetExpireTimestampVerified(false)
		return
	}
	if !trustworthyTimestamp && pokemon.ExpireTimestamp.Valid {
		existing := int64OrZero(pokemon.ExpireTimestamp)
		if existing >= nowSec-despawnClockSkewTolerance && onDerivedPhase(spawnId, existing) {
			return
		}
	}
	next := deriveExpiry(spawnId, nowSec)
	// A derived expiry never moves backward. Deriving from a later sighting
	// gives the same or a later occurrence, so an earlier one comes only from
	// a sighting older than the last — a delayed GMO — which would undo a
	// 60-minute pokemon's correction and drop a live pokemon at an expiry
	// that has passed.
	if pokemon.ExpireTimestamp.Valid && !pokemon.ExpireTimestampVerified {
		if existing := int64OrZero(pokemon.ExpireTimestamp); existing > next && onDerivedPhase(spawnId, existing) {
			return
		}
	}
	pokemon.SetExpireTimestamp(null.IntFrom(next))
	pokemon.SetExpireTimestampVerified(false)
}

// spawnpointLookupFailed reports whether getSpawnpointRecord failed to say
// whether the spawnpoint has a stored despawn time, as opposed to saying it
// has none. Besides an error, that is a cached spawnpoint whose despawn was
// never synced: the placeholder getOrCreateSpawnpointRecord leaves behind when
// its database read fails, which getSpawnpointRecord returns from the cache
// with no error. One known to have no row is synced.
func spawnpointLookupFailed(spawnPoint *Spawnpoint, err error) bool {
	if err != nil {
		return true
	}
	if spawnPoint == nil {
		return false
	}
	_, _, synced := spawnPoint.DespawnSecFast()
	return !synced
}

// derivedDespawnTolerance is how far past a derived expiry a sighting may be
// and still be taken as that same despawn. The derived second can differ from
// the game's by a couple of seconds, and both sides are truncated to whole
// seconds, so a pokemon on a half-hour spawnpoint can be listed a moment after
// its derived expiry. Without this, that moment would move it half an hour on
// and show it long after it had gone.
const derivedDespawnTolerance = 5

// deriveExpiry is the derived expiry for a sighting at nowSec: the next
// occurrence of the spawnpoint's phase, counting one up to
// derivedDespawnTolerance in the past as the current one.
func deriveExpiry(spawnId int64, nowSec int64) int64 {
	return despawn.NextOccurrence(spawnId, time.Unix(nowSec-derivedDespawnTolerance, 0)).Unix()
}

// onDerivedPhase reports whether expiry falls exactly on this spawnpoint's
// derived despawn position — that is, whether it was derived for it rather
// than guessed.
func onDerivedPhase(spawnId int64, expiry int64) bool {
	return despawn.NextOccurrence(spawnId, time.Unix(expiry-1, 0)).Unix() == expiry
}

// despawnClockSkewTolerance is how far apart resolveDespawnExpiry's two
// timestamps may be before their difference is treated as a real phantom hour
// rather than as a disagreement about the time.
//
// Both sides should be the game's own clock. expiry is computed from the
// caller's timestampMs, which every caller threads from a proto, and
// firstSeen is stamped by savePokemonRecordAsAtTime from the timestamp its
// caller was given — also a proto value on the GMO and encounter paths. So
// the tolerance is not absorbing an inherent difference between machines; it
// covers a record whose firstSeen was stamped from the host clock, or any
// path that stamps one that way. It is bounded only by an hour, since a
// difference approaching 3600 is indistinguishable from the phantom hour it
// exists to catch.
const despawnClockSkewTolerance = 120

// despawnSecondOfHour returns the absolute second-of-hour used by all
// despawn_sec writers and readers, independent of the process timezone.
//
// Rows holding the local second-of-hour agree with this in every
// whole-hour UTC offset, since a whole-hour shift changes neither minute nor
// second; rows written in a half-hour or 45-minute zone differ by 1800s or
// 900s.
func despawnSecondOfHour(unixSec int64) int {
	return int(unixSec % 3600)
}

// resolveDespawnExpiry converts a spawnpoint despawn second-of-hour into an
// absolute expiry for a sighting at nowSec, wrapping a negative offset
// forward by an hour, then clamping that wrap when it can be PROVEN wrong: no
// encounter or tappable lives longer than an hour, so an expiry landing more
// than 3600+despawnClockSkewTolerance seconds past a known firstSeen is a
// phantom hour, not a real one.
//
// Shared by Pokemon.applyVerifiedDespawn and Tappable.applyVerifiedDespawn so
// the offset/wrap/clamp arithmetic exists in exactly one place.
//
// firstSeen == 0 means "no lower bound available" and must NEVER clamp:
// Pokemon's FirstSeenTimestamp is 0 during a new record's first decode
// (clamping there would destroy a genuinely-59m59s-away expiry), and a
// Tappable's in-memory-only firstSeen is 0 whenever this process has not
// itself created the record fresh (see Tappable.firstSeen's doc comment for
// why a DB-reloaded tappable has no such bound).
//
// clamped reports only whether the wrap fired here — NOT whether the caller
// should count it. A clamp is counted only when it changes the stored expiry
// (see countDespawnWrapClamp); that depends on each caller's previously-stored
// ExpireTimestamp, so it stays caller-side.
func resolveDespawnExpiry(despawnSecond int, nowSec int64, firstSeen int64) (expiry int64, clamped bool) {
	secondOfHour := despawnSecondOfHour(nowSec)

	despawnOffset := despawnSecond - secondOfHour
	if despawnOffset < 0 {
		despawnOffset += 3600
	}

	expiry = nowSec + int64(despawnOffset)

	// Both timestamps should be the game's own: expiry from the caller's
	// timestampMs, firstSeen from the value savePokemonRecordAsAtTime was
	// given. despawnClockSkewTolerance covers a record stamped from the host
	// clock.
	//
	// A legitimate clamp can land slightly in the past — a Pokemon seen in the
	// instant it despawns — but not far into it. We are looking at this
	// Pokemon now, so its true expiry is at or just behind the present; a
	// clamped result an hour behind means the two clocks disagreed rather
	// than that an hour was phantom. The two errors are not equal: too long
	// shows a Pokemon briefly after it has gone, while too short hides a
	// catchable one and drops it to the corpse TTL, so where the inputs
	// cannot be reconciled the unclamped value is the safer wrong answer.
	if firstSeen > 0 && expiry-firstSeen > 3600+despawnClockSkewTolerance && expiry-3600 > nowSec-despawnClockSkewTolerance {
		expiry -= 3600
		clamped = true
	}

	return expiry, clamped
}

// countDespawnWrapClamp records a wrap clamp only when it actually corrects
// the record. A record re-decoded after its despawn — a Ditto, whose stored
// species never matches the disguise a GMO reports, passes the wild gate on
// every GMO — clamps back to the identical value each time. Counting those
// would bury real clamps under a baseline of re-confirmations.
func countDespawnWrapClamp(clamped, priorValid bool, prior, expiry int64) {
	if clamped && (!priorValid || prior != expiry) {
		getStatsCollector().IncDespawnWrapClamped()
	}
}

// applyVerifiedDespawn converts a spawnpoint despawn second-of-hour into a
// verified expire timestamp for this pokemon.
func (pokemon *Pokemon) applyVerifiedDespawn(despawnSecond int, timestampMs int64) {
	nowSec := timestampMs / 1000

	// A sighting just past a verified despawn on this same phase is a late
	// sighting of that despawn, not a new one: despawn_sec is stored to within
	// SetDespawnSec's 2s tolerance, so the game can list a pokemon a moment
	// past it. Recomputing from there wraps forward an hour, and the clamp
	// misses a pokemon first seen within two minutes of its despawn. Only a
	// PASSED expiry is held: an hour-late one still in the future is replaced,
	// which is how a sighting before the despawn corrects an encounter whose
	// receipt time wrapped it.
	if pokemon.ExpireTimestampVerified && pokemon.ExpireTimestamp.Valid {
		stored := int64OrZero(pokemon.ExpireTimestamp)
		if stored < nowSec && despawnSecondOfHour(stored) == despawnSecond {
			return
		}
	}

	expiry, clamped := resolveDespawnExpiry(despawnSecond, nowSec, int64(pokemon.FirstSeenTimestamp))

	countDespawnWrapClamp(clamped, pokemon.ExpireTimestamp.Valid, int64OrZero(pokemon.ExpireTimestamp), expiry)

	pokemon.SetExpireTimestamp(null.IntFrom(expiry))
	pokemon.SetExpireTimestampVerified(true)
}

func (pokemon *Pokemon) setUnknownTimestamp(now int64) {
	if !pokemon.ExpireTimestamp.Valid {
		pokemon.SetExpireTimestamp(null.IntFrom(now + 20*60)) // should be configurable, add on 20min
	} else {
		if int64OrZero(pokemon.ExpireTimestamp) < now {
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

// markDittoFromRoll is setDittoAttributes(mode, true, old, new) plus setting
// dittoRollMarked — the single place the flag is set, as SetIsDitto(false) is
// the single place it is cleared (see the field's doc comment). Every other
// setDittoAttributes(..., true, ...) call is detectDitto's evidence-based
// confirmation, which must NOT set dittoRollMarked, so the flag stays out of
// setDittoAttributes. Any path that marks a Ditto by prediction rather than
// scan evidence must call this, not setDittoAttributes directly, or the
// provenance flag the audit log depends on is silently not set.
func (pokemon *Pokemon) markDittoFromRoll(mode string, old, new *pokemonScan) {
	pokemon.setDittoAttributes(mode, true, old, new)
	pokemon.dittoRollMarked = true
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

// maybeMarkDittoFromRoll implements roll-based Ditto marking (config
// ditto_derivation/ditto_pool): when this sighting's displayed species and form are
// in the pool and the encounter id's roll (encounter.DittoCandidateRoll, see
// dittoSuspectRoll) fires, it marks the record as a Ditto and derives the
// Ditto's own IVs.
//
// The boost branch comes from pokemon.Weather — the condition the game
// reports for the DISPLAYED species — through dittoCaughtBoosted, not from
// the cell's weather; see dittoCaughtBoosted for why no cell lookup is
// needed.
//
// ORDERING IS LOAD-BEARING. Every call site (applySightingDerivation,
// addEncounterPokemon) must call this after the sighting's display has been
// applied (setPokemonDisplay / addWildPokemon), so PokemonId/Form/Weather
// reflect this sighting. In addEncounterPokemon it must precede detectDitto,
// so detectDitto sees dittoRollMarked and takes its keep branch rather than
// treating a first-ever encounter as an unmarked record with missing
// history, and must precede the scanHistory append (see the call site).
//
// Does nothing when ditto_derivation is off, the record is already a Ditto (by
// roll or by detectDitto — the roll only marks a record nothing has
// concluded about), the record is strong, the record already has scan
// history, the display is not in the pool, or the roll does not fire.
//
// The scan-history guard exists because detectDitto's history-present
// branches (those that find a boostedScan or unboostedScan) assume any history
// on an IsDitto record is the Ditto's own, and uses it to recover the Ditto's
// IVs across boost states. Roll-marking a record with earlier non-Ditto
// history would hand that branch the disguise's scans and corrupt the
// recovery; the missing-history branch only runs when history is empty.
// Observation outranks prediction: detectDitto is authoritative over any
// record it has real data about. See TestMaybeMarkDittoFromRollScanHistoryGate.
func (pokemon *Pokemon) maybeMarkDittoFromRoll() {
	if !pokemon.rollWouldMark() {
		return
	}

	pokemon.markDittoFromRoll("roll", nil, nil)

	// setPokemonDisplay has already stored this sighting's reported condition,
	// which is all dittoCaughtBoosted needs. Form is always 0: setDittoAttributes
	// above set it, and encounter.For reads form only for the Pumpkaboo/
	// Gourgeist size override, which cannot apply to a Ditto.
	v := encounter.For(uint64(pokemon.Id), dittoCaughtBoosted(int64OrZero(pokemon.Weather)), 0)
	pokemon.applyDerivedValues(v)
}

// rollWouldMark reports whether maybeMarkDittoFromRoll would mark this record
// as it stands. The wild and nearby significance checks use it too: a pool
// refresh can add a species a cached record already displays, and an
// unchanged sighting of it is otherwise skipped for the rest of its life
// without the roll being asked. The cheap tests come first, so a skipped
// sighting of a species outside the pool costs a map lookup.
func (pokemon *Pokemon) rollWouldMark() bool {
	// IsStrong excludes a strong Pokemon, for the same reason deriveIvIfAbsent
	// and repopulateIv's re-derive branch do: the ordinary boosted-floor model
	// behind applyDerivedValues below does not describe a strong Pokemon's
	// stats. Without this, a pool species arriving strong gets marked on the
	// roll's own 2% and then written ordinary IVs and a level that cannot
	// occur for it — and nothing takes the identity back, because detectDitto
	// returns on `scan.Strong` before any Ditto logic runs. repopulateIv's
	// Ditto arm calls that combination unhandleable outright ("Strong
	// Ditto???"); this is what stops it being created.
	if !config.Config.DittoDerivation || pokemon.IsDitto || pokemon.IsStrong.ValueOrZero() {
		return false
	}
	// A record that has been encountered before has already been through
	// detectDitto, which is the authority on it. Its scan history says so in
	// memory, but is persisted only with pokemon_internal_to_db; the
	// encounter's seen_type is always persisted, so a refuted record reloaded
	// without its history is not marked again — which would overwrite the IVs
	// and moves the encounter observed. oldValues is snapshotted when the lock
	// is taken, before an encounter in progress writes its own seen_type, so
	// a first encounter still marks.
	if encounteredSeenType(pokemon.oldValues.SeenType.Code) {
		return false
	}
	// pokemon.Form, not DisplayPokemonForm: this runs before the record is
	// marked, so both still describe the DISGUISE. A Ditto only wears a
	// species' normal form, so the form is half the membership test — without
	// it the roll fires on 2% of the variants too.
	if !inDittoPool(pokemon.PokemonId, int16(int64OrZero(pokemon.Form))) {
		return false
	}
	// The roll before the history check: it is a few generator steps, where
	// the history check may decode GolbatInternal.
	if !dittoSuspectRoll(encounter.DittoCandidateRoll(uint64(pokemon.Id))) {
		return false
	}
	// populateInternal (not a bare len(pokemon.scanHistory) check) so a
	// record freshly loaded from the database — GolbatInternal bytes
	// present, but not yet lazily decoded into scanHistory in memory — is
	// still correctly seen as having history. It is a no-op once
	// scanHistory is already populated, so this costs nothing on the
	// common (already-hydrated or genuinely-new) path.
	pokemon.populateInternal()
	return len(pokemon.scanHistory) == 0
}

// dittoDisguises holds the disguises — species AND form — detectDitto has
// confirmed from scan evidence since golbat started, with when each was last
// confirmed. It is empty at every start.
var dittoDisguises sync.Map

// knownDittoDisguise is detectDitto's prior for the two transitions its
// evidence cannot settle (BN>[0P] and 0N>[B0]): whether a scan shows a known
// disguise. Species AND form, as for the roll: a Ditto only wears a species'
// NORMAL form, so a Hisuian Growlithe is not a known disguise because an
// ordinary one is. The pool in force answers from startup; a disguise
// confirmed since start counts too, since a confirmation is the stronger
// evidence and the pool can lag the game.
func knownDittoDisguise(scan *pokemonScan) bool {
	key := dittoPoolEntry{pokemonId: int16(scan.Pokemon), form: int16(scan.Form)}
	if dittoPoolLookup.Load().has(key) {
		return true
	}
	_, confirmed := dittoDisguises.Load(key)
	return confirmed
}

// confirmDitto records a disguise detectDitto has confirmed from scan
// evidence, and logs the first confirmation of each species and form since
// start. That line separates the two cases worth separating: a disguise the
// pool already lists, which is corroboration, and one it does not, which is
// either a change in the game the pool has not caught up with or a
// detection worth checking — the same question logUnknownDittoDisguise asks
// from the derivation side.
func confirmDitto(scan *pokemonScan) {
	now := time.Now()
	key := dittoPoolEntry{pokemonId: int16(scan.Pokemon), form: int16(scan.Form)}
	if lastSeen, exists := dittoDisguises.Swap(key, now); exists {
		log.Debugf("[DITTO] Disguise %s reseen after %s", scan, now.Sub(lastSeen.(time.Time)))
		return
	}
	if inDittoPool(key.pokemonId, key.form) {
		log.Infof("[DITTO] Confirmed a Ditto disguised as %s from scan history — in the ditto pool. "+
			"First confirmation of this disguise since start.", key)
		return
	}
	log.Warnf("[DITTO] Confirmed a Ditto disguised as %s from scan history — NOT in the ditto pool. "+
		"Confirm in game; if genuine, report it for the published pool. %s Scan: %s",
		key, dittoPoolAddAdvice(fmt.Sprintf("%d:%d", key.pokemonId, key.form)), scan)
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
		} else if pokemon.dittoRollMarked {
			// A roll-mark is a PREDICTION (see dittoRollMarked), not
			// detectDitto's own confirmation, so a roll-marked record has
			// no history at its first encounter by construction — not the
			// unexpected condition the error below flags.
			//
			// This branch must not reset the record. Resetting is right
			// when an encounter CONTRADICTS the prediction; missing history
			// only means a single boost state CANNOT VERIFY a Ditto, which
			// is not evidence against one. Treating "unable to verify" as
			// "refuted" would discard every roll-mark at its first
			// encounter, and a single-boost-state encounter says no more
			// about Ditto-ness than the roll did.
			//
			// So IsDitto and dittoRollMarked stay set, unless the values
			// below refute the mark, and scan is returned with a nil error
			// (or nil caught set in 0P) so the caller appends it to
			// scanHistory as usual. A later encounter finds history and runs
			// the evidence-based logic below, which can still reset the
			// record. Pinned by
			// TestDetectDittoKeepsRollMarkedRecordOnMissingHistory,
			// TestDetectDittoStillReturnsErrorForGenuineMissingHistory
			// (a detectDitto-confirmed Ditto with no history still errors),
			// and TestDetectDittoRollMarkedRecordWithHistoryUsesNormalDittoLogic
			// (a roll-marked record with history is unaffected).
			//
			// scan.Weather is reclassified to the DITTO's boost state before
			// returning, the same normalisation the evidence-based branches
			// below perform for a Ditto they settle. The field arrives
			// holding weather_boosted_condition, which the game reports for
			// the DISPLAYED species, while the values beside it were rolled
			// for a Normal-type Ditto — boosted only by PARTLY_CLOUDY, which
			// scan.CellWeather carries. locateAllScans files a stored scan by
			// this field alone, so leaving the disguise's value on it would
			// file a boosted observation as the unboosted one, and the next
			// encounter would compare a boosted level against an unboosted
			// expectation and reset a Ditto that nothing had contradicted.
			if rollMarkRefutedByStats(uint64(pokemon.Id), scan) {
				return pokemon.resetDittoAttributes("roll", nil, nil, scan)
			}
			if scan.CellWeather == int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
				// Unless the observation refutes it. In partly cloudy weather
				// a real Ditto is seen boosted, and a boosted roll cannot
				// produce a level at or below 5 or any IV below 4 —
				// MustBeUnboosted is exactly that test. Values like these are
				// positive evidence against the prediction, not the absence of
				// evidence this branch exists to tolerate, so the mark goes
				// rather than being filed as boosted history that later
				// encounters would then agree with.
				//
				// A species left in ditto_pool after it stopped being eligible
				// is how this arises: the roll fires on 2% of its encounters
				// and nothing else contradicts it.
				if scan.MustBeUnboosted() {
					return pokemon.resetDittoAttributes("roll", nil, nil, scan)
				}
				// The cell is partly cloudy, so the Ditto is SEEN boosted
				// either way — but PP and 0P differ in what was observed, and
				// what separates them is whether the game reported a boost for
				// the DISGUISE too. Read before the normalisation below
				// overwrites it.
				disguiseBoosted := scan.Weather == int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				if !disguiseBoosted {
					// 0P. The Ditto is caught UNBOOSTED and nothing here
					// observed those values — no history, which is why this
					// branch exists — so the honest caughtIv is none at all.
					// The caller then keeps the level adjustment and clears
					// the IVs. scan still goes to history, filed as boosted.
					return nil, nil
				}
				// PP. The weather boosts the disguise as well, so a caught
				// Ditto is boosted and these observed stats ARE the caught
				// set. Definitive; hand them back rather than subtracting five
				// levels from data the game just gave us.
			} else {
				// Symmetric refutation. Outside partly cloudy a Ditto is never
				// boosted, so an observation that can only be boosted — level
				// 31-35, which MustBeBoosted is exactly — refutes the
				// prediction as surely as a sub-floor value does inside it.
				// Filed as unboosted history instead, it becomes the
				// expectation and later encounters agree with it.
				if scan.MustBeBoosted() {
					return pokemon.resetDittoAttributes("roll", nil, nil, scan)
				}
				scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			}
			return scan, nil
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
					if knownDittoDisguise(scan) {
						if !knownDittoDisguise(matchingScan) {
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
				if knownDittoDisguise(scan) {
					if !knownDittoDisguise(matchingScan) {
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
		pokemon.downgradeEncounterSeenType()
		pokemon.SetCp(null.NewInt(0, false))
		pokemon.SetPvp(null.NewString("", false))
	}
}

// downgradeEncounterSeenType returns an encounter or lure_encounter record to
// wild or lure_wild, for when the stats an encounter observed no longer stand:
// a weather flip has cleared them, or replaced them with derived values.
func (pokemon *Pokemon) downgradeEncounterSeenType() {
	switch pokemon.SeenType.Code {
	case SeenTypeCodeLureEncounter:
		pokemon.SetSeenType(SeenTypeCodeLureWild)
	case SeenTypeCodeEncounter:
		pokemon.SetSeenType(SeenTypeCodeWild)
	}
}

// deriveIvIfAbsent fills in this pokemon's stats from its encounter id when we
// do not already have real ones.
//
// The boost branch is not inferred: PokemonDisplayProto states it and golbat
// stores it as pokemon.Weather, so the result is exact rather than estimated.
// Values are written exactly as an encounter writes them — that is deliberate,
// and it is what lets every existing consumer benefit unchanged.
//
// Callers must invoke this only after the sighting's display has already
// been applied (setPokemonDisplay / addWildPokemon), so pokemon.Weather and
// pokemon.Form hold the values THIS sighting carried rather than whatever
// was stored from a previous one. See TestUpdateFromWildDerivesUsingTheSightingsWeather.
//
// seen_type is deliberately left untouched: it records how the pokemon was
// SEEN, not what we know about it. Upgrading it to encounter/lure_encounter
// would assert a scan that never happened, and consumers read seen_type to
// judge coverage and decide what still needs scanning. See
// TestDeriveIvLeavesSeenTypeAlone.
//
// Strong Pokemon are skipped: the ordinary boosted-floor mechanic this
// function models does not apply to them (see repopulateIv's
// isBoostedMatches || isStrong branch and detectDitto's CompressedIv
// invariant across boost states for a strong scan), so deriving for one would
// write IVs indistinguishable from real ones but wrong. See
// TestDeriveIvIfAbsentSkipsStrongPokemon.
//
// A known Ditto is derived on its own boost rule rather than its disguise's
// (statsBoosted). See TestDeriveIvIfAbsentDerivesAKnownDittoOnItsOwnBranch.
func (pokemon *Pokemon) deriveIvIfAbsent() {
	// !DeriveIv is checked here as well as in applyDerivedValues, to skip the
	// generator walk rather than discard it. applyDerivedValues remains the
	// guard; this one only saves the work.
	if !config.Config.DeriveIv {
		return
	}
	if pokemon.IsStrong.ValueOrZero() {
		return
	}
	// A player-specific species has no spawn-level stats to derive. See
	// TestDeriveSkipsMinior.
	if hasPlayerSpecificStats(pokemon.PokemonId) {
		return
	}
	// IVs already settled — by an encounter, or derived on an earlier
	// sighting — are kept. A display change (species, form or costume) clears
	// size and moves but not the IVs, so those are filled on their own:
	// without this a Pokemon that changed display would carry its IVs with no
	// size or moves until a weather change or a real encounter.
	if pokemon.AtkIv.Valid {
		if !pokemon.Size.Valid || !pokemon.Move1.Valid || !pokemon.Move2.Valid {
			pokemon.deriveSizeAndMovesIfAbsent()
		}
		return
	}

	v := encounter.For(uint64(pokemon.Id), pokemon.statsBoosted(), int32(int64OrZero(pokemon.Form)))
	pokemon.applyDerivedValues(v)
}

// statsBoosted is the boost branch the record's own stats follow, read from
// the condition the game reported for the displayed species. For an ordinary
// Pokemon that is any boost at all. For a known Ditto the reported condition
// describes the disguise, and the Ditto's own stats follow dittoCaughtBoosted.
func (pokemon *Pokemon) statsBoosted() bool {
	if pokemon.IsDitto {
		return dittoCaughtBoosted(int64OrZero(pokemon.Weather))
	}
	return int64OrZero(pokemon.Weather) != int64(pogo.GameplayWeatherProto_NONE)
}

// weatherFromThisHour reports whether a weather record was received in the
// same clock hour as nowSec. The game changes weather on the hour, so a record
// from an earlier hour no longer describes the cell.
func weatherFromThisHour(weather *Weather, nowSec int64) bool {
	return weather.UpdatedMs/1000 >= nowSec-nowSec%3600
}

// dittoBoostFromObservedValues reads a Ditto's boost state off the encounter
// itself, for when the weather cache could not supply the cell's.
//
// The observed values are the Ditto's own, and a Ditto is Normal-typed — so it
// is boosted exactly when the cell is PARTLY_CLOUDY, and the derived branch its
// values match answers both questions at once.
//
// ok is false when both branches match (they coincide for this encounter id) or
// neither does (the derivation does not describe this encounter). Neither case
// supports a conclusion, and a guess here is stored as though it were observed.
//
// Form is 0 because that is what setDittoAttributes stores for a Ditto, and
// encounter.For reads form only for the Pumpkaboo/Gourgeist size override,
// which cannot apply to one.
func dittoBoostFromObservedValues(encounterId uint64, scan *pokemonScan) (boosted, ok bool) {
	observed := encounter.Values{Attack: scan.Attack, Defense: scan.Defense, Stamina: scan.Stamina, Level: scan.Level}
	hi := boostBranchValuesMatch(observed, encounter.For(encounterId, true, 0).Values)
	lo := boostBranchValuesMatch(observed, encounter.For(encounterId, false, 0).Values)
	if hi == lo {
		return false, false
	}
	return hi, true
}

// rollMarkRefutedByStats reports whether an encounter's observed stats show a
// roll-marked record is not a Ditto. A Ditto's reported stats follow its own
// boost — boosted exactly when the cell is partly cloudy — and anything
// else's follow the boost the game reports for the displayed species. Where
// the two differ, stats that match the disguise's branch are positive
// evidence against the mark: the two branches' levels always differ by five,
// so no stats match both. Where the two agree, the encounter cannot tell, and
// the mark stands.
//
// scan.Weather must still be the reported condition and scan.CellWeather the
// cell's. A cell weather inferred from these same stats by
// dittoBoostFromObservedValues assumes a Ditto, so it always agrees with the
// mark: with the cell unknown, nothing is refuted.
func rollMarkRefutedByStats(encounterId uint64, scan *pokemonScan) bool {
	dittoBoosted := scan.CellWeather == int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
	disguiseBoosted := scan.Weather != int32(pogo.GameplayWeatherProto_NONE)
	if dittoBoosted == disguiseBoosted {
		return false
	}
	observed := encounter.Values{Attack: scan.Attack, Defense: scan.Defense, Stamina: scan.Stamina, Level: scan.Level}
	return boostBranchValuesMatch(observed, encounter.For(encounterId, disguiseBoosted, 0).Values)
}

// cpInputs is what a computed CP depends on, beyond the species and form a
// display change already resets it for. CP is latched — recomputed only when
// absent — so wherever these can change and nothing reports a replacement
// CP, the caller compares a snapshot and clears CP if they moved.
type cpInputs struct {
	atk, def, sta null.Value[uint8]
	level         null.Value[uint8]
}

func (pokemon *Pokemon) cpInputs() cpInputs {
	return cpInputs{pokemon.AtkIv, pokemon.DefIv, pokemon.StaIv, pokemon.Level}
}

// hasPlayerSpecificStats reports whether a species' encounter stats belong to
// the player rather than the shared spawn. Minior's do: its IVs, level, CP,
// moves and size are neither observed onto the record nor derived for it.
func hasPlayerSpecificStats(pokemonId int16) bool {
	return pokemonId == int16(pogo.HoloPokemonId_MINIOR)
}

// caller should setPokemonDisplay prior to calling this
func (pokemon *Pokemon) addEncounterPokemon(ctx context.Context, db db.DbDetails, proto *pogo.PokemonProto, username string) {
	// Minior's encounter stats belong to the player, not the shared spawn.
	if hasPlayerSpecificStats(pokemon.PokemonId) {
		pokemon.setUsernameIfStored(username)
		return
	}
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
	// cellWeatherKnown distinguishes "scan.CellWeather is NONE because that
	// was observed" from "scan.CellWeather is NONE because nothing is known".
	// It is true when the display already reports a boost or when
	// peekWeatherRecord finds this hour's weather (even NONE); a failed
	// lookup, or a cached record from an earlier hour, leaves it false.
	cellWeatherKnown := scan.CellWeather != int32(pogo.GameplayWeatherProto_NONE)
	if !cellWeatherKnown {
		cellId := weatherCellIdFromLatLon(pokemon.Lat, pokemon.Lon)
		weather, unlock, err := peekWeatherRecord(cellId, "addEncounterPokemon")
		if weather == nil || !weather.GameplayCondition.Valid {
			log.Warnf("Failed to obtain weather for Pokemon %d: %s", pokemon.Id, err)
		} else if !weatherFromThisHour(weather, time.Now().Unix()) {
			// Weather changes on the hour, and the cache keeps an entry up to
			// an hour, so just past the hour it can hold the previous one.
			// That is not evidence about this encounter's cell: a stale CLEAR
			// would refute a roll-marked Ditto seen boosted in partly cloudy.
			// Left unknown, the observed values settle it instead (see
			// dittoBoostFromObservedValues). Compared against the processing
			// time: encounters are decoded within seconds of receipt, so this
			// can misjudge only the first seconds past the hour.
		} else {
			scan.CellWeather = int32(weather.GameplayCondition.Int64)
			cellWeatherKnown = true
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

	// Roll-marking runs on the encounter path too: an encounter can be the
	// first time a record is seen, with no sighting path having run
	// maybeMarkDittoFromRoll.
	//
	// ORDERING IS LOAD-BEARING (see maybeMarkDittoFromRoll). It must run
	// before detectDitto, so detectDitto sees dittoRollMarked and takes its
	// keep branch. It must also run before scanHistory gains this
	// encounter's scan (the append after detectDitto, below), or
	// maybeMarkDittoFromRoll's scan-history gate would always see that scan
	// and never mark on this path. Here scanHistory holds only PRIOR scans,
	// so a first-ever encounter marks and a record with prior history does
	// not. See TestAddEncounterPokemonMarksFirstEncounterOfPoolSpeciesFromRoll
	// and TestAddEncounterPokemonWithPriorHistoryDoesNotMarkFromRoll.
	pokemon.maybeMarkDittoFromRoll()

	// Snapshot the raw display before detectDitto can rewrite it: a value
	// copy, so nothing detectDitto does to scan (it mutates
	// scan.Weather/Confirmed in place, and for a Ditto caughtIv can be a
	// different, previously-stored scan) reaches it. This is what the game
	// displayed, which is what encounter.For predicts — see checkDerivation.
	observedScan := scan

	// Snapshotted after maybeMarkDittoFromRoll, so it sees a mark this call
	// just made, and before detectDitto, whose evidence-based branches can
	// clear dittoRollMarked; a read afterwards would be wrong for exactly the
	// sightings this audits. See checkDerivation's wasRollMarked parameter.
	//
	// Hydrate first. dittoRollMarked lives in GolbatInternal, so on a record
	// loaded from the database or a preserved cache it reads false until
	// populateInternal has run, and nothing else here guarantees that:
	// setPokemonDisplay skips hydration when the boost state is unchanged,
	// and maybeMarkDittoFromRoll returns at its first guard because IsDitto
	// is already true. Unhydrated, a reloaded roll-mark would be recorded as
	// an independent detectDitto confirmation, the inflation derivecheck's
	// roll_only column exists to prevent.
	pokemon.populateInternal()
	wasRollMarked := pokemon.dittoRollMarked

	// Captured before detectDitto for the same reason: it feeds
	// checkDerivation's ditto_detect log field, which needs what
	// detectDitto's locateAllScans is about to see, before this encounter's
	// scan is appended. See hadCrossBoostScanHistory.
	hadDetectEvidence := pokemon.hadCrossBoostScanHistory()

	// Snapshotted for the same reason as the two above: detectDitto runs
	// next and may clear IsDitto. A record that was a Ditto and is not one
	// afterwards has been actively REFUTED, which detectDitto can do from a
	// single prior scan through its 0N and BN branches — cases where
	// hadDetectEvidence is false, so without this they would be logged as
	// "no evidence" when they are the clearest evidence there is. That
	// under-counts false positives, the one direction that flatters the roll
	// rule.
	wasDitto := pokemon.IsDitto

	// detectDitto files a Ditto's scan by scan.CellWeather, and a cache miss
	// leaves that at its NONE default — indistinguishable from an observed
	// NONE, so a boosted Ditto would be filed as an unboosted one, and on a
	// roll-marked record with no history it would be REFUTED: the keep
	// branch reads NONE as "not partly cloudy", where a level of 31-35 is
	// impossible for a Ditto. See dittoBoostFromObservedValues for what
	// settles it instead.
	//
	// It runs for a roll-marked record whether or not derive_iv is on. The
	// mark rests on the same derivation, so consulting it again adds no
	// trust that was not already extended; withholding it turns a cache miss
	// into evidence against a prediction the operator asked for.
	if !cellWeatherKnown && pokemon.IsDitto && (config.Config.DeriveIv || wasRollMarked) {
		if boosted, ok := dittoBoostFromObservedValues(uint64(pokemon.Id), &scan); ok {
			if boosted {
				scan.CellWeather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			} else {
				scan.CellWeather = int32(pogo.GameplayWeatherProto_NONE)
			}
		}
	}

	caughtIv, err := pokemon.detectDitto(&scan)
	if err != nil {
		caughtIv = &scan
		log.Errorf("[POKEMON] Unexpected %d: %s", pokemon.Id, err)
	}
	// The encounter is definitive wherever it actually answers the question.
	// caughtIv IS that answer: detectDitto returns the Ditto you would CATCH,
	// recovered from real observations, and for anything that is not a Ditto
	// it is this encounter's own scan. So it wins whenever it exists.
	//
	// It does not always exist. In 0P the encounter reports the SEEN set —
	// boosted, because the Ditto under the disguise is Normal-typed — while
	// the Ditto you catch is unboosted, and nothing observed those values: a
	// roll-marked record has no history, which is why that branch returns no
	// caught set at all. Derivation can supply it exactly, so it does, and
	// only there.
	//
	// Failing both, the 0P answer is a level adjustment and cleared IVs: the
	// caught level is knowable from the observed one, the IVs are not.
	//
	// Only IVs and level in the derived case: size and moves came from the
	// proto above, and the encounter is the better source for both.
	switch {
	case caughtIv != nil:
		pokemon.SetLevel(null.IntFrom(int64(caughtIv.Level)))
		pokemon.calculateIv(int64(caughtIv.Attack), int64(caughtIv.Defense), int64(caughtIv.Stamina))
	case config.Config.DeriveIv && pokemon.IsDitto:
		caught := encounter.For(uint64(pokemon.Id),
			dittoCaughtBoosted(int64OrZero(pokemon.Weather)), 0).Values
		pokemon.SetLevel(null.IntFrom(int64(caught.Level)))
		pokemon.calculateIv(int64(caught.Attack), int64(caught.Defense), int64(caught.Stamina))
	default: // 0P with no derivation to fall back on
		pokemon.SetLevel(null.IntFrom(int64(scan.Level - 5)))
		pokemon.clearIv(false)
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

	// Every real encounter is a labelled test case for the derivation
	// (decoder/derive_validation.go). Runs after detectDitto so is_ditto and
	// DisplayPokemonId reflect this sighting's ground truth, but compares
	// against observedScan — captured before detectDitto ran — not the
	// (possibly Ditto-rewritten) pokemon record.
	pokemon.checkDerivation(&observedScan, int32(proto.Size), int32(proto.Move1), int32(proto.Move2), pokemon.SeenType.Code.String(), wasRollMarked, hadDetectEvidence || (wasDitto && !pokemon.IsDitto))

	// Normally a no-op: it returns immediately once Cp is set, which the proto
	// almost always does. It earns its place when the proto did not report one
	// — the encounter's own IVs and level are right there, and nothing else on
	// this path would ever fill it. nil weather because the map is read only
	// by the Ditto arm, which falls through to the record's own values.
	pokemon.recomputeCpIfNeeded(ctx, db, nil)

	pokemon.setUsernameIfStored(username)
}

// updatePokemonFromEncounterProto applies an encounter. It returns false,
// with the record untouched, when the encounter's wild proto cannot be
// placed (see addWildPokemon); the caller must then skip the save.
func (pokemon *Pokemon) updatePokemonFromEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.EncounterOutProto, username string, timestampMs int64) bool {
	if !pokemon.addWildPokemon(ctx, db, encounterData.Pokemon, timestampMs, false) {
		return false
	}
	pokemon.SetIsEvent(0)
	// A tappable encounter also shows up as a normal encounter once tapped.
	// Downgrading its seen type to a plain encounter would lose the tappable
	// attribution, so only a pokemon that was NOT seen from a tappable is
	// rewritten here.
	if !pokemon.isSeenFromTappable() {
		pokemon.SetSeenType(SeenTypeCodeEncounter)
	}
	pokemon.addEncounterPokemon(ctx, db, encounterData.Pokemon.Pokemon, username)

	if !pokemon.CellId.Valid {
		centerCoord := s2.LatLngFromDegrees(pokemon.Lat, pokemon.Lon)
		cellID := s2.CellIDFromLatLng(centerCoord).Parent(15)
		pokemon.SetCellId(null.IntFrom(int64(cellID)))
	}
	return true
}

// encounteredSeenType reports whether a seen type records a real encounter.
func encounteredSeenType(code SeenTypeCode) bool {
	switch code {
	case SeenTypeCodeEncounter, SeenTypeCodeLureEncounter,
		SeenTypeCodeTappableEncounter, SeenTypeCodeTappableLureEncounter:
		return true
	}
	return false
}

// isSeenFromTappable reports whether the pokemon's seen type is one of the
// two tappable ones. Its call site overwrites the seen type when this is
// FALSE, so tappable attribution depends on the sense here being read
// correctly.
func (pokemon *Pokemon) isSeenFromTappable() bool {
	return pokemon.SeenType.Code == SeenTypeCodeTappableEncounter || pokemon.SeenType.Code == SeenTypeCodeTappableLureEncounter
}

func (pokemon *Pokemon) updatePokemonFromDiskEncounterProto(ctx context.Context, db db.DbDetails, encounterData *pogo.DiskEncounterOutProto, username string) {
	pokemon.SetIsEvent(0)
	pokemon.setPokemonDisplay(int16(encounterData.Pokemon.PokemonId), encounterData.Pokemon.PokemonDisplay)
	pokemon.SetSeenType(SeenTypeCodeLureEncounter)
	pokemon.addEncounterPokemon(ctx, db, encounterData.Pokemon, username)
}

func (pokemon *Pokemon) updatePokemonFromTappableEncounterProto(ctx context.Context, db db.DbDetails, request *pogo.ProcessTappableProto, encounterData *pogo.TappableEncounterProto, username string, timestampMs int64) {
	pokemon.SetIsEvent(0)
	pokemon.SetLat(request.LocationHintLat)
	pokemon.SetLon(request.LocationHintLng)

	if spawnpointId := request.GetLocation().GetSpawnpointId(); spawnpointId != "" {
		pokemon.SetSeenType(SeenTypeCodeTappableEncounter)

		spawnId, err := strconv.ParseInt(spawnpointId, 16, 64)
		if err != nil {
			panic(err)
		}

		pokemon.SetSpawnId(null.IntFrom(spawnId))
		pokemon.setExpireTimestampFromSpawnpoint(ctx, db, timestampMs, false)
	} else if fortIdStr := request.GetLocation().GetFortId(); fortIdStr != "" {
		pokemon.SetSeenType(SeenTypeCodeTappableLureEncounter)

		if fortId, ok := ParseFortId(fortIdStr); ok {
			pokemon.SetPokestopId(fortId)
		} else {
			FortIdParseDrops.Report(func(dropped int64) {
				log.Errorf("[POKEMON] dropped %d unparseable tappable-lure-encounter fort id(s) in the last second (most recently pokemon %d, id %q)",
					dropped, pokemon.Id, fortIdStr)
			})
		}
		// we don't know any despawn times from lured/fort tappables
		pokemon.SetExpireTimestamp(null.IntFrom(int64(timestampMs)/1000 + int64(120)))
		pokemon.SetExpireTimestampVerified(false)
	}
	pokemon.setPokemonDisplay(int16(encounterData.Pokemon.PokemonId), encounterData.Pokemon.PokemonDisplay)
	// addEncounterPokemon adopts the username at the end of its own updates.
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
		// Narrow the incoming display.* readings the same way the stored
		// fields were narrowed, so the two sides can actually converge: a
		// costume of 300 is stored as 255, and comparing 255 against a raw
		// 300 would report a change on every sighting forever — wiping the
		// encounter data below each time. narrowUint8/16 saturate without
		// counting, so SetForm/SetCostume/SetGender below stay the only
		// place golbat_field_clamped_total is incremented.
		formChanged := !oldForm.Valid || int64OrZero(oldForm) != narrowUint16(int64(display.Form))
		costumeChanged := !pokemon.Costume.Valid || int64OrZero(pokemon.Costume) != narrowUint8(int64(display.Costume))
		genderChanged := !pokemon.Gender.Valid || int64OrZero(pokemon.Gender) != narrowUint8(int64(display.Gender))
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
			// A Ditto's IVs and level were taken on the Ditto's own boost rule
			// (boosted only in PP), which is not the new identity's: in any
			// other boosting weather the two branches differ. With derivation
			// on they are cleared so the sighting derives the new identity's;
			// with it off there is nothing to replace them, and they are kept.
			if pokemon.IsDitto && config.Config.DeriveIv {
				pokemon.clearIv(false)
				pokemon.SetLevel(null.NewInt(0, false))
			}
			// IVs and level otherwise survive a display change, so a spawn
			// first seen (and derived or encountered) as another species that
			// now displays as Minior would keep that identity's stats, and
			// have a Minior CP recomputed from them. Minior's stats are the
			// player's, so they are dropped rather than carried over.
			if hasPlayerSpecificStats(pokemonId) {
				pokemon.clearIv(true)
				pokemon.SetLevel(null.NewInt(0, false))
			}
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
		// This passes the DISPLAY's reported condition, which is also what
		// repopulateIv's Ditto arm expects: it resolves the caught boost state
		// through dittoCaughtBoosted, which needs no cell weather.
		pokemon.repopulateIv(int64(display.WeatherBoostedCondition), display.IsStrongPokemon)
	}
	// The DISPLAY's weather is what gets stored, for a Ditto as for anything
	// else: the record's weather and CP describe what a map shows — the
	// disguise — while its IVs and level describe what you actually catch,
	// which for a Ditto in PARTLY_CLOUDY is boosted. The two halves answer
	// different questions and deliberately disagree.
	//
	// recomputeCpIfNeeded depends on this: its Ditto arm resolves cell weather
	// only when the stored weather is NONE, and computes CP for
	// DisplayPokemonId. Storing the Ditto's own boost state here would skip
	// that and produce CP for the wrong thing.
	pokemon.SetWeather(null.IntFrom(int64(display.WeatherBoostedCondition)))
	pokemon.SetIsStrong(null.BoolFrom(display.IsStrongPokemon))
}

func (pokemon *Pokemon) repopulateIv(weather int64, isStrong bool) {
	// Do not restore player-specific Minior stats from an earlier encounter,
	// nor derive them for the new weather.
	if hasPlayerSpecificStats(pokemon.PokemonId) {
		return
	}
	// pokemon.Weather holds the narrowed reading, so narrow the incoming one
	// before deriving isBoosted from it (see narrowUint8). Out of range, the
	// raw reading and the value that actually gets stored can classify
	// differently, and the two would then disagree on every sighting.
	weather = narrowUint8(weather)
	var isBoosted bool
	if !pokemon.IsDitto {
		isBoosted = weather != int64(pogo.GameplayWeatherProto_NONE)
		if isStrong == pokemon.IsStrong.ValueOrZero() &&
			int64OrZero(pokemon.Weather) != int64(pogo.GameplayWeatherProto_NONE) == isBoosted {
			return
		}
	} else if isStrong {
		log.Errorf("Strong Ditto??? I can't handle this fml %d", pokemon.Id)
		pokemon.clearIv(true)
		return
	} else {
		// Both sides read the same quantity, the reported condition: stored on
		// the record by the previous sighting, and passed in by this one.
		isBoosted = dittoCaughtBoosted(weather)
		if dittoCaughtBoosted(int64OrZero(pokemon.Weather)) == isBoosted {
			return
		}
	}
	matchingScan, isBoostedMatches := pokemon.locateScan(isStrong, isBoosted)
	// locateScan does not return nil when it has only the OTHER boost state's
	// scan — it returns that one with isBoostedMatches false. For an ordinary
	// Pokemon that is not a scan for this state at all: the game rolls
	// different IVs and a different level per boost state, which is the whole
	// reason this function has work to do. Treating it as a usable scan sends
	// the flip down the branch below, which clears the IVs and keeps only a
	// ±5 level estimate — while the derivation could supply both exactly.
	//
	// A strong Pokemon is the exception and keeps the fallback: its stats do
	// not move with weather, so the other state's scan IS its scan.
	usableScan := matchingScan != nil && (isBoostedMatches || isStrong)
	var oldAtk, oldDef, oldSta int64
	if !usableScan && config.Config.DeriveIv && !isStrong {
		// Derive for the state actually being asked about — the encounter id
		// is unchanged and isBoosted above already accounts for Ditto.
		//
		// Strong Pokemon are excluded: a strong Pokemon's IVs are unaffected
		// by weather (see the isBoostedMatches || isStrong branch below, and
		// detectDitto's identical CompressedIv requirement across boost states
		// for a strong scan), so the ordinary boosted-floor derivation in
		// encounter.For would write IVs that cannot occur for one. Whether a
		// strong Pokemon's true values are the unboosted derivation with the
		// level shift applied separately is an open question. Until it is
		// answered, no data beats wrong data.
		//
		// derive_iv gates the branch rather than only applyDerivedValues:
		// with derivation off that call writes nothing, and this arm would
		// then clear CP and PVP alone, leaving the PREVIOUS boost state's IVs
		// and level valid for the new one.
		//
		// The encounter's stats no longer stand, so an encounter record goes
		// back to wild, as it does when the stats are cleared.
		v := encounter.For(uint64(pokemon.Id), isBoosted, int32(int64OrZero(pokemon.Form)))
		pokemon.applyDerivedValues(v)
		pokemon.downgradeEncounterSeenType()
		pokemon.SetCp(null.NewInt(0, false))
		pokemon.SetPvp(null.NewString("", false))
	} else if matchingScan == nil {
		// Nothing stored and nothing derivable: clear, which is what
		// derivation-off keeps.
		pokemon.SetLevel(null.NewInt(0, false))
		pokemon.clearIv(true)
	} else {
		oldLevel := int64OrZero(pokemon.Level)
		if pokemon.AtkIv.Valid {
			oldAtk = int64OrZero(pokemon.AtkIv)
			oldDef = int64OrZero(pokemon.DefIv)
			oldSta = int64OrZero(pokemon.StaIv)
		} else {
			oldAtk = -1
			oldDef = -1
			oldSta = -1
		}
		newLevel := int64(matchingScan.Level)
		if isBoostedMatches || isStrong { // strong Pokemon IV is unaffected by weather
			pokemon.calculateIv(int64(matchingScan.Attack), int64(matchingScan.Defense), int64(matchingScan.Stamina))
			switch pokemon.SeenType.Code {
			case SeenTypeCodeLureWild:
				pokemon.SetSeenType(SeenTypeCodeLureEncounter)
			case SeenTypeCodeWild:
				pokemon.SetSeenType(SeenTypeCodeEncounter)
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
		// oldLevel was read from the narrowed field, so narrow newLevel too
		// (see narrowUint8) — otherwise a level pushed out of range by the
		// boost adjustment would clear CP and PVP on every sighting.
		if narrowUint8(newLevel) != oldLevel || pokemon.AtkIv.Valid &&
			(int64OrZero(pokemon.AtkIv) != oldAtk || int64OrZero(pokemon.DefIv) != oldDef || int64OrZero(pokemon.StaIv) != oldSta) {
			pokemon.SetCp(null.NewInt(0, false))
			pokemon.SetPvp(null.NewString("", false))
		}
	}
}

// cellWeatherMissDrops rate-limits recomputeCpIfNeeded's weather-lookup
// warning. The batch weather map is consulted first and normally answers; this
// covers the fallback that takes the weather record lock.
var cellWeatherMissDrops util.DropReporter

func (pokemon *Pokemon) recomputeCpIfNeeded(ctx context.Context, db db.DbDetails, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition) {
	o := ohbem.Load()
	if pokemon.Cp.Valid || o == nil {
		return
	}
	var displayPokemon int
	var displayPokemonForm int
	shouldOverrideIv := false
	var overrideIv *pokemonScan
	if pokemon.IsDitto {
		displayPokemon = int(pokemon.DisplayPokemonId.ValueOrZero())
		displayPokemonForm = int(pokemon.DisplayPokemonForm.ValueOrZero())
		if int64OrZero(pokemon.Weather) == int64(pogo.GameplayWeatherProto_NONE) {
			cellId := weatherCellIdFromLatLon(pokemon.Lat, pokemon.Lon)
			cellWeather, found := weather[cellId]
			if !found {
				record, unlock, err := getWeatherRecordReadOnly(ctx, db, cellId, "recomputeCpIfNeeded")
				if err != nil || record == nil || !record.GameplayCondition.Valid {
					// Rate-limited: a roll-marked Ditto reaches this arm from
					// every GMO batch, so a cell whose weather is not held
					// locally would otherwise warn once per sighting.
					cellWeatherMissDrops.Report(func(dropped int64) {
						log.Warnf("[POKEMON] %d Ditto CP recomputes could not obtain cell weather, most recently Pokemon %d at cell %d: %v",
							dropped, pokemon.Id, cellId, err)
					})
				} else {
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
	var atk, def, sta int
	var level float64
	// overrideIv != nil, not just shouldOverrideIv: a roll-marked Ditto has no
	// scan history by construction, so in 0P the unboosted observation this
	// arm looks for does not exist. Falling through to the record's own values
	// is correct, not merely a fallback: they are the CAUGHT set, which in 0P
	// is the unboosted one — the values overrideIv would have supplied.
	if shouldOverrideIv && overrideIv != nil {
		// You should see boosted IV for 0P Ditto
		atk, def, sta = int(overrideIv.Attack), int(overrideIv.Defense), int(overrideIv.Stamina)
		level = float64(overrideIv.Level)
	} else {
		if !pokemon.AtkIv.Valid || !pokemon.Level.Valid {
			return
		}
		atk, def, sta = int(pokemon.AtkIv.ValueOrZero()), int(pokemon.DefIv.ValueOrZero()), int(pokemon.StaIv.ValueOrZero())
		level = float64(pokemon.Level.ValueOrZero())
	}
	// Level 0 is not a level, and ohbem does not treat it as one: it indexes
	// its CP multiplier table at level*2-2 and panics with "index out of range
	// [-2]". Nothing in the decode path recovers, so this would take the
	// goroutine down rather than lose one CP.
	//
	// Zero is reachable without anything looking wrong. SetLevel clamps into
	// uint8, so any adjustment below 1 — addEncounterPokemon's scan.Level-5 on
	// the 0P arm, repopulateIv's newLevel -= 5, or a cp_multiplier low enough
	// that cpMultiplierToLevel itself goes negative — stores a VALID 0 rather
	// than an absent one, and passes the Valid checks above.
	if level < 1 {
		return
	}
	cp, err := o.CalculateCp(displayPokemon, displayPokemonForm, 0, atk, def, sta, level)
	if err == nil {
		pokemon.SetCp(null.IntFrom(int64(cp)))
	} else {
		log.Warnf("Pokemon %d %d CP unset due to error %s", pokemon.Id, displayPokemon, err)
	}
}
