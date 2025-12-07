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
	lastSeen  int64
	pokestops map[string]struct{} // set of pokestop IDs
	gyms      map[string]struct{} // set of gym IDs
}

// FortInfo tracks individual fort metadata
type FortInfo struct {
	cellId   uint64
	lastSeen int64
	isGym    bool
}

// FortType for tell apart pokestops and gyms
type TrackedFortType int

const (
	TrackedPokestop TrackedFortType = iota
	TrackedGym
)

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
	log.Infof("FortTracker initialized with stale threshold of %d seconds", staleThresholdSeconds)
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

	log.Infof("FortTracker loaded %d pokestops and %d gyms from DB in %v",
		pokestopCount, gymCount, time.Since(startTime))

	return nil
}

const loadBatchSize = 50000

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
			log.Errorf("FortTracker: failed to load pokestops: %s", err)
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

		log.Debugf("FortTracker: loaded %d pokestops so far...", totalCount)
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
			log.Errorf("FortTracker: failed to load gyms: %s", err)
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

		log.Debugf("FortTracker: loaded %d gyms so far...", totalCount)
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
// This is the main entry point called after processing all forts in a cell.
func (ft *FortTracker) ProcessCellUpdate(cellId uint64, pokestopIds []string, gymIds []string, now int64) (stalePokestops []string, staleGyms []string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	cell := ft.getOrCreateCellLocked(cellId)

	// Build sets of current forts
	currentPokestops := make(map[string]struct{}, len(pokestopIds))
	for _, id := range pokestopIds {
		currentPokestops[id] = struct{}{}
	}

	currentGyms := make(map[string]struct{}, len(gymIds))
	for _, id := range gymIds {
		currentGyms[id] = struct{}{}
	}

	// Find pokestops that were in cell before but not in current GMO
	for stopId := range cell.pokestops {
		if _, found := currentPokestops[stopId]; !found {
			// Pokestop was here before, not anymore - check if stale
			if info, exists := ft.forts[stopId]; exists {
				if now-info.lastSeen > ft.staleThreshold {
					stalePokestops = append(stalePokestops, stopId)
				}
			}
		}
	}

	// Find gyms that were in cell before but not in current GMO
	for gymId := range cell.gyms {
		if _, found := currentGyms[gymId]; !found {
			// Gym was here before, not anymore - check if stale
			if info, exists := ft.forts[gymId]; exists {
				if now-info.lastSeen > ft.staleThreshold {
					staleGyms = append(staleGyms, gymId)
				}
			}
		}
	}

	// Update cell state with current forts
	cell.lastSeen = now
	cell.pokestops = currentPokestops
	cell.gyms = currentGyms

	// Update lastSeen for all current forts
	for _, id := range pokestopIds {
		if info, exists := ft.forts[id]; exists {
			info.lastSeen = now
		} else {
			ft.forts[id] = &FortInfo{
				cellId:   cellId,
				lastSeen: now,
				isGym:    false,
			}
		}
	}
	for _, id := range gymIds {
		if info, exists := ft.forts[id]; exists {
			info.lastSeen = now
		} else {
			ft.forts[id] = &FortInfo{
				cellId:   cellId,
				lastSeen: now,
				isGym:    true,
			}
		}
	}

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

// GetStats returns current tracker statistics
func (ft *FortTracker) GetStats() (cellCount, pokestopCount, gymCount int) {
	ft.mu.RLock()
	defer ft.mu.RUnlock()

	cellCount = len(ft.cells)
	for _, info := range ft.forts {
		if info.isGym {
			gymCount++
		} else {
			pokestopCount++
		}
	}
	return
}

// GetFortTracker returns the global fort tracker instance
func GetFortTracker() *FortTracker {
	return fortTracker
}
