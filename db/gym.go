package db

import (
	"context"

	"github.com/jmoiron/sqlx"
)

func FindOldGyms(ctx context.Context, db DbDetails, cellId int64) ([]string, error) {
	fortIds := []FortId{}
	err := db.GeneralDb.SelectContext(ctx, &fortIds,
		"SELECT id FROM gym WHERE deleted = 0 AND cell_id = ? AND updated < UNIX_TIMESTAMP() - 3600;", cellId)
	statsCollector.IncDbQuery("select old-gyms", err)
	if err != nil {
		return nil, err
	}
	if len(fortIds) == 0 {
		return nil, nil
	}

	// convert slices of struct to slices of string
	var list []string
	for _, element := range fortIds {
		list = append(list, element.Id)
	}
	return list, nil
}

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
