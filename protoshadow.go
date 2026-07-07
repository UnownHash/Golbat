package main

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"

	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/pogoshim"
)

// maybeShadow samples a fraction of packets for a given method and, when the
// live engine for that method is hyperpb, decodes the same payload again
// with the std (protobuf-go) engine and compares a field-fold digest between
// the two. Divergence means the hyperpb decode path silently produced
// different data than protobuf-go would have for the same bytes -- exactly
// the class of bug that must never reach the live decode path unnoticed.
//
// This runs inline on the decode goroutine (bounded by raw-processing
// concurrency); at the default ~1% sample rate the extra decode cost is
// negligible against the win of catching a hyperpb regression before it
// ships to 100%.
func maybeShadow(method string, payload []byte) {
	if engineFor(method) != "hyperpb" {
		return
	}
	if rand.Float64() >= config.Config.ProtoEngine.ShadowSampleRate {
		return
	}
	if shadowCompare(method, payload) {
		if statsCollector != nil {
			statsCollector.IncProtoShadow(method, "match")
		}
		return
	}
	if statsCollector != nil {
		statsCollector.IncProtoShadow(method, "mismatch")
	}
	dumped := dumpShadowMismatch(method, "data", payload)
	log.Errorf("[PROTO_SHADOW] digest mismatch method=%s payload_len=%d dump=%s", method, len(payload), dumped)
}

// Mismatch payloads are the only way to debug an engine divergence offline,
// so the first few per process are written to shadowMismatchDir. Bounded so
// a systematic divergence on a high-volume method cannot fill the disk.
const (
	shadowMismatchDir      = "shadow_mismatch"
	shadowMismatchMaxFiles = 25
)

var shadowMismatchCount atomic.Int64

// dumpShadowMismatch writes payload to shadow_mismatch/ and returns the
// path (or a reason it was skipped). kind distinguishes the request and
// data halves of pair methods.
func dumpShadowMismatch(method, kind string, payload []byte) string {
	n := shadowMismatchCount.Add(1)
	if n > shadowMismatchMaxFiles {
		return "skipped(cap)"
	}
	if err := os.MkdirAll(shadowMismatchDir, 0o755); err != nil {
		return "skipped(" + err.Error() + ")"
	}
	path := filepath.Join(shadowMismatchDir,
		fmt.Sprintf("%s_%s_%d_%d.bin", method, kind, time.Now().UnixMilli(), len(payload)))
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return "skipped(" + err.Error() + ")"
	}
	return path
}

// shadowCompare decodes payload with both the std and hyperpb engines and
// reports whether their field digests agree. It is a pure function (no
// stats/logging side effects) so tests can assert the core correctness
// property directly: for any well-formed payload this must return true.
//
// gmo/encounter/disk_encounter keep hand-written digest functions (below);
// every other method falls back to digestMessageGeneric (protodigest.go) via
// genericShadowEngine, which maps a method to the single *protoEngineHandle
// its digest should run against. genericShadowEngine only covers methods
// with exactly one wire proto to verify -- multi-proto methods (request+data
// pairs, "social"'s nested payloads) are wired in by the task that adds
// their decode.go call site, either by extending genericShadowEngine (if a
// single proto is representative) or by adding a bespoke case here. For a
// genuine request+data pair (not just "one proto is representative"),
// maybeShadow/shadowCompare aren't called at all -- see maybeShadowPair/
// shadowComparePair/compareDigestPair below ("Composite (request+data) shadow
// verification"), open_invasion's decode.go call site, and Task 4's other
// request+data methods, which follow the same pattern.
func shadowCompare(method string, payload []byte) bool {
	switch method {
	case engMethodGmo:
		return compareDigest(gmoEngine, payload, pogoshim.AsGetMapObjectsOutProto, digestGmo)
	case engMethodEncounter:
		return compareDigest(encounterEngine, payload, pogoshim.AsEncounterOutProto, digestEncounter)
	case engMethodDiskEncounter:
		return compareDigest(diskEncounterEngine, payload, pogoshim.AsDiskEncounterOutProto, digestDiskEncounter)
	default:
		eng := genericShadowEngine(method)
		if eng == nil {
			return true
		}
		return compareDigest(eng, payload, identityWrap, digestGenericAdapter)
	}
}

// genericShadowEngine maps a method key to the single *protoEngineHandle
// its shadow digest should decode, for methods with exactly one wire proto
// (no hand-written digest). Returns nil for anything else -- including a
// method with no handle wired here yet -- so shadowCompare's default case
// is a safe no-op (matching its pre-Wave-3 behavior of skipping verification
// for any method it doesn't recognize). A method with a genuine request+data
// pair (rather than one representative proto) belongs in shadowComparePair's
// switch instead -- see the "Composite (request+data) shadow verification"
// section below.
func genericShadowEngine(method string) *protoEngineHandle {
	switch method {
	case engMethodFortDetails:
		return fortDetailsEngine
	case engMethodGymInfo:
		return gymInfoEngine
	case engMethodQuest:
		return questEngine
	case engMethodGetMapForts:
		return mapFortsEngine
	case engMethodRoutes:
		return routesEngine
	case engMethodStartIncident:
		return startIncidentEngine
	case engMethodNebulaBattleState:
		return battleStateEngine
	case engMethodEventRsvpCount:
		return rsvpCountEngine
	default:
		return nil
	}
}

// --- Composite (request+data) shadow verification ---------------------------
//
// Some methods carry a request proto and a data/response proto as two
// independent wire payloads sharing one config method key -- open_invasion
// (OpenInvasionCombatSessionProto + OpenInvasionCombatSessionOutProto), and
// Task 4's contest_data, size_contest_entry, station_details, tappable,
// event_rsvps, and social (Request+Response only -- see its switch case
// below). genericShadowEngine can't express this: it
// maps a method to exactly ONE *protoEngineHandle. maybeShadowPair/
// shadowComparePair/compareDigestPair below are the composite escape hatch --
// this is the "composite note" referenced by shadowCompare's and
// genericShadowEngine's doc comments. The pattern: decode both payloads
// through both engines (std once, hyperpb once) and fold digestMessageGeneric
// for BOTH messages into the SAME hash.Hash64 per engine (request's fields,
// then data's), so one equality check catches a divergence in either half.
// Adding a future method here is a new case in shadowComparePair's switch --
// compareDigestPair itself is method-agnostic.

// maybeShadowPair is maybeShadow's two-payload counterpart: same sampling
// and engine-gating logic, but for a method whose payload is a (request,
// data) pair rather than a single proto.
func maybeShadowPair(method string, request, data []byte) {
	if engineFor(method) != "hyperpb" {
		return
	}
	if rand.Float64() >= config.Config.ProtoEngine.ShadowSampleRate {
		return
	}
	if shadowComparePair(method, request, data) {
		if statsCollector != nil {
			statsCollector.IncProtoShadow(method, "match")
		}
		return
	}
	if statsCollector != nil {
		statsCollector.IncProtoShadow(method, "mismatch")
	}
	dumpedReq := dumpShadowMismatch(method, "request", request)
	dumpedData := dumpShadowMismatch(method, "data", data)
	log.Errorf("[PROTO_SHADOW] digest mismatch method=%s request_len=%d payload_len=%d dump_request=%s dump_data=%s",
		method, len(request), len(data), dumpedReq, dumpedData)
}

// shadowComparePair maps method to the request+data handle pair it should
// verify. The default (true, a safe no-op) mirrors shadowCompare's default
// case for any method not wired in here yet.
func shadowComparePair(method string, request, data []byte) bool {
	switch method {
	case engMethodOpenInvasion:
		return compareDigestPair(openInvasionReqEngine, request, openInvasionEngine, data)
	case engMethodContestData:
		return compareDigestPair(contestDataReqEngine, request, contestDataEngine, data)
	case engMethodSizeContestEntry:
		return compareDigestPair(sizeEntryReqEngine, request, sizeEntryEngine, data)
	case engMethodStationDetails:
		return compareDigestPair(stationDetailsReqEngine, request, stationDetailsEngine, data)
	case engMethodTappable:
		return compareDigestPair(tappableReqEngine, request, tappableEngine, data)
	case engMethodEventRsvps:
		return compareDigestPair(rsvpReqEngine, request, rsvpEngine, data)
	case engMethodSocial:
		// Composite covers Request (ProxyRequestProto) + Data
		// (ProxyResponseProto) only. The response's Payload bytes decode as
		// one of several inner proto types chosen by the request's action
		// type (InternalGetFriendDetailsOutProto vs
		// InternalSearchPlayerOutProto+InternalSearchPlayerProto) -- shadow
		// verifying those would mean re-implementing the same action-type
		// dispatch decodeSocialActionWithRequest already does, and an action
		// type this switch doesn't recognize is a normal "Did not process"
		// outcome, not a divergence to catch. Request+Response is the pair
		// that's genuinely comparable between engines without that
		// ambiguity; the two inner payload types are an accepted shadow
		// coverage gap (see decodeSocialActionWithRequest's doc comment).
		return compareDigestPair(proxyReqEngine, request, proxyRespEngine, data)
	default:
		return true
	}
}

// compareDigestPair decodes request via reqEng and data via dataEng, once
// with decodeStd and once with decodeHyperpb, folding digestMessageGeneric
// for both messages into a single hash.Hash64 per engine (request first,
// then data), and compares the two engines' combined digests. Both messages
// share the identity wrap (protoreflect.Message) since neither has a
// hand-written digest -- the generic descriptor-walk digest is exactly what
// genericShadowEngine's single-proto path already uses.
func compareDigestPair(reqEng *protoEngineHandle, request []byte, dataEng *protoEngineHandle, data []byte) bool {
	method := ""
	if reqEng != nil {
		method = reqEng.method
	}

	fold := func(decode func(*protoEngineHandle, []byte, func(protoreflect.Message) protoreflect.Message, func(protoreflect.Message) string) (string, error)) (uint64, error) {
		h := fnv.New64a()
		process := func(m protoreflect.Message) string {
			digestMessageGeneric(h, m)
			return ""
		}
		if _, err := decode(reqEng, request, identityWrap, process); err != nil {
			return 0, err
		}
		if _, err := decode(dataEng, data, identityWrap, process); err != nil {
			return 0, err
		}
		return h.Sum64(), nil
	}

	stdDigest, err := fold(decodeStd[protoreflect.Message])
	if err != nil {
		log.Errorf("[PROTO_SHADOW] std decode failed method=%s request_len=%d payload_len=%d err=%s", method, len(request), len(data), err)
		return false
	}
	hyperDigest, err := fold(decodeHyperpb[protoreflect.Message])
	if err != nil {
		log.Errorf("[PROTO_SHADOW] hyperpb decode failed method=%s request_len=%d payload_len=%d err=%s", method, len(request), len(data), err)
		return false
	}
	return stdDigest == hyperDigest
}

// identityWrap is the "wrap" function for the generic-digest shadow path:
// digestMessageGeneric operates directly on protoreflect.Message, so there is
// no pogoshim type to wrap into.
func identityWrap(m protoreflect.Message) protoreflect.Message { return m }

// digestGenericAdapter folds m through digestMessageGeneric into a single
// uint64, matching the (T) uint64 shape compareDigest expects from a
// hand-written digest function.
func digestGenericAdapter(m protoreflect.Message) uint64 {
	h := fnv.New64a()
	digestMessageGeneric(h, m)
	return h.Sum64()
}

// compareDigest decodes payload once via decodeStd and once via
// decodeHyperpb (the same arena/pool machinery the live hyperpb path uses),
// folding each parse through the identical digest function, and compares
// the results.
func compareDigest[T any](eng *protoEngineHandle, payload []byte, wrap func(protoreflect.Message) T, digest func(T) uint64) bool {
	process := func(v T) string { return strconv.FormatUint(digest(v), 16) }

	method := ""
	if eng != nil {
		method = eng.method
	}

	stdDigest, err := decodeStd(eng, payload, wrap, process)
	if err != nil {
		log.Errorf("[PROTO_SHADOW] std decode failed method=%s payload_len=%d err=%s", method, len(payload), err)
		return false
	}
	hyperDigest, err := decodeHyperpb(eng, payload, wrap, process)
	if err != nil {
		log.Errorf("[PROTO_SHADOW] hyperpb decode failed method=%s payload_len=%d err=%s", method, len(payload), err)
		return false
	}
	return stdDigest == hyperDigest
}

// --- FNV-1a fold primitives -------------------------------------------------
//
// Every fold writes a small integer "tag" identifying which field is being
// folded, then the field's value, so that (a) an all-zero-value message
// still produces a non-trivial digest and (b) two fields that happen to
// share a value can't be swapped without changing the digest. Floats are
// folded via their raw bits (no arithmetic), strings via their length
// followed by their raw bytes (removing tag/value ambiguity), and repeated
// fields fold their length before their elements. Before descending into
// any singular (optional) message field, its Has<Field>() presence bit is
// folded first, so an absent submessage and a present-but-all-default one
// produce different digests.

func foldU64(h hash.Hash64, tag int, v uint64) {
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], uint64(tag))
	binary.LittleEndian.PutUint64(buf[8:16], v)
	_, _ = h.Write(buf[:])
}

func foldI64(h hash.Hash64, tag int, v int64) { foldU64(h, tag, uint64(v)) }

func foldBool(h hash.Hash64, tag int, v bool) {
	var u uint64
	if v {
		u = 1
	}
	foldU64(h, tag, u)
}

func foldF32(h hash.Hash64, tag int, v float32) {
	foldU64(h, tag, uint64(math.Float32bits(v)))
}

func foldF64(h hash.Hash64, tag int, v float64) {
	foldU64(h, tag, math.Float64bits(v))
}

func foldStr(h hash.Hash64, tag int, s string) {
	foldLen(h, tag, len(s))
	_, _ = h.Write([]byte(s))
}

func foldLen(h hash.Hash64, tag int, n int) { foldU64(h, tag, uint64(n)) }

// foldBytes mirrors foldStr's length-prefix-then-raw-bytes shape for a
// []byte field (used by digestMessageGeneric's BytesKind case; none of the
// hand-written digests above currently fold a bytes field).
func foldBytes(h hash.Hash64, tag int, b []byte) {
	foldLen(h, tag, len(b))
	_, _ = h.Write(b)
}

// --- Message-level digest folds ---------------------------------------------

func digestPokemonDisplay(h hash.Hash64, d pogoshim.PokemonDisplayProto) {
	foldI64(h, 1, int64(d.GetForm()))
	foldI64(h, 2, int64(d.GetCostume()))
	foldI64(h, 3, int64(d.GetGender()))
	foldBool(h, 4, d.GetShiny())
	foldI64(h, 5, int64(d.GetWeatherBoostedCondition()))
	foldBool(h, 6, d.GetIsStrongPokemon())
	foldI64(h, 7, int64(d.GetCurrentTempEvolution()))
	foldI64(h, 8, d.GetTemporaryEvolutionFinishMs())
	foldI64(h, 9, int64(d.GetAlignment()))
	foldI64(h, 10, int64(d.GetPokemonBadge()))
	foldBool(h, 11, d.HasLocationCard())
	if d.HasLocationCard() {
		foldI64(h, 12, int64(d.GetLocationCard().GetLocationCard()))
	}
	foldI64(h, 13, int64(d.GetBreadModeEnum()))
}

func digestPokemon(h hash.Hash64, p pogoshim.PokemonProto) {
	foldU64(h, 20, p.GetId())
	foldI64(h, 21, int64(p.GetPokemonId()))
	foldI64(h, 22, int64(p.GetCp()))
	foldI64(h, 23, int64(p.GetMove1()))
	foldI64(h, 24, int64(p.GetMove2()))
	foldF32(h, 25, p.GetHeightM())
	foldF32(h, 26, p.GetWeightKg())
	foldI64(h, 27, int64(p.GetIndividualAttack()))
	foldI64(h, 28, int64(p.GetIndividualDefense()))
	foldI64(h, 29, int64(p.GetIndividualStamina()))
	foldF32(h, 30, p.GetCpMultiplier())
	foldI64(h, 31, int64(p.GetSize()))
	foldI64(h, 32, int64(p.GetStamina()))
	foldBool(h, 33, p.HasPokemonDisplay())
	if p.HasPokemonDisplay() {
		digestPokemonDisplay(h, p.GetPokemonDisplay())
	}
}

func digestCaptureProbability(h hash.Hash64, c pogoshim.CaptureProbabilityProto) {
	balls := c.GetPokeballType()
	foldLen(h, 40, balls.Len())
	for i := 0; i < balls.Len(); i++ {
		foldI64(h, 41, int64(balls.At(i).Enum()))
	}
	probs := c.GetCaptureProbability()
	foldLen(h, 42, probs.Len())
	for i := 0; i < probs.Len(); i++ {
		foldF32(h, 43, float32(probs.At(i).Float()))
	}
	foldF64(h, 44, c.GetReticleDifficultyScale())
}

func digestWild(h hash.Hash64, w pogoshim.WildPokemonProto) {
	foldU64(h, 50, w.GetEncounterId())
	foldI64(h, 51, w.GetLastModifiedMs())
	foldF64(h, 52, w.GetLatitude())
	foldF64(h, 53, w.GetLongitude())
	foldStr(h, 54, w.GetSpawnPointId())
	foldI64(h, 55, int64(w.GetTimeTillHiddenMs()))
	foldBool(h, 56, w.HasPokemon())
	if w.HasPokemon() {
		digestPokemon(h, w.GetPokemon())
	}
}

func digestNearby(h hash.Hash64, n pogoshim.NearbyPokemonProto) {
	foldI64(h, 60, int64(n.GetPokedexNumber()))
	foldF32(h, 61, n.GetDistanceMeters())
	foldU64(h, 62, n.GetEncounterId())
	foldStr(h, 63, n.GetFortId())
	foldStr(h, 64, n.GetFortImageUrl())
	foldBool(h, 65, n.HasPokemonDisplay())
	if n.HasPokemonDisplay() {
		digestPokemonDisplay(h, n.GetPokemonDisplay())
	}
}

func digestMapPokemon(h hash.Hash64, m pogoshim.MapPokemonProto) {
	foldStr(h, 70, m.GetSpawnpointId())
	foldU64(h, 71, m.GetEncounterId())
	foldI64(h, 72, int64(m.GetPokedexTypeId()))
	foldI64(h, 73, m.GetExpirationTimeMs())
	foldF64(h, 74, m.GetLatitude())
	foldF64(h, 75, m.GetLongitude())
	foldBool(h, 76, m.HasPokemonDisplay())
	if m.HasPokemonDisplay() {
		digestPokemonDisplay(h, m.GetPokemonDisplay())
	}
}

func digestIncidentDisplay(h hash.Hash64, p pogoshim.PokestopIncidentDisplayProto) {
	foldStr(h, 80, p.GetIncidentId())
	foldI64(h, 81, p.GetIncidentStartMs())
	foldI64(h, 82, p.GetIncidentExpirationMs())
	foldI64(h, 83, int64(p.GetIncidentDisplayType()))
	foldBool(h, 86, p.HasCharacterDisplay())
	if p.HasCharacterDisplay() {
		cd := p.GetCharacterDisplay()
		foldI64(h, 84, int64(cd.GetStyle()))
		foldI64(h, 85, int64(cd.GetCharacter()))
	}
}

func digestRaidInfo(h hash.Hash64, r pogoshim.RaidInfoProto) {
	foldI64(h, 90, r.GetRaidEndMs())
	foldI64(h, 91, r.GetRaidSpawnMs())
	foldI64(h, 92, r.GetRaidSeed())
	foldI64(h, 93, r.GetRaidBattleMs())
	foldI64(h, 94, int64(r.GetRaidLevel()))
	foldBool(h, 95, r.HasRaidPokemon())
	if r.HasRaidPokemon() {
		digestPokemon(h, r.GetRaidPokemon())
	}
}

func digestFort(h hash.Hash64, f pogoshim.PokemonFortProto) {
	foldStr(h, 110, f.GetFortId())
	foldF64(h, 111, f.GetLatitude())
	foldF64(h, 112, f.GetLongitude())
	foldI64(h, 113, int64(f.GetFortType()))
	foldI64(h, 114, int64(f.GetTeam()))
	foldBool(h, 115, f.GetEnabled())
	foldI64(h, 116, int64(f.GetSponsor()))
	foldBool(h, 117, f.GetIsArScanEligible())
	foldI64(h, 118, int64(f.GetPowerUpProgressPoints()))
	foldI64(h, 119, f.GetPowerUpLevelExpirationMs())
	foldI64(h, 120, f.GetLastModifiedMs())

	mods := f.GetActiveFortModifier()
	foldLen(h, 121, mods.Len())
	for i := 0; i < mods.Len(); i++ {
		foldI64(h, 122, int64(mods.At(i).Enum()))
	}

	foldStr(h, 123, f.GetImageUrl())
	foldStr(h, 124, f.GetPartnerId())
	foldBool(h, 125, f.GetIsInBattle())
	foldI64(h, 126, int64(f.GetGuardPokemonId()))
	foldBool(h, 132, f.HasGuardPokemonDisplay())
	if f.HasGuardPokemonDisplay() {
		digestPokemonDisplay(h, f.GetGuardPokemonDisplay())
	}

	foldBool(h, 133, f.HasGymDisplay())
	if f.HasGymDisplay() {
		gd := f.GetGymDisplay()
		foldI64(h, 127, int64(gd.GetSlotsAvailable()))
		foldI64(h, 128, int64(gd.GetTotalGymCp()))
	}

	foldBool(h, 129, f.HasRaidInfo())
	if f.HasRaidInfo() {
		digestRaidInfo(h, f.GetRaidInfo())
	}

	displays := f.GetPokestopDisplays()
	foldLen(h, 130, displays.Len())
	for d := range displays.All() {
		digestIncidentDisplay(h, d)
	}
	foldBool(h, 131, f.HasPokestopDisplay())
	if f.HasPokestopDisplay() {
		digestIncidentDisplay(h, f.GetPokestopDisplay())
	}

	foldBool(h, 134, f.GetIsExRaidEligible())

	// active_pokemon feeds decodeGMO's lure pipeline (fort.GetActivePokemon()
	// -> RawMapPokemonData in decode.go) independently of the pokestop
	// display fields above, so it must be folded on its own.
	foldBool(h, 135, f.HasActivePokemon())
	if f.HasActivePokemon() {
		digestMapPokemon(h, f.GetActivePokemon())
	}
}

func digestWeather(h hash.Hash64, w pogoshim.ClientWeatherProto) {
	foldI64(h, 140, w.GetS2CellId())

	foldBool(h, 152, w.HasGameplayWeather())
	if w.HasGameplayWeather() {
		foldI64(h, 141, int64(w.GetGameplayWeather().GetGameplayCondition()))
	}

	foldBool(h, 153, w.HasDisplayWeather())
	if w.HasDisplayWeather() {
		dw := w.GetDisplayWeather()
		foldI64(h, 142, int64(dw.GetCloudLevel()))
		foldI64(h, 143, int64(dw.GetRainLevel()))
		foldI64(h, 144, int64(dw.GetWindLevel()))
		foldI64(h, 145, int64(dw.GetSnowLevel()))
		foldI64(h, 146, int64(dw.GetFogLevel()))
		foldI64(h, 147, int64(dw.GetSpecialEffectLevel()))
		foldI64(h, 148, int64(dw.GetWindDirection()))
	}

	alerts := w.GetAlerts()
	foldLen(h, 149, alerts.Len())
	for a := range alerts.All() {
		foldI64(h, 150, int64(a.GetSeverity()))
		foldBool(h, 151, a.GetWarnWeather())
	}
}

func digestStation(h hash.Hash64, s pogoshim.StationProto) {
	foldStr(h, 160, s.GetId())
	foldStr(h, 161, s.GetName())
	foldF64(h, 162, s.GetLat())
	foldF64(h, 163, s.GetLng())
	foldI64(h, 164, s.GetStartTimeMs())
	foldI64(h, 165, s.GetEndTimeMs())
	foldI64(h, 166, s.GetCooldownCompleteMs())
	foldBool(h, 167, s.GetIsBreadBattleAvailable())

	foldBool(h, 168, s.HasBattleDetails())
	if s.HasBattleDetails() {
		bd := s.GetBattleDetails()
		foldI64(h, 169, bd.GetBreadBattleSeed())
		foldI64(h, 170, int64(bd.GetBattleLevel()))
		foldI64(h, 171, bd.GetBattleWindowStartMs())
		foldI64(h, 172, bd.GetBattleWindowEndMs())
		foldBool(h, 173, bd.HasBattlePokemon())
		if bd.HasBattlePokemon() {
			digestPokemon(h, bd.GetBattlePokemon())
		}
	}
}

// digestGmo folds cell ids/timestamps and every entity family the GMO
// pipeline reads (forts incl. raid/gym display/incidents, wild/nearby/map
// pokemon, weather incl. alerts, stations incl. battle details).
func digestGmo(g pogoshim.GetMapObjectsOutProto) uint64 {
	h := fnv.New64a()
	foldI64(h, 1, int64(g.GetStatus()))
	foldI64(h, 2, int64(g.GetTimeOfDay()))
	foldI64(h, 3, int64(g.GetMoonPhase()))
	foldI64(h, 4, int64(g.GetTwilightPeriod()))

	cells := g.GetMapCell()
	foldLen(h, 5, cells.Len())
	for cell := range cells.All() {
		foldU64(h, 6, cell.GetS2CellId())
		foldI64(h, 7, cell.GetAsOfTimeMs())

		forts := cell.GetFort()
		foldLen(h, 8, forts.Len())
		for f := range forts.All() {
			digestFort(h, f)
		}

		wilds := cell.GetWildPokemon()
		foldLen(h, 9, wilds.Len())
		for w := range wilds.All() {
			digestWild(h, w)
		}

		nearby := cell.GetNearbyPokemon()
		foldLen(h, 10, nearby.Len())
		for n := range nearby.All() {
			digestNearby(h, n)
		}

		catchable := cell.GetCatchablePokemon()
		foldLen(h, 11, catchable.Len())
		for m := range catchable.All() {
			digestMapPokemon(h, m)
		}

		stations := cell.GetStations()
		foldLen(h, 12, stations.Len())
		for s := range stations.All() {
			digestStation(h, s)
		}
	}

	weathers := g.GetClientWeather()
	foldLen(h, 13, weathers.Len())
	for w := range weathers.All() {
		digestWeather(h, w)
	}

	return h.Sum64()
}

// digestEncounter folds the wild pokemon chain (incl. IVs/display) and
// capture probabilities the encounter decode path reads.
func digestEncounter(e pogoshim.EncounterOutProto) uint64 {
	h := fnv.New64a()
	foldI64(h, 1, int64(e.GetBackground()))
	foldI64(h, 2, int64(e.GetStatus()))
	foldI64(h, 3, int64(e.GetActiveItem()))
	foldI64(h, 4, int64(e.GetArplusAttemptsUntilFlee()))

	foldBool(h, 5, e.HasPokemon())
	if e.HasPokemon() {
		digestWild(h, e.GetPokemon())
	}

	foldBool(h, 6, e.HasCaptureProbability())
	if e.HasCaptureProbability() {
		digestCaptureProbability(h, e.GetCaptureProbability())
	}

	return h.Sum64()
}

// digestDiskEncounter folds the pokemon chain (incl. display) and capture
// probabilities the disk-encounter decode path reads.
func digestDiskEncounter(d pogoshim.DiskEncounterOutProto) uint64 {
	h := fnv.New64a()
	foldI64(h, 1, int64(d.GetResult()))
	foldI64(h, 2, int64(d.GetActiveItem()))
	foldI64(h, 3, int64(d.GetArplusAttemptsUntilFlee()))

	foldBool(h, 4, d.HasPokemon())
	if d.HasPokemon() {
		digestPokemon(h, d.GetPokemon())
	}

	foldBool(h, 5, d.HasCaptureProbability())
	if d.HasCaptureProbability() {
		digestCaptureProbability(h, d.GetCaptureProbability())
	}

	return h.Sum64()
}
