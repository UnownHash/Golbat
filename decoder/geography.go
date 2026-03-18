package decoder

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"sync/atomic"

	"golbat/config"
	"golbat/geo"

	"github.com/golang/geo/s2"
	"github.com/paulmach/orb/geojson"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/rtree"
)

type KojiResponse struct {
	Message    string                    `json:"message"`
	Data       geojson.FeatureCollection `json:"data"`
	Status     string                    `json:"status"`
	StatusCode int                       `json:"status_code"`
	// Stats      KojiStats     `json:"stats"`
}

var statsTree atomic.Value
var nestTree atomic.Value
var statsS2Lookup atomic.Value
var kojiUrl = ""
var kojiBearerToken = ""

const geojsonFilename = "geojson/geofence.json"

const kojiCacheFilename = "cache/koji_geofence.json"

// const nestFilename = "geojson/nests.json"

func SetKojiUrl(geofenceUrl string, bearerToken string) {
	if geofenceUrl == "" {
		return
	}
	log.Infof("Setting koji url to %s with bearer token: %t", geofenceUrl, bearerToken != "")
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

	newStatsTree := geo.LoadRtree(statsFeatureCollection)
	var newStatsS2Lookup *geo.S2CellLookup
	if config.Config.Tuning.S2CellLookup {
		newStatsS2Lookup = geo.BuildS2LookupFromFeatures(statsFeatureCollection)
	}

	statsTree.Store(newStatsTree)
	statsS2Lookup.Store(newStatsS2Lookup)

	return nil
}

func MatchStatsGeofence(lat, lon float64) []geo.AreaName {
	return MatchStatsGeofenceWithCell(lat, lon, 0)
}

func MatchStatsGeofenceWithCell(lat, lon float64, cellId uint64) []geo.AreaName {
	lookup, _ := statsS2Lookup.Load().(*geo.S2CellLookup)
	if cellId != 0 && lookup != nil {
		if areas := lookup.Lookup(s2.CellID(cellId)); len(areas) > 0 {
			return areas
		}
	}
	tree, _ := statsTree.Load().(*rtree.RTreeG[*geojson.Feature])
	return geo.MatchGeofencesRtree(tree, lat, lon)
}

func MatchNestGeofence(lat, lon float64) []geo.AreaName {
	tree, _ := nestTree.Load().(*rtree.RTreeG[*geojson.Feature])
	return geo.MatchGeofencesRtree(tree, lat, lon)
}
