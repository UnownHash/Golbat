package geo

import (
	"fmt"
	"math"
)

type Geofence struct {
	Fence []Location
}

type BoundingBox struct {
	MinimumLatitude  float64
	MinimumLongitude float64
	MaximumLatitude  float64
	MaximumLongitude float64
}

func (f *Geofence) GetBoundingBox() BoundingBox {
	if len(f.Fence) == 0 {
		return BoundingBox{}
	}

	bbox := BoundingBox{
		MinimumLatitude:  f.Fence[0].Latitude,
		MinimumLongitude: f.Fence[0].Longitude,
		MaximumLongitude: f.Fence[0].Longitude,
		MaximumLatitude:  f.Fence[0].Latitude,
	}

	if len(f.Fence) > 1 {
		for x := 1; x < len(f.Fence); x++ {
			point := f.Fence[x]
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

func (f *Geofence) ToPolygonString() string {
	routeString := ""
	for _, l := range f.Fence {
		if routeString != "" {
			routeString = routeString + ","
		}
		routeString = routeString + fmt.Sprintf("%f %f", l.Latitude, l.Longitude)
	}

	return routeString

}

// NewPolygon Creates and returns a new pointer to a Polygon
// composed of the passed in points.  Points are
// considered to be in order such that the last point
// forms an edge with the first point.
//
//goland:noinspection GoUnusedExportedFunction
func NewPolygon(points []Location) *Geofence {
	return &Geofence{Fence: points}
}

// Points returns the points of the current Polygon.
func (f *Geofence) Points() []Location {
	return f.Fence
}

// Add Appends the passed in contour to the current Polygon.
func (f *Geofence) Add(point Location) {
	f.Fence = append(f.Fence, point)
}

// IsClosed returns whether or not the polygon is closed.
// TODO:  This can obviously be improved, but for now,
//
//	this should be sufficient for detecting if points
//	are contained using the raycast algorithm.
func (f *Geofence) IsClosed() bool {
	if len(f.Fence) < 3 {
		return false
	}

	return true
}

// Contains returns whether or not the current Polygon contains the passed in Point.
func (f *Geofence) Contains(point Location) bool {
	if !f.IsClosed() {
		return false
	}

	start := len(f.Fence) - 1
	end := 0

	contains := f.intersectsWithRaycast(point, f.Fence[start], f.Fence[end])

	for i := 1; i < len(f.Fence); i++ {
		if f.intersectsWithRaycast(point, f.Fence[i-1], f.Fence[i]) {
			contains = !contains
		}
	}

	return contains
}

// Using the raycast algorithm, this returns whether or not the passed in point
// Intersects with the edge drawn by the passed in start and end points.
// Original implementation: http://rosettacode.org/wiki/Ray-casting_algorithm#Go
func (f *Geofence) intersectsWithRaycast(point Location, start Location, end Location) bool {
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
