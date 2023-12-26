package geo

import (
	"fmt"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
	"github.com/tidwall/rtree"
	"math"
)

type AreaName struct {
	Parent string
	Name   string
}

func (an *AreaName) String() string {
	parent := an.Parent != ""
	name := an.Name != ""

	if name && !parent {
		return an.Name
	}
	if parent && !name {
		return an.Parent
	}
	if parent && name {
		if an.Parent == an.Name {
			return an.Name
		}
		if an.Parent != an.Name {
			return an.Parent + "/" + an.Name
		}
	}
	return "unown"
}

type Geofence struct {
	Fence []Location
}

type BoundingBox struct {
	MinimumLatitude  float64
	MinimumLongitude float64
	MaximumLatitude  float64
	MaximumLongitude float64
}

func (fence *Geofence) GetBoundingBox() BoundingBox {
	if len(fence.Fence) == 0 {
		return BoundingBox{}
	}

	bbox := BoundingBox{
		MinimumLatitude:  fence.Fence[0].Latitude,
		MinimumLongitude: fence.Fence[0].Longitude,
		MaximumLongitude: fence.Fence[0].Longitude,
		MaximumLatitude:  fence.Fence[0].Latitude,
	}

	if len(fence.Fence) > 1 {
		for x := 1; x < len(fence.Fence); x++ {
			point := fence.Fence[x]
			if point.Latitude < bbox.MinimumLatitude {
				bbox.MinimumLatitude = point.Latitude
			}
			if point.Longitude < bbox.MinimumLongitude {
				bbox.MinimumLongitude = point.Longitude
			}
			if point.Latitude > bbox.MaximumLatitude {
				bbox.MaximumLatitude = point.Latitude
			}
			if point.Longitude > bbox.MaximumLongitude {
				bbox.MaximumLongitude = point.Longitude
			}
		}
	}
	return bbox
}

func (fence *Geofence) ToPolygonString() string {
	routeString := ""
	for _, l := range fence.Fence {
		if routeString != "" {
			routeString = routeString + ","
		}
		routeString = routeString + fmt.Sprintf("%f %f", l.Latitude, l.Longitude)
	}

	return routeString

}

// NewPolygon: Creates and returns a new pointer to a Polygon
// composed of the passed in points.  Points are
// considered to be in order such that the last point
// forms an edge with the first point.
func NewPolygon(points []Location) *Geofence {
	return &Geofence{Fence: points}
}

// Points returns the points of the current Polygon.
func (p *Geofence) Points() []Location {
	return p.Fence
}

// Add: Appends the passed in contour to the current Polygon.
func (p *Geofence) Add(point Location) {
	p.Fence = append(p.Fence, point)
}

// IsClosed returns whether or not the polygon is closed.
// TODO:  This can obviously be improved, but for now,
//
//	this should be sufficient for detecting if points
//	are contained using the raycast algorithm.
func (p *Geofence) IsClosed() bool {
	if len(p.Fence) < 3 {
		return false
	}

	return true
}

// Contains returns whether or not the current Polygon contains the passed in Point.
func (p *Geofence) Contains(point Location) bool {
	if !p.IsClosed() {
		return false
	}

	start := len(p.Fence) - 1
	end := 0

	contains := p.intersectsWithRaycast(point, p.Fence[start], p.Fence[end])

	for i := 1; i < len(p.Fence); i++ {
		if p.intersectsWithRaycast(point, p.Fence[i-1], p.Fence[i]) {
			contains = !contains
		}
	}

	return contains
}

// Using the raycast algorithm, this returns whether or not the passed in point
// Intersects with the edge drawn by the passed in start and end points.
// Original implementation: http://rosettacode.org/wiki/Ray-casting_algorithm#Go
func (p *Geofence) intersectsWithRaycast(point Location, start Location, end Location) bool {
	// Always ensure that the the first point
	// has a y coordinate that is less than the second point
	if start.Longitude > end.Longitude {

		// Switch the points if otherwise.
		start, end = end, start

	}

	// Move the point's y coordinate
	// outside of the bounds of the testing region
	// so we can start drawing a ray
	for point.Longitude == start.Longitude || point.Longitude == end.Longitude {
		newLng := math.Nextafter(point.Longitude, math.Inf(1))
		point = Location{
			point.Latitude,
			newLng,
		}
	}

	// If we are outside of the polygon, indicate so.
	if point.Longitude < start.Longitude || point.Longitude > end.Longitude {
		return false
	}

	if start.Latitude > end.Latitude {
		if point.Latitude > start.Latitude {
			return false
		}
		if point.Latitude < end.Latitude {
			return true
		}

	} else {
		if point.Latitude > end.Latitude {
			return false
		}
		if point.Latitude < start.Latitude {
			return true
		}
	}

	raySlope := (point.Longitude - start.Longitude) / (point.Latitude - start.Latitude)
	diagSlope := (end.Longitude - start.Longitude) / (end.Latitude - start.Latitude)

	return raySlope >= diagSlope
}

func LoadRtree(featureCollection *geojson.FeatureCollection) *rtree.RTreeG[*geojson.Feature] {
	if featureCollection == nil {
		return nil
	}

	var tree rtree.RTreeG[*geojson.Feature]

	for _, f := range featureCollection.Features {
		bbox := f.Geometry.Bound()
		tree.Insert(bbox.Min, bbox.Max, f)
	}

	return &tree
}

func MatchGeofencesRtree(tree *rtree.RTreeG[*geojson.Feature], lat, lon float64) (areas []AreaName) {
	if tree == nil {
		return
	}

	p := orb.Point{lon, lat}

	tree.Search([2]float64{lon, lat}, [2]float64{lon, lat}, func(min, max [2]float64, f *geojson.Feature) bool {
		geoType := f.Geometry.GeoJSONType()
		switch geoType {
		case "Polygon":
			polygon := f.Geometry.(orb.Polygon)
			if planar.PolygonContains(polygon, p) {
				name := f.Properties.MustString("name", "unknown")
				parent := f.Properties.MustString("parent", name)
				areas = append(areas, AreaName{Parent: parent, Name: name})
			}
		case "MultiPolygon":
			multiPolygon := f.Geometry.(orb.MultiPolygon)
			if planar.MultiPolygonContains(multiPolygon, p) {
				name := f.Properties.MustString("name", "unknown")
				parent := f.Properties.MustString("parent", name)
				areas = append(areas, AreaName{Parent: parent, Name: name})
			}
		}
		return true // always continue
	})

	return
}

func MatchGeofences(featureCollection *geojson.FeatureCollection, lat, lon float64) (areas []AreaName) {
	if featureCollection == nil {
		return
	}

	p := orb.Point{lon, lat}

	for _, f := range featureCollection.Features {
		geoType := f.Geometry.GeoJSONType()
		switch geoType {
		case "Polygon":
			polygon := f.Geometry.(orb.Polygon)
			if planar.PolygonContains(polygon, p) {
				name := f.Properties.MustString("name", "unknown")
				parent := f.Properties.MustString("parent", name)
				areas = append(areas, AreaName{Parent: parent, Name: name})
			}
		case "MultiPolygon":
			multiPolygon := f.Geometry.(orb.MultiPolygon)
			if planar.MultiPolygonContains(multiPolygon, p) {
				name := f.Properties.MustString("name", "unknown")
				parent := f.Properties.MustString("parent", name)
				areas = append(areas, AreaName{Parent: parent, Name: name})
			}
		}
	}

	return
}
