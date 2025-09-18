package geo

import (
	"math"

	"golbat/pogo"
)

type Location struct {
	Latitude  float64
	Longitude float64
}

type Bbox struct {
	MinLon float64 `json:"min_lon"`
	MinLat float64 `json:"min_lat"`
	MaxLon float64 `json:"max_lon"`
	MaxLat float64 `json:"max_lat"`
}

type ApiLocation struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

func (l Location) Tuple() (float64, float64) {
	return l.Latitude, l.Longitude
}

func LocationFromFort(fort *pogo.PokemonFortProto) Location {
	return Location{fort.Latitude, fort.Longitude}
}

func NormalizeLon(v float64) float64 {
	v = math.Mod(v+180.0, 360.0)
	if v < 0 {
		v += 360.0
	}
	return v - 180.0
}

var UseCurrentLocation = Location{0, 0}

func SplitRoute(route []Location, parts int) [][]Location {
	var routes [][]Location
	splitLen := len(route) / parts
	startSplit := 0
	if parts > 1 {
		for x := 0; x < parts-1; x++ {
			routes = append(routes, route[startSplit:(startSplit+splitLen)])
			startSplit += splitLen
		}
	}
	routes = append(routes, route[startSplit:])

	return routes
}

func (l ApiLocation) ToLocation() Location {
	return Location{l.Latitude, l.Longitude}
}
