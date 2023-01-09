package db

import (
	"github.com/jmoiron/sqlx"
)

type Connections struct {
	PokemonDb       *sqlx.DB
	UsePokemonCache bool
	GeneralDb       *sqlx.DB
}
