package geo

import "golbat/pogo"

type Location struct {
	Latitude  float64
	Longitude float64
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
