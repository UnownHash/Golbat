package decoder

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
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

// lastGeofenceHash gates geofence re-indexing on actual content change, so
// a no-op Koji reload neither rebuilds the S2 lookup nor drops the served
// one. Holds a [32]byte sha256 sum.
var lastGeofenceHash atomic.Value

// geofenceContentChanged records and reports whether the geofence source
// bytes differ from the previously indexed ones. nil/empty source counts
// as changed (never skip indexing on an unhashable source).
func geofenceContentChanged(source []byte) bool {
	if len(source) == 0 {
		return true
	}
	sum := sha256.Sum256(source)
	if prev, ok := lastGeofenceHash.Load().([32]byte); ok && prev == sum {
		return false
	}
	lastGeofenceHash.Store(sum)
	return true
}

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
	var sourceBytes []byte

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
					sourceBytes = geofence
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
			sourceBytes = bytes

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
		sourceBytes = geofence
	}

	if statsFeatureCollection == nil {
		// Koji fetch failed with no usable cache: keep serving the previous
		// indexes rather than publishing an empty tree (which would silently
		// unmatch every pokemon until the next successful reload).
		return errors.New("no geofence data available; keeping previous geofences")
	}

	if !geofenceContentChanged(sourceBytes) {
		log.Infof("GEO: geofence content unchanged; keeping existing indexes")
		return nil
	}

	// Publish the rtree immediately — matching works through the (compiled)
	// polygon path while the S2 lookup builds.
	newStatsTree := geo.LoadRtree(statsFeatureCollection)
	statsTree.Store(newStatsTree)

	if config.Config.Tuning.S2CellLookup {
		// Drop the previous lookup for the duration of the rebuild so the
		// whole window serves the NEW fences consistently through the
		// compiled-polygon fallback — otherwise cells interior to the old
		// fences would keep answering from the old epoch while everything
		// else used the new one. The unchanged-content fast path above
		// means this only ever happens on a real edit.
		var rebuilding *geo.S2CellLookup
		statsS2Lookup.Store(rebuilding)

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
