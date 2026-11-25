package decoder

import (
	"context"

	"golbat/db"
	pb "golbat/grpc"
)

// gRPC adapter for the Wayfarer POI API: requests convert to ApiWayfarerPoiScan
// and run through WayfarerPoiScanEndpoint; results mirror ApiWayfarerPoiResult.

func wayfarerPoiScanRequestFromProto(req *pb.WayfarerPoiScanRequest) ApiWayfarerPoiScan {
	return ApiWayfarerPoiScan{
		Min:          latLonFromProto(req.GetMin()),
		Max:          latLonFromProto(req.GetMax()),
		Limit:        int(req.GetLimit()),
		UpdatedAfter: req.GetUpdatedAfter(),
		InGame:       req.InGame,
	}
}

func wayfarerPoiToProto(r *ApiWayfarerPoiResult) *pb.WayfarerPoi {
	return &pb.WayfarerPoi{
		Id:                 r.Id,
		Lat:                r.Lat,
		Lon:                r.Lon,
		Name:               r.Name,
		Description:        r.Description,
		Url:                r.Url,
		InGame:             r.InGame,
		FirstSeenTimestamp: r.FirstSeenTimestamp,
		Updated:            r.Updated,
	}
}

func wayfarerPoisToProto(results []*ApiWayfarerPoiResult) []*pb.WayfarerPoi {
	out := make([]*pb.WayfarerPoi, 0, len(results))
	for _, r := range results {
		out = append(out, wayfarerPoiToProto(r))
	}
	return out
}

// GrpcScanWayfarerPois runs WayfarerPoiScanEndpoint; the error is a database one.
func GrpcScanWayfarerPois(ctx context.Context, db db.DbDetails, req *pb.WayfarerPoiScanRequest) (*pb.WayfarerPoiScanResponse, error) {
	res, err := WayfarerPoiScanEndpoint(ctx, db, wayfarerPoiScanRequestFromProto(req))
	if err != nil {
		return nil, err
	}
	return &pb.WayfarerPoiScanResponse{
		WayfarerPois: wayfarerPoisToProto(res.WayfarerPois),
		LimitReached: res.LimitReached,
	}, nil
}

// GrpcGetWayfarerPois answers a batched by-id lookup. Unknown ids are omitted.
func GrpcGetWayfarerPois(ctx context.Context, db db.DbDetails, ids []string) ([]*pb.WayfarerPoi, error) {
	res, err := GetWayfarerPois(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	return wayfarerPoisToProto(res), nil
}
