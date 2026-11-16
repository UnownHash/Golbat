package decoder

import (
	"context"
	"testing"

	"golbat/db"
	"golbat/derive/encounter"
	"golbat/pogo"

	"github.com/guregu/null/v6"
)

const (
	miniorId     = int16(pogo.HoloPokemonId_MINIOR)
	miniorMeteor = uint16(pogo.PokemonDisplayProto_MINIOR_METEOR)
	miniorGreen  = uint16(pogo.PokemonDisplayProto_MINIOR_GREEN)
	miniorYellow = uint16(pogo.PokemonDisplayProto_MINIOR_YELLOW)
)

func meteorDisplay() *pogo.PokemonDisplayProto {
	return &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_MINIOR_METEOR, Gender: 3, WeatherBoostedCondition: 1}
}

// wantCore is the colour miniorCores gives id.
func wantCore(id uint64) uint16 {
	if encounter.MiniorCoreRoll(id) < 0.05 {
		return miniorGreen
	}
	return miniorYellow
}

func assertMiniorCore(t *testing.T, p *Pokemon) {
	t.Helper()
	if p.IsDitto || p.PokemonId != miniorId || p.Form.ValueOrZero() != wantCore(uint64(p.Id)) ||
		p.DisplayPokemonId.ValueOrZero() != uint16(miniorId) || p.DisplayPokemonForm.ValueOrZero() != miniorMeteor {
		t.Fatalf("want form %d shown as %d/%d, got %+v", wantCore(uint64(p.Id)), miniorId, miniorMeteor, p.PokemonData)
	}
}

func TestMeteorSightingDerivesCore(t *testing.T) {
	// Covers both colours of the default table.
	for _, id := range []uint64{977401, findIdWithRollBelow(t, 0.05)} {
		p := miniorTestPokemon()
		p.Id = Uint64Str(id)
		p.setPokemonDisplay(miniorId, meteorDisplay())
		assertMiniorCore(t, p)
	}
}

func findIdWithRollBelow(t *testing.T, limit float32) uint64 {
	t.Helper()
	for id := uint64(1); id < 100000; id++ {
		if encounter.MiniorCoreRoll(id) < limit {
			return id
		}
	}
	t.Fatal("no id found")
	return 0
}

func TestMeteorResightingKeepsCoreAndStats(t *testing.T) {
	p := miniorTestPokemon()
	p.setPokemonDisplay(miniorId, meteorDisplay())
	p.newRecord = false
	p.Move1 = null.ValueFrom(uint16(1))
	p.Cp = null.ValueFrom(uint16(500))

	p.setPokemonDisplay(miniorId, meteorDisplay())
	assertMiniorCore(t, p)
	if !p.Move1.Valid || !p.Cp.Valid {
		t.Errorf("unchanged resighting cleared stats: %+v", p.PokemonData)
	}
	if p.shownForm() != int64(miniorMeteor) {
		t.Errorf("shownForm = %d, want %d", p.shownForm(), miniorMeteor)
	}
}

func TestDerivedMiniorDisplayChangeClearsCore(t *testing.T) {
	p := miniorTestPokemon()
	p.setPokemonDisplay(miniorId, meteorDisplay())
	p.newRecord = false

	p.setPokemonDisplay(int16(pogo.HoloPokemonId_PIKACHU),
		&pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_PIKACHU_NORMAL, Gender: 1})
	if p.DisplayPokemonId.Valid || p.DisplayPokemonForm.Valid ||
		p.Form.ValueOrZero() != uint16(pogo.PokemonDisplayProto_PIKACHU_NORMAL) {
		t.Errorf("display change kept the Minior core: %+v", p.PokemonData)
	}
}

func TestMiniorCoreNotDerived(t *testing.T) {
	t.Run("derive_iv off", func(t *testing.T) {
		withDeriveIv(t, false)
		p := miniorTestPokemon()
		p.setPokemonDisplay(miniorId, meteorDisplay())
		if p.Form.ValueOrZero() != miniorMeteor || p.DisplayPokemonId.Valid {
			t.Errorf("derived with derive_iv off: %+v", p.PokemonData)
		}
	})
	t.Run("not the Meteor form", func(t *testing.T) {
		p := miniorTestPokemon()
		p.setPokemonDisplay(miniorId, &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_MINIOR_YELLOW})
		if p.Form.ValueOrZero() != miniorYellow || p.DisplayPokemonId.Valid {
			t.Errorf("derived from a core form: %+v", p.PokemonData)
		}
	})
}

// CP uses the core form's base stats, which differ from the Meteor form's.
func TestDerivedMiniorCpUsesCoreForm(t *testing.T) {
	withOhbem(t)
	p := miniorTestPokemon()
	p.setPokemonDisplay(miniorId, meteorDisplay())
	p.AtkIv, p.DefIv, p.StaIv = null.ValueFrom(uint8(10)), null.ValueFrom(uint8(11)), null.ValueFrom(uint8(12))
	p.Level = null.ValueFrom(uint8(20))
	p.Cp = null.Value[uint16]{}
	p.recomputeCpIfNeeded(context.Background(), db.DbDetails{}, nil)

	want, err := ohbem.Load().CalculateCp(int(miniorId), int(wantCore(uint64(p.Id))), 0, 10, 11, 12, 20)
	if err != nil {
		t.Fatal(err)
	}
	meteor, _ := ohbem.Load().CalculateCp(int(miniorId), int(miniorMeteor), 0, 10, 11, 12, 20)
	if want == meteor {
		t.Fatal("test needs Meteor and core CP to differ")
	}
	if int(p.Cp.ValueOrZero()) != want {
		t.Errorf("CP = %d, want core CP %d (Meteor %d)", p.Cp.ValueOrZero(), want, meteor)
	}
}
