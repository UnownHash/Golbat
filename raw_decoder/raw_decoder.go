package raw_decoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golbat/grpc"
	"golbat/pogo"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

type RawDecoder interface {
	GetProtoDataFromHTTP(http.Header, []byte) (*ProtoData, error)
	GetProtoDataFromGRPC(*grpc.RawProtoRequest) *ProtoData
	Decode(context.Context, *ProtoData, func(context.Context, *Proto))
}

type ProtoData struct {
	*CommonData
	Protos []Proto
}

func (pd ProtoData) Lat() float64 {
	l := len(pd.Protos)
	if l == 0 {
		return 0
	}
	return pd.Protos[l-1].Lat
}

func (pd ProtoData) Lon() float64 {
	l := len(pd.Protos)
	if l == 0 {
		return 0
	}
	return pd.Protos[l-1].Lon
}

type CommonData struct {
	Account     string
	Level       int
	Uuid        string
	ScanContext string
}

type Proto struct {
	*CommonData
	Method         int
	HaveAr         *bool
	Lat            float64
	Lon            float64
	base64Request  string
	base64Response string
	requestBytes   []byte
	responseBytes  []byte
}

func (pd *Proto) RequestProtoBytes() []byte {
	if pd.requestBytes != nil {
		return pd.requestBytes
	}
	if pd.base64Request == "" {
		return nil
	}
	reqBytes, err := base64.StdEncoding.DecodeString(pd.base64Request)
	if err != nil {
		return nil
	}
	pd.requestBytes = reqBytes
	return reqBytes
}

func (pd *Proto) ResponseProtoBytes() []byte {
	if pd.responseBytes != nil {
		return pd.responseBytes
	}
	if pd.base64Response == "" {
		return nil
	}
	respBytes, err := base64.StdEncoding.DecodeString(pd.base64Response)
	if err != nil {
		return nil
	}
	pd.responseBytes = respBytes
	return respBytes
}

var _ RawDecoder = (*rawDecoder)(nil)

type rawDecoder struct {
	decodeTimeout time.Duration
}

func (dec *rawDecoder) parsePogodroidBody(headers http.Header, body []byte, origin string) (*ProtoData, error) {
	const arQuestId = int(pogo.QuestType_QUEST_GEOTARGETED_AR_SCAN)

	type pogoDroidRawEntry struct {
		Lat        float64 `json:"lat"`
		Lng        float64 `json:"lng"`
		Payload    string  `json:"payload"`
		Type       int     `json:"type"`
		QuestsHeld []int   `json:"quests_held"`
	}

	var entries []pogoDroidRawEntry

	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}

	commonData := &CommonData{
		Uuid:    origin,
		Account: "Pogodroid",
		Level:   30,
	}

	protos := make([]Proto, len(entries))

	for entryIdx, entry := range entries {
		var lat, lon float64

		if entry.Lat != 0 && entry.Lng != 0 {
			lat = entry.Lat
			lon = entry.Lng
		}

		var haveAr *bool

		if entry.QuestsHeld != nil {
			for _, quest_id := range entry.QuestsHeld {
				if quest_id == arQuestId {
					value := true
					haveAr = &value
					break
				}
			}
			if haveAr == nil {
				value := false
				haveAr = &value
			}
		}

		protos[entryIdx] = Proto{
			CommonData:     commonData,
			base64Response: entry.Payload,
			Method:         entry.Type,
			HaveAr:         haveAr,
			Lat:            lat,
			Lon:            lon,
		}
	}

	return &ProtoData{
		CommonData: commonData,
		Protos:     protos,
	}, nil
}

func decodeAlternate(data map[string]interface{}, key1, key2 string) interface{} {
	if v := data[key1]; v != nil {
		return v
	}
	if v := data[key2]; v != nil {
		return v
	}
	return nil
}

func (dec *rawDecoder) GetProtoDataFromHTTP(headers http.Header, body []byte) (*ProtoData, error) {
	if origin := headers.Get("origin"); origin != "" {
		return dec.parsePogodroidBody(headers, body, origin)
	}

	var raw map[string]any

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	commonData := &CommonData{
		Level: 30,
	}

	baseProto := Proto{
		CommonData: commonData,
	}

	if v := raw["have_ar"]; v != nil {
		res, ok := v.(bool)
		if ok {
			baseProto.HaveAr = &res
		}
	}
	if v := raw["uuid"]; v != nil {
		baseProto.Uuid, _ = v.(string)
	}
	if v := raw["username"]; v != nil {
		baseProto.Account, _ = v.(string)
	}
	if v := raw["trainerlvl"]; v != nil {
		lvl, ok := v.(float64)
		if ok {
			baseProto.Level = int(lvl)
		}
	}
	if v := raw["scan_context"]; v != nil {
		baseProto.ScanContext, _ = v.(string)
	}
	if v := raw["lat_target"]; v != nil {
		baseProto.Lat, _ = v.(float64)
	}
	if v := raw["lon_target"]; v != nil {
		baseProto.Lon, _ = v.(float64)
	}

	contents, ok := raw["contents"].([]any)
	if !ok {
		return nil, errors.New("failed to decode 'contents'")
	}

	var protos []Proto

	for _, v := range contents {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		// Try to decode the payload automatically without requiring any knowledge of the
		// provider type

		base64data := decodeAlternate(entry, "data", "payload")
		method := decodeAlternate(entry, "method", "type")
		if method == nil || base64data == nil {
			log.Errorf("Error decoding raw (no method or base64data)")
			continue
		}

		proto := baseProto
		proto.base64Response, _ = base64data.(string)
		proto.Method = func() int {
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
				proto.HaveAr = &res
			}
		}

		protos = append(protos, proto)
	}
	return &ProtoData{
		CommonData: commonData,
		Protos:     protos,
	}, nil
}

func (dec *rawDecoder) GetProtoDataFromGRPC(in *grpc.RawProtoRequest) *ProtoData {
	commonData := &CommonData{
		Uuid:    in.DeviceId,
		Account: in.Username,
		Level:   int(in.TrainerLevel),
	}

	baseProto := Proto{
		CommonData: commonData,
		Lat:        float64(in.LatTarget),
		Lon:        float64(in.LonTarget),
		HaveAr:     in.HaveAr,
	}

	if in.ScanContext != nil {
		baseProto.ScanContext = *in.ScanContext
	}

	protos := make([]Proto, len(in.Contents))
	for i, v := range in.Contents {
		proto := baseProto
		proto.Method = int(v.Method)
		proto.requestBytes = v.RequestPayload
		proto.responseBytes = v.ResponsePayload
		if v.HaveAr != nil {
			proto.HaveAr = v.HaveAr
		}
		protos[i] = proto
	}
	return &ProtoData{
		CommonData: commonData,
		Protos:     protos,
	}
}

func (dec *rawDecoder) Decode(ctx context.Context, protoData *ProtoData, decodeFn func(context.Context, *Proto)) {
	for _, proto := range protoData.Protos {
		// provide independent cancellation contexts for each proto decode
		ctx, cancel := context.WithTimeout(ctx, dec.decodeTimeout)
		decodeFn(ctx, &proto)
		cancel()
	}
}

func NewRawDecoder(decodeTimeout time.Duration) RawDecoder {
	return &rawDecoder{
		decodeTimeout: decodeTimeout,
	}
}
