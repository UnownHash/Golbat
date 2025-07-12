package decoder

import (
	"context"
	"database/sql"
	"errors"
	"golbat/db"
	"golbat/pogo"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
)

// Hyperlocal struct for hyperlocal experiment data
type Hyperlocal struct {
	ExperimentId      int32   `db:"experiment_id" json:"experiment_id"`
	StartMs           int64   `db:"start_ms" json:"start_ms"`
	EndMs             int64   `db:"end_ms" json:"end_ms"`
	Lat               float64 `db:"lat" json:"lat"`
	Lon               float64 `db:"lon" json:"lon"`
	RadiusM           float64 `db:"radius_m" json:"radius_m"`
	ChallengeBonusKey string  `db:"challenge_bonus_key" json:"challenge_bonus_key"`
	UpdatedMs         int64   `db:"updated_ms" json:"updated_ms"`
}

// HyperlocalKey represents the composite primary key for hyperlocal records
type HyperlocalKey struct {
	ExperimentId int32   `json:"experiment_id"`
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
}

func (h *Hyperlocal) getKey() HyperlocalKey {
	return HyperlocalKey{
		ExperimentId: h.ExperimentId,
		Lat:          h.Lat,
		Lon:          h.Lon,
	}
}

func (h *Hyperlocal) updateFromHyperlocalProto(data *pogo.HyperlocalExperimentClientProto, timestampMs int64) {
	h.ExperimentId = data.ExperimentId
	h.StartMs = data.StartMs
	h.EndMs = data.EndMs
	h.Lat = data.LatDegrees
	h.Lon = data.LngDegrees
	h.RadiusM = data.EventRadiusM
	h.ChallengeBonusKey = data.ChallengeBonusKey
	h.UpdatedMs = timestampMs
}

func getHyperlocalRecord(ctx context.Context, db db.DbDetails, key HyperlocalKey) (*Hyperlocal, error) {
	// Check cache first using HyperlocalKey directly
	if cachedItem := hyperlocalCache.Get(key); cachedItem != nil {
		hyperlocal := cachedItem.Value()
		return &hyperlocal, nil
	}

	hyperlocal := Hyperlocal{}
	err := db.GeneralDb.GetContext(ctx, &hyperlocal,
		`SELECT experiment_id, start_ms, end_ms, lat, lon, radius_m, challenge_bonus_key, updated_ms
         FROM hyperlocal
         WHERE experiment_id = ? AND lat = ? AND lon = ?`, key.ExperimentId, key.Lat, key.Lon)
	statsCollector.IncDbQuery("select hyperlocal", err)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}
	return &hyperlocal, nil
}

func saveHyperlocalRecord(ctx context.Context, details db.DbDetails, hyperlocal *Hyperlocal) {
	key := hyperlocal.getKey()
	oldHyperlocal, _ := getHyperlocalRecord(ctx, details, key)

	if oldHyperlocal != nil && !hasChangesHyperlocal(oldHyperlocal, hyperlocal) {
		return
	}

	if oldHyperlocal == nil {
		res, err := details.GeneralDb.NamedExecContext(ctx, `
			INSERT INTO hyperlocal (
				experiment_id, start_ms, end_ms, lat, lon, radius_m, challenge_bonus_key, updated_ms
			) VALUES (
				:experiment_id, :start_ms, :end_ms, :lat, :lon, :radius_m, :challenge_bonus_key, :updated_ms
			)
			`, hyperlocal)
		statsCollector.IncDbQuery("insert hyperlocal", err)
		if err != nil {
			log.Errorf("insert hyperlocal %+v: %s", key, err)
			return
		}
		_ = res
	} else {
		res, err := details.GeneralDb.NamedExecContext(ctx, `
			UPDATE hyperlocal SET
				start_ms = :start_ms,
				end_ms = :end_ms,
				radius_m = :radius_m,
				challenge_bonus_key = :challenge_bonus_key,
				updated_ms = :updated_ms
			WHERE experiment_id = :experiment_id AND lat = :lat AND lon = :lon
			`, hyperlocal)
		statsCollector.IncDbQuery("update hyperlocal", err)
		if err != nil {
			log.Errorf("update hyperlocal %+v: %s", key, err)
			return
		}
		_ = res
	}
	hyperlocalCache.Set(key, *hyperlocal, ttlcache.DefaultTTL)
}

func hasChangesHyperlocal(old *Hyperlocal, new *Hyperlocal) bool {
	return old.StartMs != new.StartMs ||
		old.EndMs != new.EndMs ||
		old.RadiusM != new.RadiusM ||
		old.ChallengeBonusKey != new.ChallengeBonusKey ||
		old.UpdatedMs < new.UpdatedMs
}

func ClearHyperlocalCache() {
	hyperlocalCache.DeleteAll()
}
