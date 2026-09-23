package geo

import "github.com/golang/geo/s2"

// SpawnpointCellLevel is the S2 level a spawnpoint id encodes: the id is the
// level-20 cell id with its trailing 20 bits shifted off, and the
// coordinates the game reports for the spawnpoint are that cell's center
// exactly (verified against production rows to the metre). A level-20 cell
// is about 9 m on a side.
const SpawnpointCellLevel = 20

// spawnpointIdShift is the number of trailing cell-id bits a spawnpoint id
// drops: everything after the level-20 position bits and the marker bit.
const spawnpointIdShift = 64 - 3 - 2*SpawnpointCellLevel - 1

// SpawnpointLocation derives a spawnpoint's location from its id. ok is
// false when the id does not shift into a valid level-20 cell, which no
// real spawnpoint id fails.
func SpawnpointLocation(id int64) (Location, bool) {
	cell := s2.CellID(uint64(id) << spawnpointIdShift)
	if !cell.IsValid() || cell.Level() != SpawnpointCellLevel {
		return Location{}, false
	}
	ll := cell.LatLng()
	return Location{Latitude: ll.Lat.Degrees(), Longitude: ll.Lng.Degrees()}, true
}
