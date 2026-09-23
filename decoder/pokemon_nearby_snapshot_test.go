package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/golang/geo/s2"

	"golbat/db"
	"golbat/pogo"
)

// Snapshot nearby entries whose fort was not listed in the GMO arrive with
// no cell. The pokestop's stored location says which cell that is; an
// unknown pokestop leaves nothing to place the pokemon by, so the update is
// dropped rather than placing it at the centre of cell 0.
func TestUpdateFromNearbyWithoutCell(t *testing.T) {
	t.Run("derives the cell from the pokestop", func(t *testing.T) {
		const fortId = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		id := mustFortId(t, fortId)
		pokestopCache.Set(id, &Pokestop{PokestopData: PokestopData{
			Id:  id,
			Lat: 51.5,
			Lon: -0.12,
		}}, time.Minute)
		t.Cleanup(func() { pokestopCache.Delete(id) })

		p := &Pokemon{}
		p.Id = 44
		p.newRecord = true
		nearby := &pogo.NearbyPokemonProto{
			FortId:         fortId,
			PokedexNumber:  25,
			PokemonDisplay: &pogo.PokemonDisplayProto{},
		}
		p.updateFromNearby(context.Background(), db.DbDetails{}, nearby, 0, nil, 1700000000000, "tester")

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

	t.Run("drops the update when the pokestop is unknown", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = 45
		p.newRecord = true
		nearby := &pogo.NearbyPokemonProto{
			FortId:         "not-a-fort-id",
			PokedexNumber:  25,
			PokemonDisplay: &pogo.PokemonDisplayProto{},
		}
		p.updateFromNearby(context.Background(), db.DbDetails{}, nearby, 0, nil, 1700000000000, "tester")

		if p.CellId.Valid || p.Lat != 0 || p.Lon != 0 || p.SeenType.Code == SeenTypeCodeCell {
			t.Errorf("pokemon placed anyway: cell %+v at (%v, %v) seen type %d", p.CellId, p.Lat, p.Lon, p.SeenType.Code)
		}
	})
}
