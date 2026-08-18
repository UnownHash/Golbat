package decoder

import (
	"strconv"
	"strings"

	"golbat/grpc"

	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// pokemonScan is the in-memory form of a single scan-history entry used for
// Ditto detection. It mirrors grpc.PokemonScan field for field, minus the
// protobuf machinery (MessageState, sizeCache, unknownFields) that costs 48
// bytes on every entry and earns nothing while the entry only ever lives in
// memory.
//
// The protobuf remains the wire format: the golbat_internal column holds
// marshaled grpc.PokemonInternal bytes, and conversion happens only at the two
// boundaries where bytes are actually needed — populateInternal on read and
// rewriteGolbatInternal on write (the latter gated behind
// pokemon_internal_to_db, which is off by default).
//
// Fields are int32 to match the proto, so both conversions are a straight
// copy. The two bools are last so the ten int32 pack without holes.
//
// ADDING A FIELD TO grpc.PokemonScan REQUIRES ADDING IT HERE AND TO BOTH
// CONVERTERS BELOW. Miss either and the field is silently dropped at the
// boundary: it decodes out of stored bytes into a temporary that is thrown
// away, and it is never written back. No golden-bytes fixture can catch that,
// because a fixture only knows about the fields it was built with —
// TestPokemonScanCoversEveryProtoField is what fails instead.
type pokemonScan struct {
	Weather     int32
	Level       int32
	Attack      int32
	Defense     int32
	Stamina     int32
	CellWeather int32
	Pokemon     int32
	Costume     int32
	Gender      int32
	Form        int32
	Strong      bool
	Confirmed   bool
}

func (s *pokemonScan) CompressedIv() int32 {
	return s.Attack | s.Defense<<4 | s.Stamina<<8
}

func (s *pokemonScan) MustBeBoosted() bool {
	return s.Level > 30 && s.Level <= 35
}

func (s *pokemonScan) MustBeUnboosted() bool {
	return s.Level <= 5 || s.Attack < 4 || s.Defense < 4 || s.Stamina < 4
}

func (s *pokemonScan) MustHaveRerolled(other *pokemonScan) bool {
	return s.Strong != other.Strong || s.Pokemon != other.Pokemon || s.Costume != other.Costume ||
		s.Gender != other.Gender || s.Form != other.Form
}

// RemoveDittoAuxInfo for saving space when this information is no longer needed
func (s *pokemonScan) RemoveDittoAuxInfo() {
	s.CellWeather = 0
	s.Pokemon = 0
	s.Costume = 0
	s.Gender = 0
	s.Form = 0
	s.Confirmed = false
}

// String reproduces what the generated grpc.PokemonScan.String() used to put in
// the Ditto debug logs and error messages: proto text format, one line, proto
// field names, zero-valued fields omitted, "<nil>" for a nil scan. prototext
// deliberately randomises its inter-field spacing, so this is the same content
// with stable spacing rather than a byte-for-byte match.
func (s *pokemonScan) String() string {
	if s == nil {
		return "<nil>"
	}
	var sb strings.Builder
	writeInt := func(name string, value int32) {
		if value == 0 {
			return
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(name)
		sb.WriteByte(':')
		sb.WriteString(strconv.FormatInt(int64(value), 10))
	}
	writeBool := func(name string, value bool) {
		if !value {
			return
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(name)
		sb.WriteString(":true")
	}
	// Field order follows the proto field numbers, as prototext does.
	writeInt("weather", s.Weather)
	writeBool("strong", s.Strong)
	writeInt("level", s.Level)
	writeInt("attack", s.Attack)
	writeInt("defense", s.Defense)
	writeInt("stamina", s.Stamina)
	writeInt("cell_weather", s.CellWeather)
	writeInt("pokemon", s.Pokemon)
	writeInt("costume", s.Costume)
	writeInt("gender", s.Gender)
	writeInt("form", s.Form)
	writeBool("confirmed", s.Confirmed)
	return sb.String()
}

// scanHistoryFromProto converts an unmarshaled grpc.PokemonInternal into the
// in-memory scan history. Read boundary only.
func scanHistoryFromProto(internal *grpc.PokemonInternal) []*pokemonScan {
	if len(internal.ScanHistory) == 0 {
		return nil
	}
	history := make([]*pokemonScan, len(internal.ScanHistory))
	for i, entry := range internal.ScanHistory {
		// Getters rather than direct field access: proto.Unmarshal never
		// produces a nil element in a repeated message field, but the
		// getters are nil-safe for free and this is the one place the
		// elements come from outside our own code.
		history[i] = &pokemonScan{
			Weather:     entry.GetWeather(),
			Level:       entry.GetLevel(),
			Attack:      entry.GetAttack(),
			Defense:     entry.GetDefense(),
			Stamina:     entry.GetStamina(),
			CellWeather: entry.GetCellWeather(),
			Pokemon:     entry.GetPokemon(),
			Costume:     entry.GetCostume(),
			Gender:      entry.GetGender(),
			Form:        entry.GetForm(),
			Strong:      entry.GetStrong(),
			Confirmed:   entry.GetConfirmed(),
		}
	}
	return history
}

// scanHistoryToProto builds the protobuf message to marshal into
// golbat_internal. Write boundary only.
func scanHistoryToProto(history []*pokemonScan) *grpc.PokemonInternal {
	internal := &grpc.PokemonInternal{}
	if len(history) == 0 {
		return internal
	}
	internal.ScanHistory = make([]*grpc.PokemonScan, len(history))
	for i, entry := range history {
		internal.ScanHistory[i] = &grpc.PokemonScan{
			Weather:     entry.Weather,
			Level:       entry.Level,
			Attack:      entry.Attack,
			Defense:     entry.Defense,
			Stamina:     entry.Stamina,
			CellWeather: entry.CellWeather,
			Pokemon:     entry.Pokemon,
			Costume:     entry.Costume,
			Gender:      entry.Gender,
			Form:        entry.Form,
			Strong:      entry.Strong,
			Confirmed:   entry.Confirmed,
		}
	}
	return internal
}

// rewriteGolbatInternal re-marshals the in-memory scan history into the bytes
// the golbat_internal column will be written with, and trims the Ditto aux
// info that no longer needs storing. This is the write boundary: the only
// place grpc.PokemonInternal is marshaled. savePokemonRecordAsAtTime calls it
// for encounter saves only, and only with pokemon_internal_to_db enabled,
// which is off by default.
func (pokemon *Pokemon) rewriteGolbatInternal() {
	unboosted, boosted, strong := pokemon.locateAllScans()
	if unboosted != nil && boosted != nil {
		unboosted.RemoveDittoAuxInfo()
		boosted.RemoveDittoAuxInfo()
	}
	if strong != nil {
		strong.RemoveDittoAuxInfo()
	}
	marshaled, err := proto.Marshal(scanHistoryToProto(pokemon.scanHistory))
	if err != nil {
		log.Errorf("[POKEMON] Failed to marshal internal data for %d, data may be lost: %s", pokemon.Id, err)
		return
	}
	pokemon.GolbatInternal = marshaled
}
