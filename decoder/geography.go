package decoder

import (
	"encoding/json"
	"github.com/tidwall/rtree"
	"golbat/geo"
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

var statsTree *rtree.RTreeG[*geojson.Feature]
var nestTree *rtree.RTreeG[*geojson.Feature]
var kojiUrl = ""
var kojiBearerToken = ""

const geojsonFilename = "geojson/geofence.json"

const kojiCacheFilename = "cache/koji_geofence.json"

// const nestFilename = "geojson/nests.json"

func SetKojiUrl(geofenceUrl string, bearerToken string) {
	log.Print("Setting Koji Info "+geofenceUrl+"with bearer token:"+bearerToken != "")
	kojiUrl = geofenceUrl
	kojiBearerToken = bearerToken
}

func GetKojiGeofence(url string) (*geojson.FeatureCollection, error) {
	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+kojiBearerToken)
	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	log.Infof("KOJI: Response %s", resp.Status)

	defer resp.Body.Close()

	var response KojiResponse
	err = json.NewDecoder(resp.Body).Decode(&response)

	return &response.Data, err
}

func ReadGeofences() error {
	var statsFeatureCollection *geojson.FeatureCollection

	if kojiUrl != "" {
		fc, err := GetKojiGeofence(kojiUrl)
		if err != nil {
			log.Warnf("KOJI: Unable to get geofence from koji - %s", err)
			geofence, err := ioutil.ReadFile(kojiCacheFilename)
			if err != nil {
				log.Warnf("KOJI: Unable to read cached geofence - %s", err)
			} else {
				fc, geoerr := geojson.UnmarshalFeatureCollection(geofence)
				if geoerr != nil {
					log.Warnf("KOJI: Unable to parse cached geofence - %s", geoerr)
				} else {
					statsFeatureCollection = fc
					log.Infof("KOJI: Loaded cached geofence")

				}
			}
		} else {
			log.Infof("KOJI: Loaded geofence from koji, caching")
			bytes, _ := json.MarshalIndent(fc, "", "  ")
			ioutil.WriteFile(kojiCacheFilename, []byte(bytes), 0644)
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
		log.Infof("GEO: Loaded geofence from geofence.json")
		statsFeatureCollection = fc
	}

	statsTree = geo.LoadRtree(statsFeatureCollection)

	return nil
}

func MatchStatsGeofence(lat, lon float64) []geo.AreaName {
	return geo.MatchGeofencesRtree(statsTree, lat, lon)
}

func MatchNestGeofence(lat, lon float64) []geo.AreaName {
	return geo.MatchGeofencesRtree(nestTree, lat, lon)
}
