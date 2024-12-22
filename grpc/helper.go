package grpc

func (s *PokemonScan) CompressedIv() int32 {
	return s.Attack | s.Defense<<4 | s.Stamina<<8
}

func (s *PokemonScan) MustBeBoosted() bool {
	return s.Level > 30 && s.Level <= 35
}

func (s *PokemonScan) MustBeUnboosted() bool {
	return s.Level <= 5 || s.Attack < 4 || s.Defense < 4 || s.Stamina < 4
}

func (s *PokemonScan) MustHaveRerolled(other *PokemonScan) bool {
	return s.Strong != other.Strong || s.Pokemon != other.Pokemon || s.Costume != other.Costume ||
		s.Gender != other.Gender || s.Form != other.Form
}

// RemoveDittoAuxInfo for saving space when this information is no longer needed
func (s *PokemonScan) RemoveDittoAuxInfo() {
	s.CellWeather = 0
	s.Pokemon = 0
	s.Costume = 0
	s.Gender = 0
	s.Form = 0
	s.Confirmed = false
}
