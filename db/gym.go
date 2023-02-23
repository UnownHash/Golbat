package db

import (
	"context"
	"github.com/jmoiron/sqlx"
)

func FindOldGyms(ctx context.Context, db DbDetails, cellId uint64, gymIds []string) ([]string, error) {
	fortIds := []FortId{}
	var query string
	var args []interface{}
	if len(gymIds) == 0 {
		query, args, _ = sqlx.In("SELECT id FROM gym WHERE deleted = 0 AND cell_id = ?;", cellId, gymIds)
	} else {
		query, args, _ = sqlx.In("SELECT id FROM gym WHERE deleted = 0 AND cell_id = ? AND id NOT IN (?);", cellId, gymIds)
	}
	query = db.GeneralDb.Rebind(query)
	err := db.GeneralDb.SelectContext(ctx, &fortIds, query, args...)
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
	if err != nil {
		return err
	}
	return nil
}
