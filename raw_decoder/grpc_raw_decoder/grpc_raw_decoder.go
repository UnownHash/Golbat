package grpc_raw_decoder

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"golbat/decoder"
	"golbat/geo"
	"golbat/grpc"
	"golbat/raw_decoder"
)

type PogoProtoGroup = raw_decoder.PogoProtoGroup
type PogoProto = raw_decoder.PogoProto
type ProtoDecoder = raw_decoder.ProtoDecoder
type ScanMetadata = raw_decoder.ScanMetadata

type GRPCPogoProto struct {
	ScanMetadata

	method   int
	haveAr   *bool
	location geo.Location

	requestBytes  []byte
	responseBytes []byte
}

func (pogoProto *GRPCPogoProto) GetMethod() int {
	return pogoProto.method
}

func (pogoProto *GRPCPogoProto) GetHaveAr() *bool {
	return pogoProto.haveAr
}

func (pogoProto *GRPCPogoProto) GetLocation() geo.Location {
	return pogoProto.location
}

func (pogoProto *GRPCPogoProto) HasRequest() bool {
	return len(pogoProto.requestBytes) > 0
}

func (pogoProto *GRPCPogoProto) DecodeRequest(dest proto.Message) error {
	if !pogoProto.HasRequest() {
		return raw_decoder.NewErrRequestProtoNotAvailable()
	}
	return proto.Unmarshal(pogoProto.requestBytes, dest)
}

func (pogoProto *GRPCPogoProto) DecodeResponse(dest proto.Message) error {
	return proto.Unmarshal(pogoProto.responseBytes, dest)
}

func (pogoProto *GRPCPogoProto) GetScanParameters() decoder.ScanParameters {
	return decoder.FindScanConfiguration(pogoProto.ScanContext, pogoProto.location)
}

type GRPCRawDecoder struct {
	protoDecoder ProtoDecoder
}

func (dec *GRPCRawDecoder) DecodeRaw(ctx context.Context, in *grpc.RawProtoRequest) {
	metadata := ScanMetadata{
		DeviceId:    in.DeviceId,
		Account:     in.Username,
		Level:       int(in.TrainerLevel),
		TimestampMs: in.Timestamp,
	}
	if metadata.TimestampMs <= 0 {
		metadata.TimestampMs = time.Now().UnixMilli()
	}

	baseProto := GRPCPogoProto{
		ScanMetadata: metadata,
		location: geo.Location{
			Latitude:  float64(in.LatTarget),
			Longitude: float64(in.LonTarget),
		},
		haveAr: in.HaveAr,
	}

	if in.ScanContext != nil {
		baseProto.ScanContext = *in.ScanContext
	}

	protoGroup := make(PogoProtoGroup, len(in.Contents))
	for protoGroupIdx, content := range in.Contents {
		proto := baseProto
		proto.method = int(content.Method)
		proto.requestBytes = content.RequestPayload
		proto.responseBytes = content.ResponsePayload
		if haveAr := content.HaveAr; haveAr != nil {
			proto.haveAr = haveAr
		}
		protoGroup[protoGroupIdx] = &proto
	}

	dec.protoDecoder.DecodeGroup(ctx, protoGroup)
}

func NewGRPCRawDecoder(protoDecoder ProtoDecoder) *GRPCRawDecoder {
	return &GRPCRawDecoder{
		protoDecoder: protoDecoder,
	}
}
