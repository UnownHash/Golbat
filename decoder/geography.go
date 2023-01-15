package decoder

import (
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
	"io/ioutil"
)

var featureCollection *geojson.FeatureCollection

const geojsonFilename = "geojson/geofence.json"

func ReadGeofences() error {
	geofence, err := ioutil.ReadFile(geojsonFilename)
	if err != nil {
		return err
	}

	fc, geoerr := geojson.UnmarshalFeatureCollection(geofence)
	if geoerr != nil {
		return geoerr
	}
	featureCollection = fc
	return nil
}

type areaName struct {
	parent string
	name   string
}

func matchGeofences(lat, lon float64) (areas []areaName) {
	if featureCollection == nil {
		return
	}

	p := orb.Point{lon, lat}

	for _, f := range featureCollection.Features {
		geoType := f.Geometry.GeoJSONType()
		switch geoType {
		case "Polygon":
			polygon := f.Geometry.(orb.Polygon)
			if planar.PolygonContains(polygon, p) {
				name := f.Properties.MustString("name", "unknown")
				parent := f.Properties.MustString("parent", name)
				areas = append(areas, areaName{parent: parent, name: name})
			}
		case "MultiPolygon":
			multiPolygon := f.Geometry.(orb.MultiPolygon)
			if planar.MultiPolygonContains(multiPolygon, p) {
				name := f.Properties.MustString("name", "unknown")
				parent := f.Properties.MustString("parent", name)
				areas = append(areas, areaName{parent: parent, name: name})
			}
		}
	}

	return
}
