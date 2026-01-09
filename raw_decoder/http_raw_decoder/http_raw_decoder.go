package http_raw_decoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"

	"golbat/decoder"
	"golbat/geo"
	"golbat/raw_decoder"
)

type PogoProtoGroup = raw_decoder.PogoProtoGroup
type PogoProto = raw_decoder.PogoProto
type ProtoDecoder = raw_decoder.ProtoDecoder
type ScanMetadata = raw_decoder.ScanMetadata

type HTTPPogoProto struct {
	ScanMetadata

	method   int
	haveAr   *bool
	location geo.Location

	base64Request  string
	base64Response string
}

func (pogoProto *HTTPPogoProto) GetMethod() int {
	return pogoProto.method
}

func (pogoProto *HTTPPogoProto) GetHaveAr() *bool {
	return pogoProto.haveAr
}

func (pogoProto *HTTPPogoProto) GetLocation() geo.Location {
	return pogoProto.location
}

func (pogoProto *HTTPPogoProto) HasRequest() bool {
	return len(pogoProto.base64Request) > 0
}

func (pogoProto *HTTPPogoProto) DecodeRequest(dest proto.Message) error {
	if !pogoProto.HasRequest() {
		return raw_decoder.NewErrRequestProtoNotAvailable()
	}
	requestBytes, err := base64.StdEncoding.DecodeString(pogoProto.base64Request)
	if err != nil {
		return fmt.Errorf("failed to base64 decode request proto: %w", err)
	}
	return proto.Unmarshal(requestBytes, dest)
}

func (pogoProto *HTTPPogoProto) DecodeResponse(dest proto.Message) error {
	responseBytes, err := base64.StdEncoding.DecodeString(pogoProto.base64Response)
	if err != nil {
		return fmt.Errorf("failed to base64 decode response proto: %w", err)
	}
	return proto.Unmarshal(responseBytes, dest)
}

func (pogoProto *HTTPPogoProto) GetScanParameters() decoder.ScanParameters {
	return decoder.FindScanConfiguration(pogoProto.ScanContext, pogoProto.location)
}

type HTTPRawDecoder struct {
	protoDecoder ProtoDecoder
}

func (dec *HTTPRawDecoder) decodePogodroidRaw(ctx context.Context, origin string, body []byte) error {
	var raw []map[string]interface{}

	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("failed to decode raw bytes: %w", err)
	}

	if len(raw) == 0 {
		return nil
	}

	metadata := ScanMetadata{
		DeviceId: origin,
		Account:  "Pogodroid",
		Level:    30,
	}

	location := geo.Location{}

	protoGroup := make(PogoProtoGroup, len(raw))
	for protoGroupIdx, entry := range raw {
		lat := entry["lat"]
		lng := entry["lng"]
		if lat != nil && lng != nil {
			lat_f, _ := lat.(float64)
			lng_f, _ := lng.(float64)
			if lat_f != 0 && lng_f != 0 {
				location = geo.Location{
					Latitude:  lat_f,
					Longitude: lng_f,
				}
			}
		}

		pogoProto := &HTTPPogoProto{
			ScanMetadata: metadata,
			location:     location,
			method:       int(entry["type"].(float64)),
			haveAr: func() *bool {
				if v := entry["quests_held"]; v != nil {
					return questsHeldHasARTask(v)
				}
				return nil
			}(),
		}
		pogoProto.base64Response, _ = entry["payload"].(string)

		protoGroup[protoGroupIdx] = pogoProto
	}
	dec.protoDecoder.DecodeGroup(ctx, protoGroup)
	return nil
}

func (dec *HTTPRawDecoder) decodeRawEntry(ctx context.Context, raw map[string]interface{}, requestReceivedMs int64) error {
	contents, ok := raw["contents"].([]any)
	if !ok {
		return errors.New("raw entry is missing 'contents'")
	}
	if len(contents) == 0 {
		return nil
	}

	baseProto := HTTPPogoProto{}

	{
		metadata := &baseProto.ScanMetadata
		metadata.Level = 30
		if v := raw["uuid"]; v != nil {
			metadata.DeviceId, _ = v.(string)
		}
		if v := raw["username"]; v != nil {
			metadata.Account, _ = v.(string)
		}
		if v := raw["trainerlvl"]; v != nil {
			lvl, ok := v.(float64)
			if ok {
				metadata.Level = int(lvl)
			}
		}
		if v := raw["scan_context"]; v != nil {
			metadata.ScanContext, _ = v.(string)
		}
		if v := raw["timestamp_ms"]; v != nil {
			metadata.TimestampMs, _ = v.(int64)
		}
		if metadata.TimestampMs <= 0 {
			metadata.TimestampMs = requestReceivedMs
		}
	}

	if v := raw["have_ar"]; v != nil {
		res, ok := v.(bool)
		if ok {
			baseProto.haveAr = &res
		}
	}

	if lat_target, lon_target := raw["lat_target"], raw["lon_target"]; lat_target != nil && lon_target != nil {
		baseProto.location = geo.Location{
			Latitude:  lat_target.(float64),
			Longitude: lon_target.(float64),
		}
	}

	protoGroup := make(PogoProtoGroup, len(contents))
	protoGroupIdx := 0
	for _, v := range contents {
		entry, ok := v.(map[string]any)
		if !ok || entry == nil {
			continue
		}

		// Try to decode the payload automatically without requiring any knowledge of the
		// provider type
		b64data := getValueFromMap(entry, "data", "payload")
		method := getValueFromMap(entry, "method", "type")
		if method == nil || b64data == nil {
			log.Errorf("Error decoding raw (no method or base64 data)")
			continue
		}

		proto := baseProto
		proto.base64Response, _ = b64data.(string)
		proto.method = func() int {
			if res, ok := method.(float64); ok {
				return int(res)
			}
			return 0
		}()
		if request := entry["request"]; request != nil {
			proto.base64Request, _ = request.(string)
		}
		if haveAr := entry["have_ar"]; haveAr != nil {
			res, ok := haveAr.(bool)
			if ok {
				proto.haveAr = &res
			}
		}
		protoGroup[protoGroupIdx] = &proto
		protoGroupIdx++
	}
	if protoGroupIdx == 0 {
		return errors.New("all contents were missing method and/or base64 data")
	}
	dec.protoDecoder.DecodeGroup(ctx, protoGroup[:protoGroupIdx])
	return nil
}

func (dec *HTTPRawDecoder) DecodeRaw(ctx context.Context, headers http.Header, body []byte, requestReceivedMs int64) error {
	if len(body) == 0 {
		return errors.New("raw request contains empty body")
	}

	if origin := headers.Get("origin"); origin != "" {
		if err := dec.decodePogodroidRaw(ctx, origin, body); err != nil {
			return fmt.Errorf("failed to decode raw entry: %w", err)
		}
	}

	if body[0] == '[' {
		var rawEntries []map[string]interface{}
		if err := json.Unmarshal(body, &rawEntries); err != nil {
			return fmt.Errorf("failed to decode json from array of raw entries: %w", err)
		}
		if len(rawEntries) == 0 {
			return nil
		}
		decoded := false
		for _, rawEntry := range rawEntries {
			err := dec.decodeRawEntry(ctx, rawEntry, requestReceivedMs)
			if err != nil {
				log.Errorf("failed to decode raw entry: %v", err)
				continue
			}
			decoded = true
		}
		if !decoded {
			return errors.New("no valid entry in batched entries")
		}
		return nil
	}

	var rawEntry map[string]interface{}
	if err := json.Unmarshal(body, &rawEntry); err != nil {
		return fmt.Errorf("failed to decode json from raw body: %w", err)
	}
	if err := dec.decodeRawEntry(ctx, rawEntry, requestReceivedMs); err != nil {
		return fmt.Errorf("failed to decode raw entry: %w", err)
	}
	return nil
}

func NewHTTPRawDecoder(protoDecoder ProtoDecoder) *HTTPRawDecoder {
	return &HTTPRawDecoder{
		protoDecoder: protoDecoder,
	}
}
