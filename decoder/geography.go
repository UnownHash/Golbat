package decoder

import (
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
)

var featureCollection *geojson.FeatureCollection

func ReadGeofences() {
	geofence, err := ioutil.ReadFile("geojson/geofence.json")
	if err != nil {
		log.Errorf("Error reading geofence file: %s", err)
		return
	}

	fc, geoerr := geojson.UnmarshalFeatureCollection(geofence)
	if geoerr != nil {
		log.Errorf("Error unmarshalling geofence file: %s", geoerr)
		return
	}
	featureCollection = fc
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
