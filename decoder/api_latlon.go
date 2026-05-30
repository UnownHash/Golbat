package decoder

import (
	"encoding/json"
	"fmt"

	"golbat/geo"

	"github.com/danielgtaylor/huma/v2"
)

// ApiLatLon is an API coordinate. It accepts the canonical {"lat":..,"lon":..}
// form used by the other Golbat endpoints, and also the longer
// {"latitude":..,"longitude":..} spelling for backward compatibility (the shape
// the internal geo.Location previously exposed). Internally it converts to
// geo.Location via Location().
type ApiLatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Location converts to the internal geo.Location used by the spatial search.
func (l ApiLatLon) Location() geo.Location {
	return geo.Location{Latitude: l.Lat, Longitude: l.Lon}
}

// UnmarshalJSON accepts either lat/lon (preferred) or latitude/longitude.
func (l *ApiLatLon) UnmarshalJSON(b []byte) error {
	var raw struct {
		Lat       *float64 `json:"lat"`
		Lon       *float64 `json:"lon"`
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch {
	case raw.Lat != nil:
		l.Lat = *raw.Lat
	case raw.Latitude != nil:
		l.Lat = *raw.Latitude
	default:
		return fmt.Errorf(`missing latitude: provide "lat" (preferred) or "latitude"`)
	}
	switch {
	case raw.Lon != nil:
		l.Lon = *raw.Lon
	case raw.Longitude != nil:
		l.Lon = *raw.Longitude
	default:
		return fmt.Errorf(`missing longitude: provide "lon" (preferred) or "longitude"`)
	}
	return nil
}

// Schema implements huma.SchemaProvider so the OpenAPI documents (and Huma
// validates) both accepted spellings. Without this, Huma would generate the
// schema from the struct's lat/lon tags and reject latitude/longitude under its
// additionalProperties:false default.
func (ApiLatLon) Schema(huma.Registry) *huma.Schema {
	num := func(desc string) *huma.Schema {
		return &huma.Schema{Type: huma.TypeNumber, Format: "double", Description: desc}
	}
	return &huma.Schema{
		Type:        huma.TypeObject,
		Description: `Coordinate. Provide "lat"/"lon" (preferred) or "latitude"/"longitude".`,
		Properties: map[string]*huma.Schema{
			"lat":       num("Latitude (preferred)"),
			"lon":       num("Longitude (preferred)"),
			"latitude":  num("Latitude (alias for lat)"),
			"longitude": num("Longitude (alias for lon)"),
		},
	}
}
