package main

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/pogo"
)

// Engine method keys: the [proto_engine] config resolution keys (engineFor's
// "method" parameter). These are NOT 1:1 with generator roots -- several
// methods decode more than one root under the same key (a request proto plus
// a data/response proto, or, for "social", a response envelope plus one of
// several inner payload types), so engineFor()/config.Overrides always key
// on the method, while the specific *protoEngineHandle used for a given
// proto is selected directly by the call site.
const (
	engMethodGmo               = "gmo"
	engMethodEncounter         = "encounter"
	engMethodDiskEncounter     = "disk_encounter"
	engMethodFortDetails       = "fort_details"
	engMethodGymInfo           = "gym_info"
	engMethodQuest             = "quest"
	engMethodGetMapForts       = "get_map_forts"
	engMethodRoutes            = "routes"
	engMethodStartIncident     = "start_incident"
	engMethodOpenInvasion      = "open_invasion"
	engMethodNebulaBattleState = "nebula_battle_state"
	engMethodContestData       = "contest_data"
	engMethodSizeContestEntry  = "size_contest_entry"
	engMethodStationDetails    = "station_details"
	engMethodTappable          = "tappable"
	engMethodEventRsvps        = "event_rsvps"
	engMethodEventRsvpCount    = "event_rsvp_count"
	engMethodSocial            = "social"
)

// Per-root engine handles: one package var per generator root (see
// cmd/pogoshimgen/main.go's defaultRoots), populated by initProtoEngines()
// after config load. Several vars share a config method key above -- e.g.
// openInvasionReqEngine/openInvasionEngine both resolve their std/hyperpb
// choice via engMethodOpenInvasion, but decode independent payloads
// (request vs. data) through independent arenas/compiled types.
var (
	gmoEngine           *protoEngineHandle
	encounterEngine     *protoEngineHandle
	diskEncounterEngine *protoEngineHandle

	fortDetailsEngine       *protoEngineHandle
	gymInfoEngine           *protoEngineHandle
	questEngine             *protoEngineHandle
	mapFortsEngine          *protoEngineHandle
	routesEngine            *protoEngineHandle
	startIncidentEngine     *protoEngineHandle
	openInvasionReqEngine   *protoEngineHandle
	openInvasionEngine      *protoEngineHandle
	battleStateEngine       *protoEngineHandle
	contestDataReqEngine    *protoEngineHandle
	contestDataEngine       *protoEngineHandle
	sizeEntryReqEngine      *protoEngineHandle
	sizeEntryEngine         *protoEngineHandle
	stationDetailsReqEngine *protoEngineHandle
	stationDetailsEngine    *protoEngineHandle
	tappableReqEngine       *protoEngineHandle
	tappableEngine          *protoEngineHandle
	rsvpReqEngine           *protoEngineHandle
	rsvpEngine              *protoEngineHandle
	rsvpCountEngine         *protoEngineHandle
	proxyReqEngine          *protoEngineHandle
	proxyRespEngine         *protoEngineHandle
	friendDetailsEngine     *protoEngineHandle
	searchPlayerOutEngine   *protoEngineHandle
	searchPlayerReqEngine   *protoEngineHandle
)

// engineSpec pairs a config method key + generator root descriptor + std
// prototype constructor with the package var it populates. engineSpecs has
// no config or platform dependency, so it lives in this platform-neutral
// file; initProtoEngines() (also defined here, shared by both platform
// builds) walks it once at startup, after config load, calling the
// platform-specific newProtoEngine to build each protoEngineHandle.
type engineSpec struct {
	method string
	target **protoEngineHandle
	md     protoreflect.MessageDescriptor
	newStd func() proto.Message
}

var engineSpecs = []engineSpec{
	{engMethodGmo, &gmoEngine,
		(*pogo.GetMapObjectsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetMapObjectsOutProto{} }},
	{engMethodEncounter, &encounterEngine,
		(*pogo.EncounterOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.EncounterOutProto{} }},
	{engMethodDiskEncounter, &diskEncounterEngine,
		(*pogo.DiskEncounterOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.DiskEncounterOutProto{} }},

	{engMethodFortDetails, &fortDetailsEngine,
		(*pogo.FortDetailsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.FortDetailsOutProto{} }},
	{engMethodGymInfo, &gymInfoEngine,
		(*pogo.GymGetInfoOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GymGetInfoOutProto{} }},
	{engMethodQuest, &questEngine,
		(*pogo.FortSearchOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.FortSearchOutProto{} }},
	{engMethodGetMapForts, &mapFortsEngine,
		(*pogo.GetMapFortsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetMapFortsOutProto{} }},
	{engMethodRoutes, &routesEngine,
		(*pogo.GetRoutesOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetRoutesOutProto{} }},
	{engMethodStartIncident, &startIncidentEngine,
		(*pogo.StartIncidentOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.StartIncidentOutProto{} }},
	{engMethodOpenInvasion, &openInvasionReqEngine,
		(*pogo.OpenInvasionCombatSessionProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.OpenInvasionCombatSessionProto{} }},
	{engMethodOpenInvasion, &openInvasionEngine,
		(*pogo.OpenInvasionCombatSessionOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.OpenInvasionCombatSessionOutProto{} }},
	{engMethodNebulaBattleState, &battleStateEngine,
		(*pogo.BattleStateOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.BattleStateOutProto{} }},
	{engMethodContestData, &contestDataReqEngine,
		(*pogo.GetContestDataProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetContestDataProto{} }},
	{engMethodContestData, &contestDataEngine,
		(*pogo.GetContestDataOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetContestDataOutProto{} }},
	{engMethodSizeContestEntry, &sizeEntryReqEngine,
		(*pogo.GetPokemonSizeLeaderboardEntryProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetPokemonSizeLeaderboardEntryProto{} }},
	{engMethodSizeContestEntry, &sizeEntryEngine,
		(*pogo.GetPokemonSizeLeaderboardEntryOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetPokemonSizeLeaderboardEntryOutProto{} }},
	{engMethodStationDetails, &stationDetailsReqEngine,
		(*pogo.GetStationedPokemonDetailsProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetStationedPokemonDetailsProto{} }},
	{engMethodStationDetails, &stationDetailsEngine,
		(*pogo.GetStationedPokemonDetailsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetStationedPokemonDetailsOutProto{} }},
	{engMethodTappable, &tappableReqEngine,
		(*pogo.ProcessTappableProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.ProcessTappableProto{} }},
	{engMethodTappable, &tappableEngine,
		(*pogo.ProcessTappableOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.ProcessTappableOutProto{} }},
	{engMethodEventRsvps, &rsvpReqEngine,
		(*pogo.GetEventRsvpsProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetEventRsvpsProto{} }},
	{engMethodEventRsvps, &rsvpEngine,
		(*pogo.GetEventRsvpsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetEventRsvpsOutProto{} }},
	{engMethodEventRsvpCount, &rsvpCountEngine,
		(*pogo.GetEventRsvpCountOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.GetEventRsvpCountOutProto{} }},
	{engMethodSocial, &proxyReqEngine,
		(*pogo.ProxyRequestProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.ProxyRequestProto{} }},
	{engMethodSocial, &proxyRespEngine,
		(*pogo.ProxyResponseProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.ProxyResponseProto{} }},
	{engMethodSocial, &friendDetailsEngine,
		(*pogo.InternalGetFriendDetailsOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.InternalGetFriendDetailsOutProto{} }},
	{engMethodSocial, &searchPlayerOutEngine,
		(*pogo.InternalSearchPlayerOutProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.InternalSearchPlayerOutProto{} }},
	{engMethodSocial, &searchPlayerReqEngine,
		(*pogo.InternalSearchPlayerProto)(nil).ProtoReflect().Descriptor(),
		func() proto.Message { return &pogo.InternalSearchPlayerProto{} }},
}

// initProtoEngines compiles every root's protoEngineHandle (hyperpb arenas +
// PGO warmup profile on supported platforms; a bare std-prototype handle on
// the stub build) and starts the PGO warmup deadline clock. Must run after
// config load (cache/PGO-profile setup below reads config.Config).
func initProtoEngines() {
	startPgoWarmupClock()
	for _, s := range engineSpecs {
		*s.target = newProtoEngine(s.method, s.md, s.newStd)
	}
}

// legacyProtoEngineValue returns the explicit [proto_engine] value for the
// three original methods that predate config.Overrides, or "" (meaning
// "inherit": fall through to Overrides then Default) for every other
// method or an unset legacy field.
func legacyProtoEngineValue(method string) string {
	switch method {
	case engMethodGmo:
		return config.Config.ProtoEngine.Gmo
	case engMethodEncounter:
		return config.Config.ProtoEngine.Encounter
	case engMethodDiskEncounter:
		return config.Config.ProtoEngine.DiskEncounter
	default:
		return ""
	}
}

// engineFor resolves the live decode engine for method. Resolution order:
// an explicit legacy key (gmo/encounter/disk_encounter, non-empty) wins
// outright, preserving deployed-config compatibility; otherwise a per-method
// override; otherwise the package default ("hyperpb"). Anything other than
// exactly "hyperpb" resolves to "std" -- including an unrecognized value,
// silently, which is why warnInvalidProtoEngineValues must be called once at
// startup to flag typos.
func engineFor(method string) string {
	if !hyperpbSupported {
		return "std"
	}
	v := legacyProtoEngineValue(method)
	if v == "" {
		if ov, ok := config.Config.ProtoEngine.Overrides[method]; ok && ov != "" {
			v = ov
		} else {
			v = config.Config.ProtoEngine.Default
		}
	}
	if v == "hyperpb" {
		return "hyperpb"
	}
	return "std"
}

// invalidProtoEngineValues returns every [proto_engine] value (legacy keys,
// default, and each overrides entry) that is non-empty but neither "std"
// nor "hyperpb", mapped to the offending value under a descriptive key
// ("gmo", "encounter", "disk_encounter", "default", "overrides.<method>").
// An empty string is never flagged: for the legacy keys it means "inherit"
// by design, and treating an empty Default/override the same way (rather
// than as an error) keeps the three config surfaces consistent. engineFor()
// silently treats anything unrecognized as "std", so a typo (e.g.
// "hyperbp") would otherwise run on the std engine with no indication
// anything was misconfigured -- callers should warn once at startup for
// every entry here.
func invalidProtoEngineValues() map[string]string {
	bad := map[string]string{}
	check := func(key, v string) {
		if v != "" && v != "std" && v != "hyperpb" {
			bad[key] = v
		}
	}
	check(engMethodGmo, config.Config.ProtoEngine.Gmo)
	check(engMethodEncounter, config.Config.ProtoEngine.Encounter)
	check(engMethodDiskEncounter, config.Config.ProtoEngine.DiskEncounter)
	check("default", config.Config.ProtoEngine.Default)
	for method, v := range config.Config.ProtoEngine.Overrides {
		check("overrides."+method, v)
	}
	return bad
}

// warnInvalidProtoEngineValues logs a one-time warning for every
// [proto_engine] value that isn't "", "std", or "hyperpb". Call once at
// startup after config load, regardless of platform (hyperpb support is
// architecture-gated, but a config typo is worth flagging everywhere).
func warnInvalidProtoEngineValues() {
	for key, v := range invalidProtoEngineValues() {
		log.Warnf("[PROTO_ENGINE] proto_engine.%s = %q is neither \"std\" nor \"hyperpb\" -- falling back to std", key, v)
	}
}

// decodeStd is the fallback path: protobuf-go unmarshal into eng's std
// prototype, wrapped into the same pogoshim surface the hyperpb path uses.
// A nil eng (an unwired or not-yet-initialized handle) is a programming
// error, not a payload error, but is reported the same way (rather than
// panicking) since decodeHyperpb's own nil-handle guard also lands here.
func decodeStd[T any](eng *protoEngineHandle, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	if eng == nil {
		return "", fmt.Errorf("proto engine handle is nil")
	}
	m := eng.newStd()
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, m); err != nil {
		return "", err
	}
	return process(wrap(m.ProtoReflect())), nil
}

func decodeWithArena[T any](method string, eng *protoEngineHandle, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	if engineFor(method) == "hyperpb" {
		return decodeHyperpb(eng, payload, wrap, process)
	}
	return decodeStd(eng, payload, wrap, process)
}
