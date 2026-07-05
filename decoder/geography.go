package decoder

import (
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"

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

// s2BuildGeneration guards against a slow, stale S2 build (from an older
// geofence reload) overwriting a newer one: only the latest generation may
// publish its result.
var s2BuildGeneration atomic.Int64

var kojiUrl = ""
var kojiBearerToken = ""

// kojiHTTPClient bounds the Koji fetch — http.DefaultClient has no timeout,
// and a hung Koji must not wedge geofence reloads.
var kojiHTTPClient = &http.Client{Timeout: 30 * time.Second}

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

	resp, err := kojiHTTPClient.Do(req)

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
			geofence, err := os.ReadFile(kojiCacheFilename)
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
			if err := os.WriteFile(kojiCacheFilename, bytes, 0644); err != nil {
				log.Warnf("KOJI: Unable to cache geofence - %s", err)
			}
			statsFeatureCollection = fc

		}
	} else {
		geofence, err := os.ReadFile(geojsonFilename)
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

	// Publish the rtree immediately — matching works through the (compiled)
	// polygon path while the S2 lookup builds.
	newStatsTree := geo.LoadRtree(statsFeatureCollection)
	statsTree.Store(newStatsTree)

	if config.Config.Tuning.S2CellLookup {
		// The S2 covering can take a while for large projects; build it off
		// the caller's thread so neither startup nor a geofence reload
		// blocks on it. The generation counter makes a stale build lose to
		// a newer reload's.
		gen := s2BuildGeneration.Add(1)
		go func() {
			start := time.Now()
			built := geo.BuildS2LookupFromFeatures(statsFeatureCollection)
			if s2BuildGeneration.Load() == gen {
				statsS2Lookup.Store(built)
				log.Infof("GEO: S2 lookup ready after %s", time.Since(start).Round(time.Millisecond))
			} else {
				log.Infof("GEO: discarding stale S2 lookup build (newer reload in progress)")
			}
		}()
	} else {
		var disabled *geo.S2CellLookup
		statsS2Lookup.Store(disabled)
	}

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
	tree, _ := statsTree.Load().(*rtree.RTreeG[*geo.CompiledFence])
	return geo.MatchGeofencesRtree(tree, lat, lon)
}

func MatchNestGeofence(lat, lon float64) []geo.AreaName {
	tree, _ := nestTree.Load().(*rtree.RTreeG[*geo.CompiledFence])
	return geo.MatchGeofencesRtree(tree, lat, lon)
}
