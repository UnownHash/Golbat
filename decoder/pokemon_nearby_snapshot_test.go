package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/pogo"
)

// A nearby pokemon is placed at its pokestop. When the pokestop's location is
// not known the sighting is refused with the record untouched, so the caller
// does not save it; a later GMO can place it once the pokestop is known.
func TestUpdateFromNearbyPlacement(t *testing.T) {
	const knownFort = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const zeroFort = "cccccccccccccccccccccccccccccccc"
	for id, loc := range map[string][2]float64{knownFort: {51.5, -0.12}, zeroFort: {0, 0}} {
		fortId := mustFortId(t, id)
		pokestopCache.Set(fortId, &Pokestop{PokestopData: PokestopData{Id: fortId, Lat: loc[0], Lon: loc[1]}}, time.Minute)
		t.Cleanup(func() { pokestopCache.Delete(fortId) })
	}
	const cell = int64(0x2a0000000000000f)
	nearby := func(fortId string) *pogo.NearbyPokemonProto {
		return &pogo.NearbyPokemonProto{FortId: fortId, PokedexNumber: 25, PokemonDisplay: &pogo.PokemonDisplayProto{}}
	}
	newPokemon := func() *Pokemon {
		p := &Pokemon{}
		p.Id = 44
		p.newRecord = true
		return p
	}

	t.Run("snapshot entry without a cell takes the pokestop's cell", func(t *testing.T) {
		p := newPokemon()
		if !p.updateFromNearby(context.Background(), db.DbDetails{}, nearby(knownFort), 0, nil, 1700000000000, "tester") {
			t.Fatal("sighting at a known pokestop refused")
		}
		wantCell := int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5, -0.12)).Parent(gmoCellLevel))
		if !p.CellId.Valid || p.CellId.V != wantCell {
			t.Errorf("CellId = %+v, want %d (level %d cell of the pokestop)", p.CellId, wantCell, gmoCellLevel)
		}
		if p.Lat != 51.5 || p.Lon != -0.12 {
			t.Errorf("coordinates = (%v, %v), want the pokestop's (51.5, -0.12)", p.Lat, p.Lon)
		}
		if p.SeenType.Code != SeenTypeCodeNearbyStop {
			t.Errorf("SeenType = %d, want nearby stop (%d)", p.SeenType.Code, SeenTypeCodeNearbyStop)
		}
	})

	for _, tc := range []struct {
		name   string
		fortId string
		cellId int64
	}{
		{"unknown pokestop, no cell", "not-a-fort-id", 0},
		{"unknown pokestop, cell known: not placed at the cell centre", "not-a-fort-id", cell},
		{"pokestop without a location", zeroFort, cell},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPokemon()
			if p.updateFromNearby(context.Background(), db.DbDetails{}, nearby(tc.fortId), tc.cellId, nil, 1700000000000, "tester") {
				t.Fatal("sighting accepted, want refused")
			}
			if p.IsDirty() || p.CellId.Valid || p.Lat != 0 || p.Lon != 0 || p.SeenType.Code != SeenTypeCodeUnset || p.PokemonId != 0 {
				t.Errorf("refused sighting mutated the record: dirty=%t cell %+v at (%v, %v) seen type %d pokemon %d",
					p.IsDirty(), p.CellId, p.Lat, p.Lon, p.SeenType.Code, p.PokemonId)
			}
		})
	}

	t.Run("a wild pokemon keeps its own location", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = 46
		p.Lat, p.Lon = 40.7, -73.9
		p.SetSeenType(SeenTypeCodeWild)
		if !p.updateFromNearby(context.Background(), db.DbDetails{}, nearby(knownFort), cell, nil, 1700000000000, "tester") {
			t.Fatal("nearby sighting of a wild pokemon refused")
		}
		if p.Lat != 40.7 || p.Lon != -73.9 || p.SeenType.Code != SeenTypeCodeWild {
			t.Errorf("wild pokemon moved to (%v, %v) seen type %d", p.Lat, p.Lon, p.SeenType.Code)
		}
		if p.PokemonId != 25 {
			t.Errorf("PokemonId = %d, want the display refreshed to 25", p.PokemonId)
		}
	})
}

// Snapshot entries carry the encounter id in PokemonDisplay.DisplayId, not
// the species. An unchanged pokemon must not count as a significant update,
// or every nearby pokemon is rewritten on every GMO.
func TestNearbySignificantUpdateIgnoresDisplayId(t *testing.T) {
	p := &Pokemon{}
	p.Id = 3920828785339412403
	p.PokemonId = 66
	p.SetSeenType(SeenTypeCodeNearbyStop)
	p.SetExpireTimestamp(null.IntFrom(1700000600))

	// The value seen on the wire: DisplayId is the encounter id.
	encounterId := uint64(3920828785339412403)
	nearby := func(dex int32) *pogo.NearbyPokemonProto {
		return &pogo.NearbyPokemonProto{
			EncounterId:    encounterId,
			PokedexNumber:  dex,
			PokemonDisplay: &pogo.PokemonDisplayProto{DisplayId: int64(encounterId)},
		}
	}
	if p.nearbySignificantUpdate(nearby(66), 1700000000) {
		t.Error("unchanged nearby pokemon reported as a significant update")
	}
	if !p.nearbySignificantUpdate(nearby(67), 1700000000) {
		t.Error("species change not reported as a significant update")
	}
}
