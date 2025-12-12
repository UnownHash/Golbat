package db

import (
	"context"

	"github.com/jmoiron/sqlx"
)

func ClearOldGyms(ctx context.Context, db DbDetails, gymIds []string) error {
	query, args, _ := sqlx.In("UPDATE gym SET deleted = 1 WHERE id IN (?);", gymIds)
	query = db.GeneralDb.Rebind(query)

	_, err := db.GeneralDb.ExecContext(ctx, query, args...)
	statsCollector.IncDbQuery("clear old-gyms", err)
	if err != nil {
		return err
	}
	return nil
}
