package proto_decoder

import (
	"fmt"
	"golbat/pogo"
	"strings"

	"google.golang.org/protobuf/proto"
)

func getMethodName(method int, trimString bool) string {
	if val, ok := pogo.Method_name[int32(method)]; ok {
		if trimString && strings.HasPrefix(val, "METHOD_") {
			return strings.TrimPrefix(val, "METHOD_")
		}
		return val
	}
	return fmt.Sprintf("#%d", method)
}

func isCellNotEmpty(mapCell *pogo.ClientMapCellProto) bool {
	return len(mapCell.Stations) > 0 || len(mapCell.Fort) > 0 || len(mapCell.WildPokemon) > 0 || len(mapCell.NearbyPokemon) > 0 || len(mapCell.CatchablePokemon) > 0
}

func cellContainsForts(mapCell *pogo.ClientMapCellProto) bool {
	return len(mapCell.Fort) > 0
}

// ProtoMessagePtr is a generic type that must be a pointer and
// satisfy proto.Message interface
type ProtoMessagePtr[T any] interface {
	proto.Message
	*T
}

// DecodeRequestProto will unmarshal bytes into a destination type T and
// return a pointer to T. Because of type inference, only T needs to be specified.
// E.g.:
// gmoReqProto, err := DecodeRequestProto[pogo.GetMapObjectsProto](pogoProto)
func DecodeRequestProto[T any, TP ProtoMessagePtr[T]](pogoProto PogoProto) (TP, error) {
	var dest T

	ptr := TP(&dest)
	err := pogoProto.DecodeRequest(ptr)
	if err != nil {
		return nil, err
	}
	return ptr, nil
}

// DecodeResponeProto will unmarshal bytes into a destination type T and
// return a pointer to T. Because of type inference, only T needs to be specified.
// E.g.:
// gmoRespProto, err := DecodeResponseProto[pogo.GetMapObjectsOutProto](pogoProto)
func DecodeResponseProto[T any, TP ProtoMessagePtr[T]](pogoProto PogoProto) (TP, error) {
	var dest T

	ptr := TP(&dest)
	err := pogoProto.DecodeResponse(ptr)
	if err != nil {
		return nil, err
	}
	return ptr, nil
}
