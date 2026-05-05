package tz

import (
	_ "time/tzdata"

	"github.com/ringsaturn/tzf"
)

var finder tzf.F

func init() {
	var err error
	finder, err = tzf.NewFullFinder() // Disk size about 17MB.
	if err != nil {
		panic(err)
	}
}

func SearchTimezone(lat, lng float64) string {
	return finder.GetTimezoneName(lng, lat)
}
