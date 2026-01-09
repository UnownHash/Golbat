package raw_decoder

import (
	"context"
	"golbat/decoder"
	"golbat/geo"

	"google.golang.org/protobuf/proto"
)

type ScanMetadata struct {
	Account     string
	Level       int
	DeviceId    string
	ScanContext string
	TimestampMs int64
}

func (metadata *ScanMetadata) GetAccount() string {
	return metadata.Account
}

func (metadata *ScanMetadata) GetLevel() int {
	return metadata.Level
}

func (metadata *ScanMetadata) GetDeviceId() string {
	return metadata.DeviceId
}

func (metadata *ScanMetadata) GetScanContext() string {
	return metadata.ScanContext
}

func (metadata *ScanMetadata) GetTimestampMs() int64 {
	return metadata.TimestampMs
}

type PogoProto interface {
	GetAccount() string
	GetLevel() int
	GetDeviceId() string
	GetScanContext() string
	GetTimestampMs() int64
	GetMethod() int
	GetHaveAr() *bool
	GetLocation() geo.Location
	GetScanParameters() decoder.ScanParameters
	HasRequest() bool
	DecodeRequest(dest proto.Message) error
	DecodeResponse(dest proto.Message) error
}

type PogoProtoGroup []PogoProto

func (protoGroup PogoProtoGroup) GetLastLocation() geo.Location {
	l := len(protoGroup)
	if l == 0 {
		return geo.Location{}
	}
	return protoGroup[l-1].GetLocation()
}

type ProtoDecoder interface {
	DecodeGroup(ctx context.Context, protoGroup PogoProtoGroup)
}
