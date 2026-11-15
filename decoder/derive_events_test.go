package decoder

import (
	"testing"
	"time"

	"golbat/derive/encounter"
	"golbat/pogo"

	"github.com/guregu/null/v6"
)

var eventTestValues = encounter.Values{Attack: 10, Defense: 11, Stamina: 12, Level: 20, Size: encounter.SizeM}

var eventTestMoves = derivedMoveResult{
	Move1: 214, Move2: 268,
	QuickPool: []int{214, 215}, ChargedPool: []int{268, 300},
	Resolved: true,
}

func TestClassifyDimensionsAgreeAll(t *testing.T) {
	got := classifyDimensions(eventTestValues, eventTestValues, 0, eventTestMoves, 214, 268)
	want := dimAgreement{IVs: dimAgree, Level: dimAgree, Size: dimAgree, Quick: dimAgree, Charged: dimAgree}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got.Deviates() {
		t.Fatal("Deviates() = true for full agreement")
	}
}

func TestClassifyDimensionsLevelOnly(t *testing.T) {
	observed := eventTestValues
	observed.Level += 5
	got := classifyDimensions(observed, eventTestValues, 0, eventTestMoves, 214, 268)
	want := dimAgreement{IVs: dimAgree, Level: dimDisagree, Size: dimAgree, Quick: dimAgree, Charged: dimAgree}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !got.Deviates() {
		t.Fatal("Deviates() = false with a level disagreement")
	}
}

func TestClassifyDimensionsIVsAnyStat(t *testing.T) {
	observed := eventTestValues
	observed.Stamina++
	got := classifyDimensions(observed, eventTestValues, 0, eventTestMoves, 214, 268)
	if got.IVs != dimDisagree || got.Level != dimAgree {
		t.Fatalf("got %+v, want IVs disagree, Level agree", got)
	}
}

func TestClassifyDimensionsMoveOutOfPool(t *testing.T) {
	got := classifyDimensions(eventTestValues, eventTestValues, 0, eventTestMoves, 214, 494)
	if got.Charged != dimDisagree {
		t.Fatalf("Charged = %v, want dimDisagree for out-of-pool 494", got.Charged)
	}
	if got.Quick != dimAgree {
		t.Fatalf("Quick = %v, want dimAgree", got.Quick)
	}
	if !got.Deviates() {
		t.Fatal("Deviates() = false with an out-of-pool move")
	}
}

func TestClassifyDimensionsInPoolWrongMove(t *testing.T) {
	got := classifyDimensions(eventTestValues, eventTestValues, 0, eventTestMoves, 215, 300)
	if got.Quick != dimDisagree || got.Charged != dimDisagree {
		t.Fatalf("got %+v, want both move dims dimDisagree", got)
	}
}

func TestClassifyDimensionsSizeNoOpinion(t *testing.T) {
	observed := eventTestValues
	observed.Size = 0
	if got := classifyDimensions(observed, eventTestValues, 0, eventTestMoves, 214, 268); got.Size != dimNoOpinion {
		t.Fatalf("Size = %v, want dimNoOpinion for unset size", got.Size)
	}

	observed = eventTestValues
	observed.Size = encounter.SizeXXL
	if got := classifyDimensions(observed, eventTestValues, 2641, eventTestMoves, 214, 268); got.Size != dimNoOpinion {
		t.Fatalf("Size = %v, want dimNoOpinion for a form-fixed size", got.Size)
	}

	if got := classifyDimensions(observed, eventTestValues, 0, eventTestMoves, 214, 268); got.Size != dimDisagree {
		t.Fatalf("Size = %v, want dimDisagree for a differing rolled size", got.Size)
	}
}

func TestClassifyDimensionsUnresolvedMoves(t *testing.T) {
	got := classifyDimensions(eventTestValues, eventTestValues, 0, derivedMoveResult{}, 214, 494)
	if got.Quick != dimNoOpinion || got.Charged != dimNoOpinion {
		t.Fatalf("got %+v, want both move dims dimNoOpinion", got)
	}
	if got.Deviates() {
		t.Fatal("Deviates() = true with only no-opinion move dims")
	}
}

func TestClassifyDimensionsUnreportedMoves(t *testing.T) {
	got := classifyDimensions(eventTestValues, eventTestValues, 0, eventTestMoves, 0, 0)
	if got.Quick != dimNoOpinion || got.Charged != dimNoOpinion {
		t.Fatalf("got %+v, want both move dims dimNoOpinion", got)
	}
}

func TestNewDeriveEventCheckExcludesDitto(t *testing.T) {
	scan := &pokemonScan{Pokemon: 1}
	now := time.Unix(1_700_000_000, 0)

	ditto := &Pokemon{PokemonData: PokemonData{Id: 1, IsDitto: true}}
	if _, ok := newDeriveEventCheck(ditto, scan, derivationCheck{Branch: dittoBranchExpected}, "acct", now); ok {
		t.Fatal("ok = true for an IsDitto record")
	}

	plain := &Pokemon{PokemonData: PokemonData{Id: 1}}
	if _, ok := newDeriveEventCheck(plain, scan, derivationCheck{Branch: dittoBranchOpposite}, "acct", now); ok {
		t.Fatal("ok = true for an opposite-branch encounter")
	}
}

func TestNewDeriveEventCheckFillsFields(t *testing.T) {
	scan := &pokemonScan{
		Pokemon: 25, Form: 598, Costume: 3, Weather: int32(pogo.GameplayWeatherProto_RAINY), Strong: true,
	}
	chk := derivationCheck{
		Observed: eventTestValues, Derived: eventTestValues,
		Moves: eventTestMoves, Move1: 214, Move2: 494,
		Branch: dittoBranchUnknown, Boosted: true,
	}
	p := &Pokemon{PokemonData: PokemonData{
		Id:                 Uint64Str(987654321),
		CellId:             null.ValueFrom(int64(-5)),
		SpawnId:            null.ValueFrom(int64(42)),
		Lat:                1.5,
		Lon:                -2.5,
		FirstSeenTimestamp: 1234,
	}}
	now := time.Unix(1_700_000_000, 0)

	ev, ok := newDeriveEventCheck(p, scan, chk, "acct", now)
	if !ok {
		t.Fatal("ok = false for a non-Ditto encounter")
	}
	if ev.Time != now || ev.EncounterID != 987654321 || ev.PokemonID != 25 || ev.Form != 598 ||
		ev.Costume != 3 || ev.Weather != int32(pogo.GameplayWeatherProto_RAINY) || !ev.Strong || !ev.Boosted ||
		ev.CellID != -5 || ev.SpawnID != 42 || ev.Lat != 1.5 || ev.Lon != -2.5 ||
		ev.FirstSeen != 1234 || ev.Account != "acct" {
		t.Fatalf("unexpected fields: %+v", ev)
	}
	if ev.Agree.Charged != dimDisagree || ev.Agree.Quick != dimAgree || ev.Agree.IVs != dimAgree {
		t.Fatalf("Agree = %+v, want charged disagree, rest agree", ev.Agree)
	}

	// Null cell and spawn read as 0.
	p.CellId, p.SpawnId = null.Value[int64]{}, null.Value[int64]{}
	ev, _ = newDeriveEventCheck(p, scan, chk, "acct", now)
	if ev.CellID != 0 || ev.SpawnID != 0 {
		t.Fatalf("CellID=%d SpawnID=%d, want 0 for null", ev.CellID, ev.SpawnID)
	}
}

func TestCheckDerivationReturnsComputation(t *testing.T) {
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			1: {QuickMoves: []int{214, 215}, ChargedMoves: []int{90, 91, 92}},
		},
	})
	withDerivationCountingCollector(t)

	const eid = uint64(111222333444)
	draw := encounter.For(eid, false, 0)
	scan := &pokemonScan{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   draw.Level, Attack: draw.Attack, Defense: draw.Defense, Stamina: draw.Stamina,
		Pokemon: 1,
	}
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eid)}}

	chk := p.checkDerivation(scan, draw.Size, 214, 90, "encounter", false, false)

	if chk.Derived != draw.Values {
		t.Fatalf("Derived = %+v, want %+v", chk.Derived, draw.Values)
	}
	if chk.Opposite != encounter.For(eid, true, 0).Values {
		t.Fatalf("Opposite = %+v, want the boosted branch", chk.Opposite)
	}
	if chk.SizeRoll != draw.SizeRoll {
		t.Fatalf("SizeRoll = %v, want %v", chk.SizeRoll, draw.SizeRoll)
	}
	if chk.Observed != draw.Values {
		t.Fatalf("Observed = %+v, want %+v", chk.Observed, draw.Values)
	}
	if !chk.Moves.Resolved || chk.Move1 != 214 || chk.Move2 != 90 {
		t.Fatalf("Moves=%+v Move1=%d Move2=%d", chk.Moves, chk.Move1, chk.Move2)
	}
	if chk.Branch != dittoBranchExpected || chk.Boosted {
		t.Fatalf("Branch=%v Boosted=%v, want expected/false", chk.Branch, chk.Boosted)
	}
}
