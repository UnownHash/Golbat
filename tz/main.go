package tz

import _ "time/tzdata"

import (
	"github.com/ringsaturn/tzf"
	tzfrel "github.com/ringsaturn/tzf-rel"
	"github.com/ringsaturn/tzf/pb"
	"google.golang.org/protobuf/proto"
)

var finder *tzf.Finder

func init() {
	input := &pb.Timezones{}

	// Lite data, about 11MB
	//dataFile := tzfrel.LiteData

	// Full data, about 83.5MB
	dataFile := tzfrel.FullData

	if err := proto.Unmarshal(dataFile, input); err != nil {
		panic(err)
	}
	var err error
	finder, err = tzf.NewFinderFromPB(input)
	if err != nil {
		panic(err)
	}
}

func SearchTimezone(lat, lng float64) string {
	return finder.GetTimezoneName(lng, lat)
}
