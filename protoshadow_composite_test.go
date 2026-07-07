package main

import (
	"hash/fnv"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/config"
	"golbat/pogo"
)

// digestPairViaStd mirrors compareDigestPair's internal fold logic (decoded
// via decodeStd only) so tests can assert on the combined digest's
// sensitivity to either half of the pair changing, the same way
// digestFortViaStd/digestPokemonViaStd do for the single-proto hand-written
// digests elsewhere in this file's sibling protoshadow_test.go.
func digestPairViaStd(t *testing.T, reqEng *protoEngineHandle, request []byte, dataEng *protoEngineHandle, data []byte) uint64 {
	t.Helper()
	h := fnv.New64a()
	process := func(m protoreflect.Message) string {
		digestMessageGeneric(h, m)
		return ""
	}
	if _, err := decodeStd(reqEng, request, identityWrap, process); err != nil {
		t.Fatalf("decodeStd request failed: %v", err)
	}
	if _, err := decodeStd(dataEng, data, identityWrap, process); err != nil {
		t.Fatalf("decodeStd data failed: %v", err)
	}
	return h.Sum64()
}

func marshalOrFatal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal %T: %v", m, err)
	}
	return b
}

// TestShadowComparePairOpenInvasionMatchesAcrossEngines exercises the
// composite (request+data) shadow path end to end for open_invasion, the
// one method wired into shadowComparePair today: a well-formed
// request+data pair must compare equal between the std and hyperpb
// engines.
func TestShadowComparePairOpenInvasionMatchesAcrossEngines(t *testing.T) {
	request := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionProto{
		IncidentLookup: &pogo.IncidentLookupProto{IncidentId: "INC1", FortId: "FORT1"},
		Step:           2,
	})
	data := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionOutProto{
		Status: pogo.InvasionStatus_SUCCESS,
		Combat: &pogo.CombatProto{
			CombatId: "COMBAT1",
			Opponent: &pogo.CombatProto_CombatPlayerProto{
				ActivePokemon: &pogo.CombatProto_CombatPokemonProto{PokedexId: pogo.HoloPokemonId(1)},
			},
		},
	})

	if !shadowComparePair(engMethodOpenInvasion, request, data) {
		t.Fatal("shadowComparePair(open_invasion, ...) = false, want true for a well-formed request+data pair")
	}
}

// TestShadowComparePairDefaultIsNoOp mirrors shadowCompare's pre-Wave-3
// default (a safe no-op returning true) for any method with no case in
// shadowComparePair's switch. Task 4 wired the last of the known
// request+data methods (contest_data, size_contest_entry, station_details,
// tappable, event_rsvps, social), so this now exercises an arbitrary
// unrecognized method key rather than one of those.
func TestShadowComparePairDefaultIsNoOp(t *testing.T) {
	if !shadowComparePair("not_a_real_method", []byte{}, []byte{}) {
		t.Fatal("shadowComparePair should no-op (return true) for a method with no case")
	}
}

// TestDigestPairDetectsRequestOrDataCorruption guards the combined-digest
// fold itself: compareDigestPair folds BOTH the request and the data
// message into the SAME hash, so a change in either half (not just the
// data half, which the pre-existing single-proto digests already cover)
// must change the resulting digest -- otherwise a divergence confined to
// the request side (e.g. IncidentLookup) could slip past shadow
// verification unnoticed.
func TestDigestPairDetectsRequestOrDataCorruption(t *testing.T) {
	baseReq := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionProto{
		IncidentLookup: &pogo.IncidentLookupProto{IncidentId: "INC1", FortId: "FORT1"},
	})
	changedReq := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionProto{
		IncidentLookup: &pogo.IncidentLookupProto{IncidentId: "INC1", FortId: "FORT2"},
	})
	baseData := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionOutProto{
		Status: pogo.InvasionStatus_SUCCESS,
		Combat: &pogo.CombatProto{CombatId: "COMBAT1"},
	})
	changedData := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionOutProto{
		Status: pogo.InvasionStatus_SUCCESS,
		Combat: &pogo.CombatProto{CombatId: "COMBAT2"},
	})

	baseDigest := digestPairViaStd(t, openInvasionReqEngine, baseReq, openInvasionEngine, baseData)

	if got := digestPairViaStd(t, openInvasionReqEngine, changedReq, openInvasionEngine, baseData); got == baseDigest {
		t.Fatal("expected a changed request field (FortId) to change the combined digest")
	}
	if got := digestPairViaStd(t, openInvasionReqEngine, baseReq, openInvasionEngine, changedData); got == baseDigest {
		t.Fatal("expected a changed data field (CombatId) to change the combined digest")
	}
}

// TestMaybeShadowPairForcedRateRecordsMatchNotMismatch is maybeShadowPair's
// counterpart to TestMaybeShadowForcedRateRecordsMatchNotMismatch: with the
// sample rate forced to 1.0, a well-formed request+data pair must record a
// "match", never a "mismatch".
func TestMaybeShadowPairForcedRateRecordsMatchNotMismatch(t *testing.T) {
	prevRate := config.Config.ProtoEngine.ShadowSampleRate
	config.Config.ProtoEngine.ShadowSampleRate = 1.0
	defer func() { config.Config.ProtoEngine.ShadowSampleRate = prevRate }()

	previousStats := statsCollector
	recorder := newRecordingProtoShadowStats()
	statsCollector = recorder
	defer func() { statsCollector = previousStats }()

	request := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionProto{
		IncidentLookup: &pogo.IncidentLookupProto{IncidentId: "INC1", FortId: "FORT1"},
	})
	data := marshalOrFatal(t, &pogo.OpenInvasionCombatSessionOutProto{Status: pogo.InvasionStatus_SUCCESS})

	maybeShadowPair(engMethodOpenInvasion, request, data)

	if got := recorder.count(engMethodOpenInvasion, "mismatch"); got != 0 {
		t.Fatalf("expected 0 mismatches for a well-formed pair, got %d", got)
	}
	if got := recorder.count(engMethodOpenInvasion, "match"); got != 1 {
		t.Fatalf("expected 1 match for a well-formed pair, got %d", got)
	}
}

// TestShadowComparePairTask4MethodsMatchAcrossEngines exercises
// shadowComparePair for the five request-optional request+data methods Task
// 4 wires in, including the request-absent case (nil request bytes) that
// open_invasion (mandatory request) never has to handle: compareDigestPair
// decodes a nil request as a zero-length message on both engines, so the
// combined digest still compares equal.
func TestShadowComparePairTask4MethodsMatchAcrossEngines(t *testing.T) {
	contestReq := marshalOrFatal(t, &pogo.GetContestDataProto{FortId: "FORT1"})
	contestData := marshalOrFatal(t, &pogo.GetContestDataOutProto{
		Status:          pogo.GetContestDataOutProto_SUCCESS,
		ContestIncident: &pogo.ClientContestIncidentProto{Contests: []*pogo.ContestProto{{ContestId: "C1"}}},
	})
	if !shadowComparePair(engMethodContestData, contestReq, contestData) {
		t.Fatal("shadowComparePair(contest_data, ...) = false, want true")
	}
	if !shadowComparePair(engMethodContestData, nil, contestData) {
		t.Fatal("shadowComparePair(contest_data, nil request, ...) = false, want true (nil request must decode identically on both engines)")
	}

	sizeReq := marshalOrFatal(t, &pogo.GetPokemonSizeLeaderboardEntryProto{ContestId: "C1-1"})
	sizeData := marshalOrFatal(t, &pogo.GetPokemonSizeLeaderboardEntryOutProto{Status: pogo.GetPokemonSizeLeaderboardEntryOutProto_SUCCESS})
	if !shadowComparePair(engMethodSizeContestEntry, sizeReq, sizeData) {
		t.Fatal("shadowComparePair(size_contest_entry, ...) = false, want true")
	}

	stationReq := marshalOrFatal(t, &pogo.GetStationedPokemonDetailsProto{StationId: "STATION1"})
	stationData := marshalOrFatal(t, &pogo.GetStationedPokemonDetailsOutProto{Result: pogo.GetStationedPokemonDetailsOutProto_SUCCESS})
	if !shadowComparePair(engMethodStationDetails, stationReq, stationData) {
		t.Fatal("shadowComparePair(station_details, ...) = false, want true")
	}

	tappableReq := marshalOrFatal(t, &pogo.ProcessTappableProto{EncounterId: 42})
	tappableData := marshalOrFatal(t, &pogo.ProcessTappableOutProto{Status: pogo.ProcessTappableOutProto_SUCCESS})
	if !shadowComparePair(engMethodTappable, tappableReq, tappableData) {
		t.Fatal("shadowComparePair(tappable, ...) = false, want true")
	}

	rsvpReq := marshalOrFatal(t, &pogo.GetEventRsvpsProto{EventDetails: &pogo.GetEventRsvpsProto_Raid{Raid: &pogo.RaidDetails{FortId: "FORT1"}}})
	rsvpData := marshalOrFatal(t, &pogo.GetEventRsvpsOutProto{Status: pogo.GetEventRsvpsOutProto_SUCCESS})
	if !shadowComparePair(engMethodEventRsvps, rsvpReq, rsvpData) {
		t.Fatal("shadowComparePair(event_rsvps, ...) = false, want true")
	}
}

// TestShadowComparePairSocialCoversRequestResponseOnly proves the "social"
// composite compares Request (ProxyRequestProto) + Response
// (ProxyResponseProto) only: two payloads whose opaque Payload bytes differ
// (the inner, unverified layer) must still match, since compareDigestPair
// folds the Payload as a plain bytes field on the two messages it actually
// decodes -- it never interprets the Payload as one of the inner proto
// types, that dispatch happens only in the live decodeSocialActionWithRequest
// path.
func TestShadowComparePairSocialCoversRequestResponseOnly(t *testing.T) {
	request := marshalOrFatal(t, &pogo.ProxyRequestProto{Action: uint32(pogo.InternalSocialAction_SOCIAL_ACTION_SEARCH_PLAYER)})
	data := marshalOrFatal(t, &pogo.ProxyResponseProto{Status: pogo.ProxyResponseProto_COMPLETED, Payload: []byte("anything")})
	if !shadowComparePair(engMethodSocial, request, data) {
		t.Fatal("shadowComparePair(social, ...) = false, want true for a well-formed Request+Response pair")
	}
}
