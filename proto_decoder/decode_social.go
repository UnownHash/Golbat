package proto_decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"

	"golbat/decoder"
	"golbat/pogo"
)

func (dec *ProtoDecoder) decodeSocialActionWithRequest(ctx context.Context, pogoProto PogoProto) (bool, string) {
	proxyRequestProto, err := DecodeRequestProto[pogo.ProxyRequestProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeSocialActionWithRequest("error", "request_parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	proxyResponseProto, err := DecodeResponseProto[pogo.ProxyResponseProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeSocialActionWithRequest("error", "response_parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	if proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED && proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED_AND_REASSIGNED {
		dec.statsCollector.IncDecodeSocialActionWithRequest("error", "non_success")
		return true, fmt.Sprintf("unsuccessful proxyResponseProto response %d %s", int(proxyResponseProto.Status), proxyResponseProto.Status)
	}

	switch pogo.InternalSocialAction(proxyRequestProto.GetAction()) {
	case pogo.InternalSocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS:
		dec.statsCollector.IncDecodeSocialActionWithRequest("ok", "list_friend_status")
		return true, dec.decodeGetFriendDetails(proxyResponseProto.Payload)
	case pogo.InternalSocialAction_SOCIAL_ACTION_SEARCH_PLAYER:
		dec.statsCollector.IncDecodeSocialActionWithRequest("ok", "search_player")
		return true, dec.decodeSearchPlayer(proxyRequestProto, proxyResponseProto.Payload)

	}

	dec.statsCollector.IncDecodeSocialActionWithRequest("ok", "unknown")
	return true, fmt.Sprintf("Did not process %s", pogo.InternalSocialAction(proxyRequestProto.GetAction()).String())
}

func (dec *ProtoDecoder) decodeGetFriendDetails(payload []byte) string {
	var getFriendDetailsOutProto pogo.InternalGetFriendDetailsOutProto
	getFriendDetailsError := proto.Unmarshal(payload, &getFriendDetailsOutProto)

	if getFriendDetailsError != nil {
		dec.statsCollector.IncDecodeGetFriendDetails("error", "parse")
		log.Errorf("Failed to parse %s", getFriendDetailsError)
		return fmt.Sprintf("Failed to parse %s", getFriendDetailsError)
	}

	if getFriendDetailsOutProto.GetResult() != pogo.InternalGetFriendDetailsOutProto_SUCCESS || getFriendDetailsOutProto.GetFriend() == nil {
		dec.statsCollector.IncDecodeGetFriendDetails("error", "non_success")
		return fmt.Sprintf("unsuccessful get friends details")
	}

	failures := 0

	for _, friend := range getFriendDetailsOutProto.GetFriend() {
		player := friend.GetPlayer()

		updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dec.dbDetails, player, player.PublicData, "", player.GetPlayerId())
		if updatePlayerError != nil {
			failures++
		}
	}

	dec.statsCollector.IncDecodeGetFriendDetails("ok", "")
	return fmt.Sprintf("%d players decoded on %d", len(getFriendDetailsOutProto.GetFriend())-failures, len(getFriendDetailsOutProto.GetFriend()))
}

func (dec *ProtoDecoder) decodeSearchPlayer(proxyRequestProto *pogo.ProxyRequestProto, payload []byte) string {
	var searchPlayerOutProto pogo.InternalSearchPlayerOutProto
	searchPlayerOutError := proto.Unmarshal(payload, &searchPlayerOutProto)

	if searchPlayerOutError != nil {
		log.Errorf("Failed to parse %s", searchPlayerOutError)
		dec.statsCollector.IncDecodeSearchPlayer("error", "parse")
		return fmt.Sprintf("Failed to parse %s", searchPlayerOutError)
	}

	if searchPlayerOutProto.GetResult() != pogo.InternalSearchPlayerOutProto_SUCCESS || searchPlayerOutProto.GetPlayer() == nil {
		dec.statsCollector.IncDecodeSearchPlayer("error", "non_success")
		return fmt.Sprintf("unsuccessful search player response")
	}

	var searchPlayerProto pogo.InternalSearchPlayerProto
	searchPlayerError := proto.Unmarshal(proxyRequestProto.GetPayload(), &searchPlayerProto)

	if searchPlayerError != nil || searchPlayerProto.GetFriendCode() == "" {
		dec.statsCollector.IncDecodeSearchPlayer("error", "parse")
		return fmt.Sprintf("Failed to parse %s", searchPlayerError)
	}

	player := searchPlayerOutProto.GetPlayer()
	updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dec.dbDetails, player, player.PublicData, searchPlayerProto.GetFriendCode(), "")
	if updatePlayerError != nil {
		dec.statsCollector.IncDecodeSearchPlayer("error", "update")
		return fmt.Sprintf("Failed update player %s", updatePlayerError)
	}

	dec.statsCollector.IncDecodeSearchPlayer("ok", "")
	return fmt.Sprintf("1 player decoded from SearchPlayerProto")
}
