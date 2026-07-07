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
// default (a safe no-op returning true) for any method not wired into
// shadowComparePair's switch yet -- Task 4 adds the rest of the request+data
// methods (contest_data, size_contest_entry, station_details, tappable,
// event_rsvps).
func TestShadowComparePairDefaultIsNoOp(t *testing.T) {
	if !shadowComparePair(engMethodContestData, []byte{}, []byte{}) {
		t.Fatal("shadowComparePair should no-op (return true) for a method with no case yet")
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
