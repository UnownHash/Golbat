package proto_decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/decoder"
	"golbat/pogo"
	"golbat/raw_decoder"
)

func (dec *ProtoDecoder) decodeGetContestData(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedContestData, err := DecodeResponseProto[pogo.GetContestDataOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse GetContestDataOutProto %s", err)
		return true, fmt.Sprintf("Failed to parse GetContestDataOutProto %s", err)
	}

	// Request helps, but can be decoded without it
	decodedContestDataRequest, err := DecodeRequestProto[pogo.GetContestDataProto](pogoProto)
	if err != nil && !raw_decoder.IsErrRequestProtoNotAvailable(err) {
		log.Errorf("Failed to parse GetContestDataProto %s", err)
		return true, fmt.Sprintf("Failed to parse GetContestDataProto %s", err)
	}
	return true, decoder.UpdatePokestopWithContestData(ctx, dec.dbDetails, decodedContestDataRequest, decodedContestData)
}

func (dec *ProtoDecoder) decodeGetPokemonSizeContestEntry(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedPokemonSizeContestEntry, err := DecodeResponseProto[pogo.GetPokemonSizeLeaderboardEntryOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
		return true, fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
	}

	if decodedPokemonSizeContestEntry.Status != pogo.GetPokemonSizeLeaderboardEntryOutProto_SUCCESS {
		return true, fmt.Sprintf("Ignored GetPokemonSizeLeaderboardEntryOutProto non-success status %s", decodedPokemonSizeContestEntry.Status)
	}

	decodedPokemonSizeContestEntryRequest, err := DecodeRequestProto[pogo.GetPokemonSizeLeaderboardEntryProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
		return true, fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
	}
	return true, decoder.UpdatePokestopWithPokemonSizeContestEntry(ctx, dec.dbDetails, decodedPokemonSizeContestEntryRequest, decodedPokemonSizeContestEntry)
}
