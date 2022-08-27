package tz

import (
	timezone "github.com/evanoberholster/timezoneLookup/v2"
	log "github.com/sirupsen/logrus"
	"os"
	"syscall"
)

var tzc timezone.Timezonecache

var dbFilename string = "cache/tz"
var cacheFilename string = "cache/tz-geoJSON.zip"

func init() {
	f, err := os.OpenFile(dbFilename, syscall.O_RDWR, 0644)
	if err != nil {
		downloadAndBuild()
		return
	}
	defer f.Close()
	if err = tzc.Load(f); err != nil {
		downloadAndBuild()
		return
	}
}

func SearchTimezone(lat, lng float64) (timezone.Result, error) {
	return tzc.Search(lat, lng)
}

func downloadAndBuild() (err error) {
	log.Infof("Downloading timezone database")
	var total int
	err = timezone.ImportZipFile(cacheFilename, timezone.DefaultURL, func(tz timezone.Timezone) error {
		total += len(tz.Polygons)
		tzc.AddTimezone(tz)
		return nil
	})
	if err != nil {
		return err
	}
	if err = tzc.Save(dbFilename); err != nil {
		return err
	}
	log.Infof("Timezone - %d Polygons added", total)
	log.Infof("Timezone - Saved Timezone data to %s", dbFilename)
	return nil
}
