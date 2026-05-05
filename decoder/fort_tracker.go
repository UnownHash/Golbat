package decoder

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
)

// FortTracker provides memory-based tracking of forts (pokestops/gyms) per S2 cell
type FortTracker struct {
	mu sync.RWMutex

	// cellId -> CellFortState
	cells map[uint64]*FortTrackerCellState

	// fortId -> FortInfo (for quick lookup)
	forts map[string]*FortTrackerLastSeen

	// Configuration
	staleThreshold int64 // seconds after which a fort is considered stale
	minMissCount   int   // consecutive cell scans a fort must be missing before staleness
}

// FortTrackerCellState tracks the state of forts within a single S2 cell
type FortTrackerCellState struct {
	lastSeen  int64               // last time this cell was seen in GMO
	pokestops map[string]struct{} // set of pokestop IDs
	gyms      map[string]struct{} // set of gym IDs
}

// FortTrackerLastSeen holds tracker bookkeeping (cell + last-seen + type) for a fort
type FortTrackerLastSeen struct {
	cellId    uint64
	lastSeen  int64 // last time this fort was seen in GMO
	missCount int   // consecutive cell scans where this fort was absent
	isGym     bool  // faster targeted removal
}

// FortTrackerGMOContents holds fort IDs per cell extracted from a single GMO
type FortTrackerGMOContents struct {
	Pokestops []string
	Gyms      []string
	Timestamp int64 // GMO AsOfTimeMs for this cell
}

// Global fort tracker instance
var fortTracker *FortTracker

// InitFortTracker initializes the global fort tracker.
// minMissCount must be >= 1 (1 = previous behavior, >1 = require multiple
// consecutive cell scans missing the fort before flagging it stale).
func InitFortTracker(staleThresholdSeconds int64, minMissCount int) {
	if minMissCount < 1 {
		minMissCount = 1
	}
	fortTracker = &FortTracker{
		cells:          make(map[uint64]*FortTrackerCellState),
		forts:          make(map[string]*FortTrackerLastSeen),
		staleThreshold: staleThresholdSeconds,
		minMissCount:   minMissCount,
	}
	log.Infof("FortTracker: initialized with stale threshold of %d seconds, min miss count %d", staleThresholdSeconds, minMissCount)
}

// LoadFortsFromDB populates the tracker from database on startup
func LoadFortsFromDB(ctx context.Context, dbDetails db.DbDetails) error {
	if fortTracker == nil {
		return nil
	}

	startTime := time.Now()

	pokestopCount, err := loadFortKindFromDB(ctx, dbDetails, "pokestop", false)
	if err != nil {
		return err
	}

	gymCount, err := loadFortKindFromDB(ctx, dbDetails, "gym", true)
	if err != nil {
		return err
	}

	log.Infof("FortTracker: loaded %d pokestops and %d gyms from DB in %v",
		pokestopCount, gymCount, time.Since(startTime))

	return nil
}

const loadBatchSize = 30000

// loadFortKindFromDB streams non-deleted forts of a single kind into the tracker.
// `table` is a hardcoded literal (not user input), so the Sprintf is not a SQL injection vector.
func loadFortKindFromDB(ctx context.Context, dbDetails db.DbDetails, table string, isGym bool) (int, error) {
	type fortRow struct {
		Id      string `db:"id"`
		CellId  int64  `db:"cell_id"`
		Updated int64  `db:"updated"`
	}

	query := fmt.Sprintf(
		"SELECT id, cell_id, updated FROM %s WHERE deleted = 0 AND cell_id IS NOT NULL AND id > ? ORDER BY id LIMIT ?",
		table,
	)

	var totalCount int
	var lastId string

	for {
		rows := []fortRow{}
		if err := dbDetails.GeneralDb.SelectContext(ctx, &rows, query, lastId, loadBatchSize); err != nil {
			log.Errorf("FortTracker: failed to load %s - %s", table, err)
			return totalCount, err
		}

		if len(rows) == 0 {
			break
		}

		// "now" rather than row.Updated: load is a confirmation event.
		// See preloadPokestops for rationale.
		nowMs := time.Now().UnixMilli()
		fortTracker.mu.Lock()
		for _, row := range rows {
			cellId := uint64(row.CellId)
			cell := fortTracker.getOrCreateCellLocked(cellId)
			if isGym {
				cell.gyms[row.Id] = struct{}{}
			} else {
				cell.pokestops[row.Id] = struct{}{}
			}
			if nowMs > cell.lastSeen {
				cell.lastSeen = nowMs
			}
			fortTracker.forts[row.Id] = &FortTrackerLastSeen{
				cellId:   cellId,
				lastSeen: nowMs,
				isGym:    isGym,
			}
		}
		fortTracker.mu.Unlock()

		totalCount += len(rows)
		lastId = rows[len(rows)-1].Id

		if len(rows) < loadBatchSize {
			break
		}

		log.Debugf("FortTracker: loading %s... %d so far", table, totalCount)
	}

	return totalCount, nil
}

// getOrCreateCellLocked returns or creates a cell state (caller must hold lock)
func (ft *FortTracker) getOrCreateCellLocked(cellId uint64) *FortTrackerCellState {
	cell, exists := ft.cells[cellId]
	if !exists {
		cell = &FortTrackerCellState{
			pokestops: make(map[string]struct{}),
			gyms:      make(map[string]struct{}),
		}
		ft.cells[cellId] = cell
	}
	return cell
}

// RegisterFort registers a fort during bulk loading (e.g., from preload).
// Seeds cell.lastSeen so a partial first GMO does not erase preloaded forts.
// Does not rewind info.lastSeen if a more recent value is already tracked.
func (ft *FortTracker) RegisterFort(fortId string, cellId uint64, isGym bool, updatedTimestamp int64) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	cell := ft.getOrCreateCellLocked(cellId)

	if isGym {
		cell.gyms[fortId] = struct{}{}
	} else {
		cell.pokestops[fortId] = struct{}{}
	}

	if updatedTimestamp > cell.lastSeen {
		cell.lastSeen = updatedTimestamp
	}

	if existing, exists := ft.forts[fortId]; exists && updatedTimestamp <= existing.lastSeen {
		return
	}
	ft.forts[fortId] = &FortTrackerLastSeen{
		cellId:   cellId,
		lastSeen: updatedTimestamp,
		isGym:    isGym,
	}
}

// CellUpdateResult holds the results of processing a cell update
type CellUpdateResult struct {
	StalePokestops       []string // pokestops to mark deleted (not seen for staleThreshold)
	StaleGyms            []string // gyms to mark deleted (not seen for staleThreshold)
	ConvertedToGyms      []string // pokestops that became gyms (mark pokestop as deleted)
	ConvertedToPokestops []string // gyms that became pokestops (mark gym as deleted)
}

// ProcessCellUpdate processes a complete cell update from GMO and returns forts to delete.
// Logic: if fort.lastSeen < cell.lastSeen, fort is missing from cell.
// Remove when cell.lastSeen - fort.lastSeen > staleThreshold.
// Returns nil if timestamp is older than last processed for this cell, or
// far enough in the future to look implausible (clock skew / malicious input).
func (ft *FortTracker) ProcessCellUpdate(cellId uint64, pokestopIds []string, gymIds []string, timestamp int64) *CellUpdateResult {
	// Reject implausibly future timestamps so a single bad GMO cannot
	// permanently wedge a cell (any later real GMO would always be <= it).
	nowMs := time.Now().UnixMilli()
	if timestamp > nowMs+60_000 {
		log.Warnf("FortTracker: rejecting GMO with future timestamp cell=%d ts=%d now=%d", cellId, timestamp, nowMs)
		return nil
	}

	ft.mu.Lock()
	result, pendingPokestops, pendingGyms, processed := ft.processCellUpdateLocked(cellId, pokestopIds, gymIds, timestamp)
	ft.mu.Unlock()

	if !processed {
		return nil
	}

	// Hoisted out of locked section: log message formatting / slice boxing is
	// not worth holding the global tracker lock for.
	if len(pendingPokestops) > 0 {
		log.Debugf("FortTracker: cell %d has %d pokestop(s) pending removal: %v", cellId, len(pendingPokestops), pendingPokestops)
	}
	if len(pendingGyms) > 0 {
		log.Debugf("FortTracker: cell %d has %d gym(s) pending removal: %v", cellId, len(pendingGyms), pendingGyms)
	}

	return result
}

// processCellUpdateLocked is the locked core of ProcessCellUpdate.
// Caller must hold ft.mu. Returns (result, pendingPokestops, pendingGyms, processed).
// processed is false when the GMO is older than our last processed timestamp for this cell.
func (ft *FortTracker) processCellUpdateLocked(cellId uint64, pokestopIds []string, gymIds []string, timestamp int64) (*CellUpdateResult, []string, []string, bool) {
	cell := ft.getOrCreateCellLocked(cellId)

	if timestamp <= cell.lastSeen {
		return nil, nil, nil, false
	}

	result := &CellUpdateResult{}

	// Build sets of current forts from GMO. If the same id appears in both
	// pokestopIds and gymIds (Niantic-side type-change race), gym wins —
	// matches proto pack order and avoids the conversion loop fighting itself.
	currentPokestops := make(map[string]struct{}, len(pokestopIds))
	for _, id := range pokestopIds {
		currentPokestops[id] = struct{}{}
	}
	currentGyms := make(map[string]struct{}, len(gymIds))
	for _, id := range gymIds {
		currentGyms[id] = struct{}{}
	}
	for id := range currentGyms {
		delete(currentPokestops, id)
	}

	firstScan := cell.lastSeen == 0

	ft.applyPresentForts(cell, cellId, pokestopIds, currentPokestops, false, &result.ConvertedToPokestops, timestamp)
	ft.applyPresentForts(cell, cellId, gymIds, currentGyms, true, &result.ConvertedToGyms, timestamp)

	cell.lastSeen = timestamp

	// First scan: merge previously-tracked forts into the GMO set instead of
	// replacing, otherwise preloaded forts not in this partial GMO disappear
	// from cell tracking and are never checked for staleness again.
	if firstScan {
		for stopId := range cell.pokestops {
			if _, inGMO := currentPokestops[stopId]; inGMO {
				continue
			}
			if info, ok := ft.forts[stopId]; ok {
				info.lastSeen = timestamp
				info.missCount = 0
			}
			currentPokestops[stopId] = struct{}{}
		}
		for gymId := range cell.gyms {
			if _, inGMO := currentGyms[gymId]; inGMO {
				continue
			}
			if info, ok := ft.forts[gymId]; ok {
				info.lastSeen = timestamp
				info.missCount = 0
			}
			currentGyms[gymId] = struct{}{}
		}
		cell.pokestops = currentPokestops
		cell.gyms = currentGyms
		return result, nil, nil, true
	}

	var pendingPokestops, pendingGyms []string

	// A fort qualifies as stale only when both criteria are met:
	// - missing for at least staleThreshold (time-based, defends against partial GMOs)
	// - absent from at least minMissCount consecutive cell scans (count-based,
	//   defends against transient single-frame coverage gaps / level-30 gating).
	for stopId := range cell.pokestops {
		if _, inGMO := currentPokestops[stopId]; inGMO {
			continue
		}
		info, exists := ft.forts[stopId]
		if !exists {
			continue
		}
		info.missCount++
		missingDuration := timestamp - info.lastSeen
		if missingDuration >= ft.staleThreshold*1000 && info.missCount >= ft.minMissCount {
			result.StalePokestops = append(result.StalePokestops, stopId)
		} else {
			pendingPokestops = append(pendingPokestops, stopId)
		}
	}

	for gymId := range cell.gyms {
		if _, inGMO := currentGyms[gymId]; inGMO {
			continue
		}
		info, exists := ft.forts[gymId]
		if !exists {
			continue
		}
		info.missCount++
		missingDuration := timestamp - info.lastSeen
		if missingDuration >= ft.staleThreshold*1000 && info.missCount >= ft.minMissCount {
			result.StaleGyms = append(result.StaleGyms, gymId)
		} else {
			pendingGyms = append(pendingGyms, gymId)
		}
	}

	// Keep pending forts in cell tracking so they are checked again on subsequent scans
	for _, stopId := range pendingPokestops {
		currentPokestops[stopId] = struct{}{}
	}
	for _, gymId := range pendingGyms {
		currentGyms[gymId] = struct{}{}
	}

	cell.pokestops = currentPokestops
	cell.gyms = currentGyms

	return result, pendingPokestops, pendingGyms, true
}

// applyPresentForts updates lastSeen for forts present in the GMO, handles
// cell moves and type conversions. `current` is the deduplicated set for the
// fort kind being processed; ids skipped via dedup are ignored.
func (ft *FortTracker) applyPresentForts(cell *FortTrackerCellState, cellId uint64, ids []string, current map[string]struct{}, isGym bool, converted *[]string, timestamp int64) {
	for _, id := range ids {
		if _, ok := current[id]; !ok {
			continue
		}
		info, exists := ft.forts[id]
		if !exists {
			ft.forts[id] = &FortTrackerLastSeen{cellId: cellId, lastSeen: timestamp, isGym: isGym}
			if isGym {
				cell.gyms[id] = struct{}{}
			} else {
				cell.pokestops[id] = struct{}{}
			}
			continue
		}
		// Handle cell move
		if info.cellId != cellId {
			if oldCell, oldExists := ft.cells[info.cellId]; oldExists {
				if info.isGym {
					delete(oldCell.gyms, id)
				} else {
					delete(oldCell.pokestops, id)
				}
			}
			info.cellId = cellId
		}
		// Handle type change. The follow-up clear*WithLock call already logs
		// the conversion at Info level, so we keep this trace at Debug.
		if info.isGym != isGym {
			info.isGym = isGym
			*converted = append(*converted, id)
			if log.IsLevelEnabled(log.DebugLevel) {
				if isGym {
					log.Debugf("FortTracker: fort %s converted from pokestop to gym", id)
				} else {
					log.Debugf("FortTracker: fort %s converted from gym to pokestop", id)
				}
			}
		}
		info.lastSeen = timestamp
		info.missCount = 0
		// Defensive: ensure fort lives in this cell's set even if a later
		// early-return is added before cell.pokestops/gyms is reassigned.
		if isGym {
			cell.gyms[id] = struct{}{}
		} else {
			cell.pokestops[id] = struct{}{}
		}
	}
}

// RemoveFort removes a fort from tracking (called after marking as deleted)
func (ft *FortTracker) RemoveFort(fortId string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	info, exists := ft.forts[fortId]
	if !exists {
		return
	}

	if cell, cellExists := ft.cells[info.cellId]; cellExists {
		if info.isGym {
			delete(cell.gyms, fortId)
		} else {
			delete(cell.pokestops, fortId)
		}
	}

	delete(ft.forts, fortId)
}

// RestoreFort adds a fort back to tracking (called when un-deleting).
// nowMs MUST be milliseconds (matches tracker's internal unit).
func (ft *FortTracker) RestoreFort(fortId string, cellId uint64, isGym bool, nowMs int64) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	cell := ft.getOrCreateCellLocked(cellId)

	if existing, exists := ft.forts[fortId]; exists && existing.cellId != cellId {
		if oldCell, ok := ft.cells[existing.cellId]; ok {
			if existing.isGym {
				delete(oldCell.gyms, fortId)
			} else {
				delete(oldCell.pokestops, fortId)
			}
		}
	}

	if isGym {
		cell.gyms[fortId] = struct{}{}
	} else {
		cell.pokestops[fortId] = struct{}{}
	}

	ft.forts[fortId] = &FortTrackerLastSeen{
		cellId:    cellId,
		lastSeen:  nowMs,
		missCount: 0,
		isGym:     isGym,
	}
}

// GetFortTracker returns the global fort tracker instance
func GetFortTracker() *FortTracker {
	return fortTracker
}

// CellFortInfo holds information about forts in a cell for API response
type CellFortInfo struct {
	CellId    string   `json:"cell_id"`
	LastSeen  int64    `json:"last_seen"`
	Pokestops []string `json:"pokestops"`
	Gyms      []string `json:"gyms"`
}

// FortTrackerInfo holds information about a fort for API response
type FortTrackerInfo struct {
	FortId   string `json:"fort_id"`
	CellId   string `json:"cell_id"`
	LastSeen int64  `json:"last_seen"`
	IsGym    bool   `json:"is_gym"`
}

// GetCellInfo returns information about a specific cell
func (ft *FortTracker) GetCellInfo(cellId uint64) *CellFortInfo {
	if ft == nil {
		return nil
	}

	ft.mu.RLock()
	defer ft.mu.RUnlock()

	cell, exists := ft.cells[cellId]
	if !exists {
		return nil
	}

	pokestops := make([]string, 0, len(cell.pokestops))
	for stopId := range cell.pokestops {
		pokestops = append(pokestops, stopId)
	}

	gyms := make([]string, 0, len(cell.gyms))
	for gymId := range cell.gyms {
		gyms = append(gyms, gymId)
	}

	return &CellFortInfo{
		CellId:    strconv.FormatUint(cellId, 10),
		LastSeen:  cell.lastSeen,
		Pokestops: pokestops,
		Gyms:      gyms,
	}
}

// GetFortInfo returns information about a specific fort
func (ft *FortTracker) GetFortInfo(fortId string) *FortTrackerInfo {
	if ft == nil {
		return nil
	}

	ft.mu.RLock()
	defer ft.mu.RUnlock()

	fort, exists := ft.forts[fortId]
	if !exists {
		return nil
	}

	return &FortTrackerInfo{
		FortId:   fortId,
		CellId:   strconv.FormatUint(fort.cellId, 10),
		LastSeen: fort.lastSeen,
		IsGym:    fort.isGym,
	}
}

// fortKindOps captures the type-specific bits clearFortWithLock needs
// so the gym/pokestop control flow can share a single implementation.
type fortKindOps[T comparable] struct {
	kindLabel        string
	convertedToLabel string
	statDelete       string
	statConvert      string
	loadForUpdate    func(context.Context, db.DbDetails, string, string) (T, func(), error)
	saveRecord       func(context.Context, db.DbDetails, T)
	initWebhook      func(T) *FortWebhook
	setDeleted       func(T)
	isNil            func(T) bool
}

var gymClearOps = fortKindOps[*Gym]{
	kindLabel:        "gym",
	convertedToLabel: "pokestop",
	statDelete:       "gym_delete",
	statConvert:      "gym_to_pokestop",
	loadForUpdate:    getGymRecordForUpdate,
	saveRecord:       saveGymRecord,
	initWebhook:      InitWebHookFortFromGym,
	setDeleted:       func(g *Gym) { g.SetDeleted(true) },
	isNil:            func(g *Gym) bool { return g == nil },
}

var pokestopClearOps = fortKindOps[*Pokestop]{
	kindLabel:        "pokestop",
	convertedToLabel: "gym",
	statDelete:       "pokestop_delete",
	statConvert:      "pokestop_to_gym",
	loadForUpdate:    getPokestopRecordForUpdate,
	saveRecord:       savePokestopRecord,
	initWebhook:      InitWebHookFortFromPokestop,
	setDeleted:       func(p *Pokestop) { p.SetDeleted(true) },
	isNil:            func(p *Pokestop) bool { return p == nil },
}

// clearFortWithLock marks a fort as deleted while holding its object-level mutex.
// removeFromTracker=true means stale removal (drop tracker entry, fire webhook);
// false means type conversion (the *other* type still exists in the tracker).
func clearFortWithLock[T comparable](ctx context.Context, dbDetails db.DbDetails, fortId string, cellId uint64, removeFromTracker bool, ops fortKindOps[T]) {
	rec, unlock, err := ops.loadForUpdate(ctx, dbDetails, fortId, "clear"+ops.kindLabel+"WithLock")
	if err != nil {
		log.Errorf("FortTracker: failed to load %s %s - %s", ops.kindLabel, fortId, err)
		return
	}
	if ops.isNil(rec) {
		log.Warnf("FortTracker: %s %s not found in cache or database, clearing tracker entry", ops.kindLabel, fortId)
		if removeFromTracker {
			fortTracker.RemoveFort(fortId)
		}
		return
	}

	ops.setDeleted(rec)
	ops.saveRecord(ctx, dbDetails, rec)

	var fort *FortWebhook
	if removeFromTracker {
		fort = ops.initWebhook(rec)
	}
	unlock()

	if removeFromTracker {
		fortTracker.RemoveFort(fortId)
		log.Infof("FortTracker: removed %s in cell %d: %s", ops.kindLabel, cellId, fortId)
		CreateFortChangeWebhooks(fort, REMOVAL)
		statsCollector.IncFortChange(ops.statDelete)
	} else {
		log.Infof("FortTracker: marked %s as deleted (converted to %s) in cell %d: %s", ops.kindLabel, ops.convertedToLabel, cellId, fortId)
		statsCollector.IncFortChange(ops.statConvert)
	}
}

// CheckRemovedForts uses the in-memory fort tracker for fast detection.
// Iterates cellForts directly — its keyset already matches mapCells.
func CheckRemovedForts(ctx context.Context, dbDetails db.DbDetails, cellForts map[uint64]*FortTrackerGMOContents) {
	for cellId, cf := range cellForts {
		// Process cell through tracker - returns stale and converted forts.
		// nil result means this GMO timestamp is older than last processed for this cell.
		result := fortTracker.ProcessCellUpdate(cellId, cf.Pokestops, cf.Gyms, cf.Timestamp)
		if result == nil {
			continue
		}

		for _, gymId := range result.StaleGyms {
			clearFortWithLock(ctx, dbDetails, gymId, cellId, true, gymClearOps)
		}
		for _, stopId := range result.StalePokestops {
			clearFortWithLock(ctx, dbDetails, stopId, cellId, true, pokestopClearOps)
		}
		for _, stopId := range result.ConvertedToGyms {
			clearFortWithLock(ctx, dbDetails, stopId, cellId, false, pokestopClearOps)
		}
		for _, gymId := range result.ConvertedToPokestops {
			clearFortWithLock(ctx, dbDetails, gymId, cellId, false, gymClearOps)
		}
	}
}
