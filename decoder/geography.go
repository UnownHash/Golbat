package decoder

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
)

type KojiResponse struct {
	Message    string                    `json:"message"`
	Data       geojson.FeatureCollection `json:"data"`
	Status     string                    `json:"status"`
	StatusCode int                       `json:"status_code"`
	// Stats      KojiStats     `json:"stats"`
}

var statsFeatureCollection *geojson.FeatureCollection
var nestFeatureCollection *geojson.FeatureCollection
var kojiGeofenceUrl = ""
var kojiNestGeofenceUrl = ""
var kojiBearerToken = ""

const geojsonFilename = "geojson/geofence.json"
const nestFilename = "geojson/nests.json"

func SetKojiUrl(geofenceUrl string, nestGeofenceUrl string, bearerToken string) {
	log.Print("Setting Koji Info " + geofenceUrl + " " + nestGeofenceUrl + " " + bearerToken + "")
	kojiGeofenceUrl = geofenceUrl
	kojiNestGeofenceUrl = nestGeofenceUrl
	kojiBearerToken = bearerToken
}

func GetKojiGeofence(url string) (*geojson.FeatureCollection, error) {
	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		log.Warnf("KOJI: Unable to create new request", url, err)
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+kojiBearerToken)
	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		log.Warnf("KOJI: unable to connect to %s - %s", url, err)
		return nil, err
	}

	log.Debugf("KOJI: Response %s", resp.Status)

	defer resp.Body.Close()

	var response KojiResponse
	err = json.NewDecoder(resp.Body).Decode(&response)

	return &response.Data, err
}

func ReadGeofences() error {
	if kojiGeofenceUrl != "" {
		fc, err := GetKojiGeofence(kojiGeofenceUrl)
		if err != nil {
			return err
		} else {
			statsFeatureCollection = fc
		}
	} else {
		geofence, err := ioutil.ReadFile(geojsonFilename)
		if err != nil {
			return err
		}
		fc, geoerr := geojson.UnmarshalFeatureCollection(geofence)
		if geoerr != nil {
			return geoerr
		}
		statsFeatureCollection = fc
	}
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
