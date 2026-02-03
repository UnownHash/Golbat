package decoder

import (
	"fmt"

	"golbat/db"
	"golbat/decoder/writebehind"
)

// Ensure Pokemon implements Writeable
var _ writebehind.Writeable = (*Pokemon)(nil)

// WriteKey returns a unique key for this Pokemon (for squashing)
func (pokemon *Pokemon) WriteKey() string {
	return fmt.Sprintf("pokemon:%d", pokemon.Id)
}

// WriteType returns the entity type name (for metrics)
func (pokemon *Pokemon) WriteType() string {
	return "pokemon"
}

// WriteToDB performs the actual database write for this Pokemon
// This delegates to the shared direct write function
func (pokemon *Pokemon) WriteToDB(dbDetails db.DbDetails, isNewRecord bool) error {
	pokemon.Lock()
	defer pokemon.Unlock()
	return pokemonWriteDB(dbDetails, pokemon, isNewRecord)
}
