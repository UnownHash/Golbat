package grpc

func (s *PokemonScan) CompressedIv() int32 {
	return s.GetAttack() | s.GetDefense()<<4 | s.GetStamina()<<8
}

func (s *PokemonScan) MustBeBoosted() bool {
	return s.GetLevel() > 30 && s.GetLevel() <= 35
}

func (s *PokemonScan) MustBeUnboosted() bool {
	return s.GetLevel() <= 5 || s.GetAttack() < 4 || s.GetDefense() < 4 || s.GetStamina() < 4
}

func (s *PokemonScan) MustHaveRerolled(other *PokemonScan) bool {
	return s.GetStrong() != other.GetStrong() || s.GetPokemon() != other.GetPokemon() || s.GetCostume() != other.GetCostume() ||
		s.GetGender() != other.GetGender() || s.GetForm() != other.GetForm()
}

// RemoveDittoAuxInfo for saving space when this information is no longer needed
func (s *PokemonScan) RemoveDittoAuxInfo() {
	s.SetCellWeather(0)
	s.SetPokemon(0)
	s.SetCostume(0)
	s.SetGender(0)
	s.SetForm(0)
	s.SetConfirmed(false)
}
