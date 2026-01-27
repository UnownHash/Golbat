package decoder

import "golbat/grpc"

// PokemonScanNative is a native Go equivalent of grpc.PokemonScan
// that can be safely copied (no embedded mutex from protobuf MessageState)
type PokemonScanNative struct {
	// from display
	Weather int32
	Strong  bool
	// from encounter
	Level   int32
	Attack  int32
	Defense int32
	Stamina int32
	// for Ditto detection
	CellWeather int32
	Pokemon     int32
	Costume     int32
	Gender      int32
	Form        int32
	// this is set if there is only one non-strong IV set but we were able to confirm it for some reason
	Confirmed bool
}

// PokemonInternalNative is a native Go equivalent of grpc.PokemonInternal
// that can be safely copied (no embedded mutex from protobuf MessageState)
type PokemonInternalNative struct {
	ScanHistory []*PokemonScanNative
}

// CompressedIv returns a compressed representation of the IV values
func (s *PokemonScanNative) CompressedIv() int32 {
	return s.Attack | s.Defense<<4 | s.Stamina<<8
}

// MustBeBoosted returns true if the level indicates the Pokemon must be weather boosted
func (s *PokemonScanNative) MustBeBoosted() bool {
	return s.Level > 30 && s.Level <= 35
}

// MustBeUnboosted returns true if the level/IVs indicate the Pokemon must be unboosted
func (s *PokemonScanNative) MustBeUnboosted() bool {
	return s.Level <= 5 || s.Attack < 4 || s.Defense < 4 || s.Stamina < 4
}

// MustHaveRerolled returns true if the Pokemon must have rerolled based on comparing with another scan
func (s *PokemonScanNative) MustHaveRerolled(other *PokemonScanNative) bool {
	return s.Strong != other.Strong || s.Pokemon != other.Pokemon || s.Costume != other.Costume ||
		s.Gender != other.Gender || s.Form != other.Form
}

// RemoveDittoAuxInfo clears auxiliary info for saving space when no longer needed
func (s *PokemonScanNative) RemoveDittoAuxInfo() {
	s.CellWeather = 0
	s.Pokemon = 0
	s.Costume = 0
	s.Gender = 0
	s.Form = 0
	s.Confirmed = false
}

// String returns a string representation of the scan
func (s *PokemonScanNative) String() string {
	// Delegate to the protobuf String() for consistent formatting
	return s.ToProto().String()
}

// ToProto converts a PokemonScanNative to a grpc.PokemonScan protobuf
func (s *PokemonScanNative) ToProto() *grpc.PokemonScan {
	if s == nil {
		return nil
	}
	return &grpc.PokemonScan{
		Weather:     s.Weather,
		Strong:      s.Strong,
		Level:       s.Level,
		Attack:      s.Attack,
		Defense:     s.Defense,
		Stamina:     s.Stamina,
		CellWeather: s.CellWeather,
		Pokemon:     s.Pokemon,
		Costume:     s.Costume,
		Gender:      s.Gender,
		Form:        s.Form,
		Confirmed:   s.Confirmed,
	}
}

// PokemonScanFromProto converts a grpc.PokemonScan protobuf to a PokemonScanNative
func PokemonScanFromProto(pb *grpc.PokemonScan) *PokemonScanNative {
	if pb == nil {
		return nil
	}
	return &PokemonScanNative{
		Weather:     pb.Weather,
		Strong:      pb.Strong,
		Level:       pb.Level,
		Attack:      pb.Attack,
		Defense:     pb.Defense,
		Stamina:     pb.Stamina,
		CellWeather: pb.CellWeather,
		Pokemon:     pb.Pokemon,
		Costume:     pb.Costume,
		Gender:      pb.Gender,
		Form:        pb.Form,
		Confirmed:   pb.Confirmed,
	}
}

// ToProto converts a PokemonInternalNative to a grpc.PokemonInternal protobuf
func (p *PokemonInternalNative) ToProto() *grpc.PokemonInternal {
	if p == nil {
		return nil
	}
	pb := &grpc.PokemonInternal{
		ScanHistory: make([]*grpc.PokemonScan, len(p.ScanHistory)),
	}
	for i, scan := range p.ScanHistory {
		pb.ScanHistory[i] = scan.ToProto()
	}
	return pb
}

// PokemonInternalFromProto converts a grpc.PokemonInternal protobuf to a PokemonInternalNative
func PokemonInternalFromProto(pb *grpc.PokemonInternal) PokemonInternalNative {
	if pb == nil {
		return PokemonInternalNative{}
	}
	native := PokemonInternalNative{
		ScanHistory: make([]*PokemonScanNative, len(pb.ScanHistory)),
	}
	for i, scan := range pb.ScanHistory {
		native.ScanHistory[i] = PokemonScanFromProto(scan)
	}
	return native
}
