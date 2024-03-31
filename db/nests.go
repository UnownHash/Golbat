package db

import "gopkg.in/guregu/null.v4"

type Nest struct {
	Id      int         `db:"nest_id"`
	Lat     float64     `db:"lat"`
	Lon     float64     `db:"lon"`
	Name    null.String `db:"name"`
	Polygon string      `db:"polygon_astext"`
}

func LoadNests(db DbDetails) ([]Nest, error) {
	fortIds := []Nest{}
	err := db.GeneralDb.Select(&fortIds,
		"SELECT nest_id, lat, lon, name, st_astext(polygon) as polygon_astext FROM nests WHERE active = 1")
	statsCollector.IncDbQuery("select nest-polygons", err)
	if err != nil {
		return nil, err
	}

	return fortIds, nil
}
