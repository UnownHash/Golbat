package decoder

import (
	"github.com/paulmach/orb/geojson"
	"io/ioutil"
)

var statsFeatureCollection *geojson.FeatureCollection
var nestFeatureCollection *geojson.FeatureCollection

const geojsonFilename = "geojson/geofence.json"
const nestFilename = "geojson/nests.json"

func ReadGeofences() error {
	geofence, err := ioutil.ReadFile(geojsonFilename)
	if err != nil {
		return err
	}

	fc, geoerr := geojson.UnmarshalFeatureCollection(geofence)
	if geoerr != nil {
		return geoerr
	}
	statsFeatureCollection = fc
	return nil
}

//func ReadNestGeofences() error {
//	geofence, err := ioutil.ReadFile(nestFilename)
//	if err != nil {
//		return err
//	}
//
//	fc, geoerr := geojson.UnmarshalFeatureCollection(geofence)
//	if geoerr != nil {
//		return geoerr
//	}
//	nestFeatureCollection = fc
//	return nil
//}
