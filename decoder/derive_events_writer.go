package decoder

import (
	"bufio"
	"cmp"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/geo/s2"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/tz"
	"golbat/util"
)

// deriveEventsCellLevel is the S2 level of the only location a record
// carries. Level 14 (~1 km) matches how the game partitions regional spawn
// differences, so a regional effect is attributable to its cells. The cost:
// a shared events file reveals where encounters happened to about 1 km
// (the earlier level 10 was ~10 km).
const deriveEventsCellLevel = 14

const (
	deriveEventsFilePrefix = "derive-events-"
	deriveEventsSaltFile   = "derive-events.salt"
	deriveEventsDayLayout  = "2006-01-02"
	// deriveEventsQueueSize is the capacity of the writer's events channel.
	deriveEventsQueueSize = 65536
	// deriveEventsOpenRetry is how long the writer drops lines after failing
	// to open the day's file before trying again.
	deriveEventsOpenRetry = time.Minute
)

// deriveEventsWriter owns the derivation events log: it appends one
// deviation row per check that disagreed on any dimension, and one summary
// row per (pokemon_id, form, strong, boosted, utc_offset) per bucket with
// per-dimension agreement counts — every check counts there, deviations
// included. Files are dir/derive-events-YYYY-MM-DD.jsonl by UTC write date;
// a finished day is gzipped and files past retainDays are deleted.
//
// Everything but the background gzip runs on one goroutine (run, or a test
// calling handle/tick/close directly), so the writer's state takes no lock.
type deriveEventsWriter struct {
	dir        string
	bucket     time.Duration
	retainDays int
	now        func() time.Time
	salt       []byte

	// events is drained by run; closing it makes run close the writer.
	events chan deriveEventCheck
	// submitMu orders submit against shutdown: submit sends under the read
	// lock and shutdown closes events under the write lock after setting
	// closed, so a send can never reach a closed channel.
	submitMu sync.RWMutex
	closed   bool
	done     chan struct{} // closed when run returns; nil if run was never started

	file *os.File
	buf  *bufio.Writer
	day  string // UTC date of file; "" when none is open

	bucketStart time.Time // zero when no bucket is open
	summaries   map[deriveEventsSummaryKey]*deriveEventsSummary

	zones map[s2.CellID]*time.Location // per deriveEventsCellLevel cell

	// While the day's file cannot be opened, lines are dropped and the open
	// is retried at most once per deriveEventsOpenRetry, so a missing or
	// unwritable dir logs once a minute, not once per line.
	openRetryAt  time.Time // zero when not in an open-failure outage
	openFailures int       // failed opens in the current outage
	droppedLines int       // lines dropped in the current outage

	compressing sync.WaitGroup
}

type deriveEventsSummaryKey struct {
	PokemonID, Form int32
	Strong, Boosted bool
	UTCOffset       int32
}

type deriveEventsDimCount struct {
	OK int `json:"ok"`
	N  int `json:"n"`
}

// add counts one check: n when the dimension had an opinion, ok when it agreed.
func (c *deriveEventsDimCount) add(s dimState) {
	if s == dimNoOpinion {
		return
	}
	c.N++
	if s == dimAgree {
		c.OK++
	}
}

type deriveEventsSummary struct {
	Kind          string `json:"kind"`
	Bucket        int64  `json:"bucket"`
	BucketSeconds int64  `json:"bucket_seconds"`
	PokemonID     int32  `json:"pokemon_id"`
	Form          int32  `json:"form"`
	Strong        bool   `json:"strong"`
	Boosted       bool   `json:"boosted"`
	UTCOffset     int32  `json:"utc_offset"`
	Checked       int    `json:"checked"`

	IVs     deriveEventsDimCount `json:"ivs"`
	Level   deriveEventsDimCount `json:"level"`
	Size    deriveEventsDimCount `json:"size"`
	Quick   deriveEventsDimCount `json:"quick"`
	Charged deriveEventsDimCount `json:"charged"`
	// SizeRolls is [min, max] of the raw size roll, keyed by the OBSERVED
	// size class, over checks where size had an opinion.
	SizeRolls map[string]*[2]float64 `json:"size_rolls"`
}

type deriveEventsDeviation struct {
	Kind          string        `json:"kind"`
	Ts            int64         `json:"ts"`
	UTCOffset     int32         `json:"utc_offset"`
	EncounterID   uint64        `json:"encounter_id,string"` // exceeds 2^53; see derivationLogPayload
	PokemonID     int32         `json:"pokemon_id"`
	Form          int32         `json:"form"`
	Costume       int32         `json:"costume"`
	Strong        bool          `json:"strong"`
	Boosted       bool          `json:"boosted"`
	Weather       int32         `json:"weather"`
	Cell          string        `json:"cell"`
	SpawnID       int64         `json:"spawn_id"`
	FirstSeen     uint32        `json:"first_seen"`
	Account       string        `json:"account"`
	Observed      derivedBranch `json:"observed"`
	Derived       derivedBranch `json:"derived"`
	Opposite      derivedBranch `json:"opposite"`
	SizeRoll      float64       `json:"size_roll"`
	ObservedMove1 int32         `json:"observed_move_1"`
	ObservedMove2 int32         `json:"observed_move_2"`
	DerivedMove1  int32         `json:"derived_move_1"`
	DerivedMove2  int32         `json:"derived_move_2"`
	Move1InPool   bool          `json:"move_1_in_pool"`
	Move2InPool   bool          `json:"move_2_in_pool"`
	Agree         struct {
		IVs     string `json:"ivs"`
		Level   string `json:"level"`
		Size    string `json:"size"`
		Quick   string `json:"quick"`
		Charged string `json:"charged"`
	} `json:"agree"`
}

func (s dimState) label() string {
	switch s {
	case dimAgree:
		return "agree"
	case dimDisagree:
		return "disagree"
	default:
		return "none"
	}
}

// newDeriveEventsWriter creates dir if needed, loads or creates the account
// salt, deletes files past retention and gzips plain files left from earlier
// days. A bucket under one minute becomes 15 minutes and retainDays under
// one becomes 14. The day's file is opened on the first write.
func newDeriveEventsWriter(dir string, bucket time.Duration, retainDays int,
	now func() time.Time) (*deriveEventsWriter, error) {
	if bucket < time.Minute {
		bucket = 15 * time.Minute
	}
	if retainDays < 1 {
		retainDays = 14
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("derivation events dir: %w", err)
	}
	salt, err := loadDeriveEventsSalt(filepath.Join(dir, deriveEventsSaltFile))
	if err != nil {
		return nil, err
	}
	w := &deriveEventsWriter{
		dir: dir, bucket: bucket, retainDays: retainDays, now: now, salt: salt,
		events:    make(chan deriveEventCheck, deriveEventsQueueSize),
		summaries: make(map[deriveEventsSummaryKey]*deriveEventsSummary),
		zones:     make(map[s2.CellID]*time.Location),
	}
	w.prune()
	today := now().UTC().Format(deriveEventsDayLayout)
	for _, f := range w.listFiles() {
		if f.plain && f.day < today {
			w.compressAsync(f.path)
		}
	}
	return w, nil
}

// loadDeriveEventsSalt reads the hex salt at path, creating 16 random bytes
// there if the file does not exist.
func loadDeriveEventsSalt(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("derivation events salt: %w", err)
		}
		if err := os.WriteFile(path, []byte(hex.EncodeToString(salt)+"\n"), 0o600); err != nil {
			return nil, fmt.Errorf("derivation events salt: %w", err)
		}
		return salt, nil
	}
	if err != nil {
		return nil, fmt.Errorf("derivation events salt: %w", err)
	}
	salt, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(salt) == 0 {
		return nil, fmt.Errorf("derivation events salt %s: not a hex string", path)
	}
	return salt, nil
}

// accountHash is the first 12 hex chars of sha256(salt ‖ username): stable
// per account within one dir, unlinkable to the username without the salt.
func (w *deriveEventsWriter) accountHash(username string) string {
	if username == "" {
		return ""
	}
	h := sha256.New()
	h.Write(w.salt)
	h.Write([]byte(username))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// run handles events until the channel is closed, flushing an idle bucket
// once a minute, then closes the writer.
func (w *deriveEventsWriter) run() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case ev, ok := <-w.events:
			if !ok {
				if err := w.close(); err != nil {
					log.Warnf("[DERIVE_EVENTS] close: %v", err)
				}
				return
			}
			w.handle(ev)
		case <-ticker.C:
			w.tick()
		}
	}
}

// handle counts ev in its bucket's summary and, if it deviates, writes its
// deviation row. An event for a later bucket flushes the current one first;
// one for an earlier bucket (cross-goroutine skew) counts in the current one.
func (w *deriveEventsWriter) handle(ev deriveEventCheck) {
	b := ev.Time.UTC().Truncate(w.bucket)
	if !w.bucketStart.IsZero() && b.After(w.bucketStart) {
		w.flushBucket()
	}
	if w.bucketStart.IsZero() {
		w.bucketStart = b
	}

	cell, offset := w.locate(ev)
	key := deriveEventsSummaryKey{ev.PokemonID, ev.Form, ev.Strong, ev.Boosted, offset}
	s := w.summaries[key]
	if s == nil {
		s = &deriveEventsSummary{Kind: "summary", PokemonID: ev.PokemonID, Form: ev.Form,
			Strong: ev.Strong, Boosted: ev.Boosted, UTCOffset: offset,
			SizeRolls: make(map[string]*[2]float64)}
		w.summaries[key] = s
	}
	s.Checked++
	s.IVs.add(ev.Agree.IVs)
	s.Level.add(ev.Agree.Level)
	s.Size.add(ev.Agree.Size)
	s.Quick.add(ev.Agree.Quick)
	s.Charged.add(ev.Agree.Charged)
	if ev.Agree.Size != dimNoOpinion {
		class := strconv.Itoa(int(ev.Check.Observed.Size))
		roll := ev.Check.SizeRoll
		if r := s.SizeRolls[class]; r == nil {
			s.SizeRolls[class] = &[2]float64{roll, roll}
		} else {
			r[0], r[1] = min(r[0], roll), max(r[1], roll)
		}
	}

	if ev.Agree.Deviates() {
		w.writeLine(w.deviationRow(ev, cell, offset))
	}
}

func (w *deriveEventsWriter) deviationRow(ev deriveEventCheck, cell string, offset int32) *deriveEventsDeviation {
	c := ev.Check
	d := &deriveEventsDeviation{
		Kind: "deviation", Ts: ev.Time.Unix(), UTCOffset: offset, EncounterID: ev.EncounterID,
		PokemonID: ev.PokemonID, Form: ev.Form, Costume: ev.Costume,
		Strong: ev.Strong, Boosted: ev.Boosted, Weather: ev.Weather, Cell: cell,
		SpawnID: ev.SpawnID, FirstSeen: ev.FirstSeen, Account: w.accountHash(ev.Account),
		Observed: newDerivedBranch(c.Observed), Derived: newDerivedBranch(c.Derived),
		Opposite: newDerivedBranch(c.Opposite), SizeRoll: c.SizeRoll,
		ObservedMove1: c.Move1, ObservedMove2: c.Move2,
		DerivedMove1: c.Moves.Move1, DerivedMove2: c.Moves.Move2,
		Move1InPool: deriveEventsInPool(c.Moves, c.Moves.QuickPool, c.Move1),
		Move2InPool: deriveEventsInPool(c.Moves, c.Moves.ChargedPool, c.Move2),
	}
	d.Agree.IVs, d.Agree.Level, d.Agree.Size = ev.Agree.IVs.label(), ev.Agree.Level.label(), ev.Agree.Size.label()
	d.Agree.Quick, d.Agree.Charged = ev.Agree.Quick.label(), ev.Agree.Charged.label()
	return d
}

// deriveEventsInPool is false only on evidence: resolved pools and a
// reported move that is not in its pool.
func deriveEventsInPool(moves derivedMoveResult, pool []int, move int32) bool {
	return !moves.Resolved || move == 0 || moveInPool(pool, move)
}

// locate returns ev's deriveEventsCellLevel cell token and UTC offset in minutes. The cell
// is the parent of the server's level-15 map cell; lat/lon is used only when
// there is no valid cell id. With neither, the answer is ("", 0).
func (w *deriveEventsWriter) locate(ev deriveEventCheck) (string, int32) {
	cell := s2.CellID(uint64(ev.CellID))
	if ev.CellID == 0 || !cell.IsValid() || cell.Level() < deriveEventsCellLevel {
		if ev.Lat == 0 && ev.Lon == 0 {
			return "", 0
		}
		cell = s2.CellIDFromLatLng(s2.LatLngFromDegrees(ev.Lat, ev.Lon))
	}
	cell = cell.Parent(deriveEventsCellLevel)
	loc, ok := w.zones[cell]
	if !ok {
		centre := s2.LatLngFromPoint(cell.Point())
		loc = time.UTC
		if name := tz.SearchTimezone(centre.Lat.Degrees(), centre.Lng.Degrees()); name != "" {
			if l, err := time.LoadLocation(name); err == nil {
				loc = l
			}
		}
		w.zones[cell] = loc
	}
	_, offset := ev.Time.In(loc).Zone()
	return cell.ToToken(), int32(offset / 60)
}

// tick flushes the open bucket once the clock has passed its end, so an idle
// period still produces its summary.
func (w *deriveEventsWriter) tick() {
	if !w.bucketStart.IsZero() && !w.now().Before(w.bucketStart.Add(w.bucket)) {
		w.flushBucket()
	}
}

// flushBucket writes the open bucket's summaries (sorted by key, for stable
// output) and flushes the file buffer.
func (w *deriveEventsWriter) flushBucket() {
	keys := make([]deriveEventsSummaryKey, 0, len(w.summaries))
	for k := range w.summaries {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b deriveEventsSummaryKey) int {
		return cmp.Or(cmp.Compare(a.PokemonID, b.PokemonID), cmp.Compare(a.Form, b.Form),
			boolCompare(a.Strong, b.Strong), boolCompare(a.Boosted, b.Boosted),
			cmp.Compare(a.UTCOffset, b.UTCOffset))
	})
	for _, k := range keys {
		s := w.summaries[k]
		s.Bucket, s.BucketSeconds = w.bucketStart.Unix(), int64(w.bucket/time.Second)
		w.writeLine(s)
	}
	clear(w.summaries)
	w.bucketStart = time.Time{}
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			log.Warnf("[DERIVE_EVENTS] write %s: %v", w.file.Name(), err)
		}
	}
}

func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// close flushes the open bucket and closes the file. Safe to call again.
func (w *deriveEventsWriter) close() error {
	w.flushBucket()
	return w.closeFile()
}

func (w *deriveEventsWriter) closeFile() error {
	if w.file == nil {
		return nil
	}
	err := errors.Join(w.buf.Flush(), w.file.Close())
	w.file, w.buf, w.day = nil, nil, ""
	return err
}

// writeLine appends v as one JSON line to the file for the current UTC date,
// rolling over to a new day's file first if the date has changed.
func (w *deriveEventsWriter) writeLine(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Warnf("[DERIVE_EVENTS] marshal: %v", err)
		return
	}
	today := w.now().UTC().Format(deriveEventsDayLayout)
	if w.day != today {
		if !w.openRetryAt.IsZero() && w.now().Before(w.openRetryAt) {
			w.droppedLines++
			return
		}
		w.rollover(today)
	}
	if w.buf == nil {
		w.droppedLines++
		return
	}
	_, _ = w.buf.Write(append(data, '\n')) // bufio errors are sticky; reported on Flush
}

// rollover closes the previous day's file, gzips it in the background,
// applies retention and opens today's file for append.
func (w *deriveEventsWriter) rollover(today string) {
	if w.file != nil {
		prev := w.file.Name()
		if err := w.closeFile(); err != nil {
			log.Warnf("[DERIVE_EVENTS] close %s: %v", prev, err)
		}
		w.compressAsync(prev)
		w.prune()
	}
	path := filepath.Join(w.dir, deriveEventsFilePrefix+today+".jsonl")
	// MkdirAll again so a dir deleted or remounted after startup recovers.
	err := os.MkdirAll(w.dir, 0o755)
	var f *os.File
	if err == nil {
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	}
	if err != nil {
		w.openFailures++
		w.openRetryAt = w.now().Add(deriveEventsOpenRetry)
		log.Warnf("[DERIVE_EVENTS] open %s: %v (%d lines dropped so far; retrying in %s)",
			path, err, w.droppedLines, deriveEventsOpenRetry)
		return
	}
	if !w.openRetryAt.IsZero() {
		log.Infof("[DERIVE_EVENTS] opened %s after %d failed attempts; %d lines dropped",
			path, w.openFailures, w.droppedLines)
		w.openRetryAt, w.openFailures, w.droppedLines = time.Time{}, 0, 0
	}
	w.file, w.buf, w.day = f, bufio.NewWriter(f), today
}

type deriveEventsFile struct {
	path, day string
	plain     bool
}

// listFiles returns the dir's dated event files; anything else is ignored.
func (w *deriveEventsWriter) listFiles() []deriveEventsFile {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		log.Warnf("[DERIVE_EVENTS] list %s: %v", w.dir, err)
		return nil
	}
	var files []deriveEventsFile
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), deriveEventsFilePrefix)
		if !ok || e.IsDir() {
			continue
		}
		day, plain := strings.CutSuffix(rest, ".jsonl")
		if !plain {
			if day, ok = strings.CutSuffix(rest, ".jsonl.gz"); !ok {
				continue
			}
		}
		if _, err := time.Parse(deriveEventsDayLayout, day); err != nil {
			continue
		}
		files = append(files, deriveEventsFile{filepath.Join(w.dir, e.Name()), day, plain})
	}
	return files
}

// prune deletes event files dated more than retainDays before today.
func (w *deriveEventsWriter) prune() {
	cutoff := w.now().UTC().AddDate(0, 0, -w.retainDays).Format(deriveEventsDayLayout)
	for _, f := range w.listFiles() {
		if f.day < cutoff {
			if err := os.Remove(f.path); err != nil {
				log.Warnf("[DERIVE_EVENTS] remove %s: %v", f.path, err)
			}
		}
	}
}

func (w *deriveEventsWriter) compressAsync(path string) {
	w.compressing.Add(1)
	go func() {
		defer w.compressing.Done()
		if err := gzipDeriveEventsFile(path); err != nil {
			log.Warnf("[DERIVE_EVENTS] gzip %s: %v", path, err)
		}
	}()
}

// waitCompress waits for background gzips to finish (tests).
func (w *deriveEventsWriter) waitCompress() { w.compressing.Wait() }

// gzipDeriveEventsFile writes path+".gz" via a temporary file and rename, and
// removes path only once the gzip is complete.
func gzipDeriveEventsFile(path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := path + ".gz.tmp"
	dst, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(dst)
	_, err = io.Copy(zw, src)
	if err = errors.Join(err, zw.Close(), dst.Close()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path+".gz"); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Remove(path)
}

// derivationEventsWriter is the process-wide writer; nil means the log is
// disabled. It is set once by InitDerivationEvents at startup, before any
// encounter is decoded, and not written afterwards.
var derivationEventsWriter *deriveEventsWriter

var deriveEventsDrops util.DropReporter

// InitDerivationEvents starts the derivation events log if
// config.Config.DerivationEvents.Enabled. It is a no-op when disabled; on
// error it returns it (the caller logs) and leaves the log disabled — a bad events dir must not
// stop Golbat.
func InitDerivationEvents() error {
	cfg := config.Config.DerivationEvents
	if !cfg.Enabled {
		return nil
	}
	w, err := newDeriveEventsWriter(cfg.Dir, time.Duration(cfg.BucketMinutes)*time.Minute,
		cfg.RetainDays, time.Now)
	if err != nil {
		return err
	}
	w.start()
	derivationEventsWriter = w
	log.Infof("[DERIVE_EVENTS] writing to %s", cfg.Dir)
	return nil
}

// FlushDerivationEvents stops accepting events, waits for the writer to
// drain its queue, write the open bucket and close its file. It is a no-op
// when the log is disabled, and safe to call twice.
func FlushDerivationEvents() {
	if w := derivationEventsWriter; w != nil {
		w.shutdown()
	}
}

// submitDerivationEvent hands ev to the writer without ever blocking the
// decode path. It is a no-op when the log is disabled or shutting down; a
// full queue drops the event, reported at most once a second.
func submitDerivationEvent(ev deriveEventCheck) {
	if w := derivationEventsWriter; w != nil {
		w.submit(ev)
	}
}

// start runs the writer's loop on its own goroutine.
func (w *deriveEventsWriter) start() {
	w.done = make(chan struct{})
	go func() {
		defer close(w.done)
		w.run()
	}()
}

// submit queues ev without blocking; see submitDerivationEvent.
func (w *deriveEventsWriter) submit(ev deriveEventCheck) {
	w.submitMu.RLock()
	defer w.submitMu.RUnlock()
	if w.closed {
		return
	}
	select {
	case w.events <- ev:
	default:
		deriveEventsDrops.Report(func(dropped int64) {
			log.Warnf("[DERIVE_EVENTS] dropped %d events (queue full)", dropped)
		})
	}
}

// shutdown refuses further submits, closes the channel (making run drain
// and close the writer) and waits for run to finish. Idempotent.
func (w *deriveEventsWriter) shutdown() {
	w.submitMu.Lock()
	if !w.closed {
		w.closed = true
		close(w.events)
	}
	w.submitMu.Unlock()
	if w.done != nil {
		<-w.done
	}
}
