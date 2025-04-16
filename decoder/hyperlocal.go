package decoder

type Hyperlocal struct {
	ExperimentId      int32   `db:"experiment_id"`
	StartMs           int64   `db:"start_ms"`
	EndMs             int64   `db:"end_ms"`
	Lat               float64 `db:"lat"`
	Lon               float64 `db:"lon"`
	RadiusM           float64 `db:"radius_m"`
	ChallengeBonusKey string  `db:"challenge_bonus_key"`
	UpdatedMs         int64   `db:"updated_ms"`
}
