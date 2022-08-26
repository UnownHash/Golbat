package decoder

import (
	"github.com/jmoiron/sqlx"
)

type DbDetails struct {
	PokemonDb *sqlx.DB
	GeneralDb *sqlx.DB
}
