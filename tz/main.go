package tz

import (
	_ "time/tzdata"

	"github.com/ringsaturn/tzf"
	tzfrel "github.com/ringsaturn/tzf-rel"
	pb "github.com/ringsaturn/tzf/gen/go/tzf/v1"
	"google.golang.org/protobuf/proto"
)

var finder tzf.F

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
