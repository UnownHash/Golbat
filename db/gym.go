package db

import (
	"context"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

func ClearOldGyms(ctx context.Context, db DbDetails, cellId uint64, gymIds []string) ([]string, error) {
	fortIds := []FortId{}
	query, args, _ := sqlx.In("SELECT id FROM gym WHERE deleted = 0 AND cell_id = ? AND id NOT IN (?);", cellId, gymIds)
	query = db.GeneralDb.Rebind(query)
	err := db.GeneralDb.SelectContext(ctx, &fortIds, query, args...)
	if len(fortIds) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// convert slices of struct to slices of string
	var list []string
	for _, element := range fortIds {
		list = append(list, element.Id)
	}

	log.Debugf("Query to find old gyms in cell %d - gyms: %v - query: %s", cellId, list, query)
	query2, args2, _ := sqlx.In("UPDATE gym SET deleted = 1 WHERE id IN (?)", list)
	query2 = db.GeneralDb.Rebind(query2)

	_, err2 := db.GeneralDb.ExecContext(ctx, query2, args2...)
	if err2 != nil {
		return nil, err
	}

	return list, nil
}
