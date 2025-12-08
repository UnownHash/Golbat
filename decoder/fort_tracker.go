package decoder

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
)

// FortTracker provides memory-based tracking of forts (pokestops/gyms) per S2 cell
type FortTracker struct {
	mu sync.RWMutex

	// cellId -> CellFortState
	cells map[uint64]*CellFortState

	// fortId -> FortInfo (for quick lookup)
	forts map[string]*FortInfo

	// Configuration
	staleThreshold int64 // seconds after which a fort is considered stale
}

// CellFortState tracks the state of forts within a single S2 cell
type CellFortState struct {
	lastSeen  int64               // last time this cell was seen in GMO
	pokestops map[string]struct{} // set of pokestop IDs
	gyms      map[string]struct{} // set of gym IDs
}

// FortInfo tracks individual fort metadata
type FortInfo struct {
	cellId   uint64
	lastSeen int64 // last time this fort was seen in GMO
	isGym    bool  // faster targeted removal
}

// CellFortsData holds fort IDs per cell from GMO processing
type CellFortsData struct {
	Pokestops []string
	Gyms      []string
}

// Global fort tracker instance
var fortTracker *FortTracker

// InitFortTracker initializes the global fort tracker
func InitFortTracker(staleThresholdSeconds int64) {
	fortTracker = &FortTracker{
		cells:          make(map[uint64]*CellFortState),
		forts:          make(map[string]*FortInfo),
		staleThreshold: staleThresholdSeconds,
	}
	log.Infof("FortTracker: initialized with stale threshold of %d seconds", staleThresholdSeconds)
}

// LoadFortsFromDB populates the tracker from database on startup
func LoadFortsFromDB(ctx context.Context, dbDetails db.DbDetails) error {
	if fortTracker == nil {
		return nil
	}

	startTime := time.Now()

	// Load pokestops
	pokestopCount, err := loadPokestopsFromDB(ctx, dbDetails)
	if err != nil {
		return err
	}

	// Load gyms
	gymCount, err := loadGymsFromDB(ctx, dbDetails)
	if err != nil {
		return err
	}

	log.Infof("FortTracker: loaded %d pokestops and %d gyms from DB in %v",
		pokestopCount, gymCount, time.Since(startTime))

	return nil
}

const loadBatchSize = 30000

func loadPokestopsFromDB(ctx context.Context, dbDetails db.DbDetails) (int, error) {
	type pokestopRow struct {
		Id      string `db:"id"`
		CellId  int64  `db:"cell_id"`
		Updated int64  `db:"updated"`
	}

	var totalCount int
	var lastId string

	for {
		rows := []pokestopRow{}
		err := dbDetails.GeneralDb.SelectContext(ctx, &rows,
			"SELECT id, cell_id, updated FROM pokestop WHERE deleted = 0 AND cell_id IS NOT NULL AND id > ? ORDER BY id LIMIT ?",
			lastId, loadBatchSize)
		if err != nil {
			log.Errorf("FortTracker: failed to load pokestops - %s", err)
			return totalCount, err
		}

		if len(rows) == 0 {
			break
		}

		fortTracker.mu.Lock()
		for _, row := range rows {
			cellId := uint64(row.CellId)
			cell := fortTracker.getOrCreateCellLocked(cellId)
			cell.pokestops[row.Id] = struct{}{}
			fortTracker.forts[row.Id] = &FortInfo{
				cellId:   cellId,
				lastSeen: row.Updated,
				isGym:    false,
			}
		}
		fortTracker.mu.Unlock()

		totalCount += len(rows)
		lastId = rows[len(rows)-1].Id

		if len(rows) < loadBatchSize {
			break
		}

		log.Debugf("FortTracker: loading pokestops... %d so far", totalCount)
	}

	return totalCount, nil
}

func loadGymsFromDB(ctx context.Context, dbDetails db.DbDetails) (int, error) {
	type gymRow struct {
		Id      string `db:"id"`
		CellId  int64  `db:"cell_id"`
		Updated int64  `db:"updated"`
	}

	var totalCount int
	var lastId string

	for {
		rows := []gymRow{}
		err := dbDetails.GeneralDb.SelectContext(ctx, &rows,
			"SELECT id, cell_id, updated FROM gym WHERE deleted = 0 AND cell_id IS NOT NULL AND id > ? ORDER BY id LIMIT ?",
			lastId, loadBatchSize)
		if err != nil {
			log.Errorf("FortTracker: failed to load gyms - %s", err)
			return totalCount, err
		}

		if len(rows) == 0 {
			break
		}

		fortTracker.mu.Lock()
		for _, row := range rows {
			cellId := uint64(row.CellId)
			cell := fortTracker.getOrCreateCellLocked(cellId)
			cell.gyms[row.Id] = struct{}{}
			fortTracker.forts[row.Id] = &FortInfo{
				cellId:   cellId,
				lastSeen: row.Updated,
				isGym:    true,
			}
		}
		fortTracker.mu.Unlock()

		totalCount += len(rows)
		lastId = rows[len(rows)-1].Id

		if len(rows) < loadBatchSize {
			break
		}

		log.Debugf("FortTracker: loading gyms... %d so far", totalCount)
	}

	return totalCount, nil
}

// getOrCreateCellLocked returns or creates a cell state (caller must hold lock)
func (ft *FortTracker) getOrCreateCellLocked(cellId uint64) *CellFortState {
	cell, exists := ft.cells[cellId]
	if !exists {
		cell = &CellFortState{
			pokestops: make(map[string]struct{}),
			gyms:      make(map[string]struct{}),
		}
		ft.cells[cellId] = cell
	}
	return cell
}

// UpdateFort updates tracking for a single fort seen in GMO
func (ft *FortTracker) UpdateFort(fortId string, cellId uint64, isGym bool, now int64) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	// Get or create cell
	cell := ft.getOrCreateCellLocked(cellId)

	// Check if fort moved cells
	if existing, exists := ft.forts[fortId]; exists && existing.cellId != cellId {
		// Fort moved to a different cell - remove from old cell
		oldCell, oldExists := ft.cells[existing.cellId]
		if oldExists {
			if existing.isGym {
				delete(oldCell.gyms, fortId)
			} else {
				delete(oldCell.pokestops, fortId)
			}
		}
	}

	// Add to current cell
	if isGym {
		cell.gyms[fortId] = struct{}{}
	} else {
		cell.pokestops[fortId] = struct{}{}
	}

	// Update fort info
	ft.forts[fortId] = &FortInfo{
		cellId:   cellId,
		lastSeen: now,
		isGym:    isGym,
	}
}

// ProcessCellUpdate processes a complete cell update from GMO and returns forts to delete.
// Logic: if fort.lastSeen < cell.lastSeen, fort is missing from cell.
// Remove when cell.lastSeen - fort.lastSeen > staleThreshold.
func (ft *FortTracker) ProcessCellUpdate(cellId uint64, pokestopIds []string, gymIds []string, now int64) (stalePokestops []string, staleGyms []string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	cell := ft.getOrCreateCellLocked(cellId)

	// Build sets of current forts from GMO
	currentPokestops := make(map[string]struct{}, len(pokestopIds))
	for _, id := range pokestopIds {
		currentPokestops[id] = struct{}{}
	}
	currentGyms := make(map[string]struct{}, len(gymIds))
	for _, id := range gymIds {
		currentGyms[id] = struct{}{}
	}

	// Check if this is the first time we're seeing this cell
	firstScan := cell.lastSeen == 0

	// Update lastSeen for forts present in GMO and handle cell moves / type changes
	for _, id := range pokestopIds {
		if info, exists := ft.forts[id]; exists {
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
			// Handle type change (gym -> pokestop)
			if info.isGym {
				info.isGym = false
				log.Infof("FortTracker: fort %s converted from gym to pokestop", id)
			}
			info.lastSeen = now
		} else {
			ft.forts[id] = &FortInfo{cellId: cellId, lastSeen: now, isGym: false}
		}
	}
	for _, id := range gymIds {
		if info, exists := ft.forts[id]; exists {
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
			// Handle type change (pokestop -> gym)
			if !info.isGym {
				info.isGym = true
				log.Infof("FortTracker: fort %s converted from pokestop to gym", id)
			}
			info.lastSeen = now
		} else {
			ft.forts[id] = &FortInfo{cellId: cellId, lastSeen: now, isGym: true}
		}
	}

	// Update cell lastSeen
	cell.lastSeen = now

	// Skip stale check on first scan - we need at least one prior scan to compare against
	if firstScan {
		cell.pokestops = currentPokestops
		cell.gyms = currentGyms
		return nil, nil
	}

	// Check forts in cell: if fort.lastSeen < cell.lastSeen, it's missing
	var pendingPokestops, pendingGyms []string

	for stopId := range cell.pokestops {
		if _, inGMO := currentPokestops[stopId]; inGMO {
			continue
		}
		if info, exists := ft.forts[stopId]; exists {
			missingDuration := now - info.lastSeen
			if missingDuration >= ft.staleThreshold {
				stalePokestops = append(stalePokestops, stopId)
			} else {
				pendingPokestops = append(pendingPokestops, stopId)
			}
		}
	}

	for gymId := range cell.gyms {
		if _, inGMO := currentGyms[gymId]; inGMO {
			continue
		}
		if info, exists := ft.forts[gymId]; exists {
			missingDuration := now - info.lastSeen
			if missingDuration >= ft.staleThreshold {
				staleGyms = append(staleGyms, gymId)
			} else {
				pendingGyms = append(pendingGyms, gymId)
			}
		}
	}

	if len(pendingPokestops) > 0 {
		log.Debugf("FortTracker: cell %d has %d pokestop(s) pending removal: %v", cellId, len(pendingPokestops), pendingPokestops)
	}
	if len(pendingGyms) > 0 {
		log.Debugf("FortTracker: cell %d has %d gym(s) pending removal: %v", cellId, len(pendingGyms), pendingGyms)
	}

	// Update cell fort sets with current GMO data
	cell.pokestops = currentPokestops
	cell.gyms = currentGyms

	return stalePokestops, staleGyms
}

// RemoveFort removes a fort from tracking (called after marking as deleted)
func (ft *FortTracker) RemoveFort(fortId string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	info, exists := ft.forts[fortId]
	if !exists {
		return
	}

	// Remove from cell
	if cell, cellExists := ft.cells[info.cellId]; cellExists {
		if info.isGym {
			delete(cell.gyms, fortId)
		} else {
			delete(cell.pokestops, fortId)
		}
	}

	// Remove from forts map
	delete(ft.forts, fortId)
}

// RestoreFort adds a fort back to tracking (called when un-deleting)
func (ft *FortTracker) RestoreFort(fortId string, cellId uint64, isGym bool, now int64) {
	ft.UpdateFort(fortId, cellId, isGym, now)
}

// GetFortTracker returns the global fort tracker instance
func GetFortTracker() *FortTracker {
	return fortTracker
}
