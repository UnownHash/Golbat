package decoder

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/geo/s2"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/tz"
	"golbat/util"
)

// The derivation modifier learner's runtime (design §6): one goroutine owns
// the modifierLearner, fed by a channel from the encounter check and, while
// a scope has an active modifier, its first sightings. It logs every
// transition and republishes derivationModifierIndex and the API snapshot
// after a transition or a change to a confirmed modifier's coverage at
// once, and after an evidence-only change (observations, contradictions,
// a candidate's cells) at most once per deriveModifiersEvidenceInterval.
// State is in memory only.

// deriveModifiersQueueSize is the learner's channel capacity: 16384
// pointer-free events, ~1 MB, never scanned by the GC.
const deriveModifiersQueueSize = 16384

// deriveModifiersSweepInterval is how often the worker ends unseen
// modifiers and prunes idle tracks when no event arrives.
const deriveModifiersSweepInterval = time.Minute

// deriveModifiersEvidenceInterval is the least time between two
// republishes for evidence-only changes, which can come with every event.
const deriveModifiersEvidenceInterval = time.Second

// modifierRuntime is the learner goroutine's state. Everything but the
// channel and the submit guard is owned by run (or a test calling handle).
type modifierRuntime struct {
	learner       *modifierLearner
	zoneWideCells int
	// sweepInterval is run's sweep period (deriveModifiersSweepInterval;
	// tests shorten it before start).
	sweepInterval time.Duration
	// clock times evidence-only republishes (time.Now; tests replace it).
	clock func() time.Time
	// evidencePending: an evidence-only change is not yet published;
	// lastPublish is when the index and snapshot were last rebuilt.
	evidencePending bool
	lastPublish     time.Time

	events chan modifierEvent
	// submitMu orders submit against shutdown, as in deriveEventsWriter.
	submitMu sync.RWMutex
	closed   bool
	done     chan struct{} // closed when run returns; nil if never started

	// zoneOf maps a level-14 cell to its index in zoneNames; zoneNames[0]
	// is "" (unknown). zoneIndex is the reverse, so cells share one name.
	// Never pruned: it is bounded by the level-14 cells scanned (~2 bytes of
	// value per entry).
	zoneOf    map[s2.CellID]uint16
	zoneNames []string
	zoneIndex map[string]uint16
}

func newModifierRuntime(p modifierParams, queueSize int) *modifierRuntime {
	return &modifierRuntime{
		learner:       newModifierLearner(p),
		zoneWideCells: p.ZoneWideCells,
		sweepInterval: deriveModifiersSweepInterval,
		clock:         time.Now,
		events:        make(chan modifierEvent, queueSize),
		zoneOf:        make(map[s2.CellID]uint16),
		zoneNames:     []string{""},
		zoneIndex:     map[string]uint16{"": 0},
	}
}

// derivationModifierLearnerOn reports whether the learner runs. It is set
// once by InitDerivationModifiers at startup, before any encounter is
// decoded, and read by the submit sites.
var derivationModifierLearnerOn bool

// derivationModifierRuntime is the running learner; nil when off. Set
// alongside derivationModifierLearnerOn.
var derivationModifierRuntime *modifierRuntime

// derivationModifierSnapshot holds copies of the active (candidate and
// confirmed) modifiers, refreshed with the index; nil = none.
var derivationModifierSnapshot atomic.Pointer[[]*derivationModifier]

var deriveModifierDrops util.DropReporter

// activeDerivationModifiers returns the active modifiers as of the last
// transition. The modifiers are shared and must not be modified.
func activeDerivationModifiers() []*derivationModifier {
	if p := derivationModifierSnapshot.Load(); p != nil {
		return *p
	}
	return nil
}

// modifierParamsFromConfig converts the config's thresholds, rejecting
// values the learner cannot work with.
func modifierParamsFromConfig() (modifierParams, error) {
	c := config.Config.DerivationModifiers
	p := modifierParams{Start: c.Start, Confirm: c.Confirm, End: c.End, ZoneWideCells: c.ZoneWideCells,
		Window: time.Duration(c.WindowSeconds) * time.Second, Unseen: time.Duration(c.UnseenSeconds) * time.Second}
	if p.Start < 1 || p.Confirm < p.Start || p.End < 1 || p.ZoneWideCells < 1 || p.Window <= 0 || p.Unseen <= 0 {
		return p, fmt.Errorf("invalid thresholds start=%d confirm=%d end=%d zone_wide_cells=%d "+
			"window_seconds=%d unseen_seconds=%d (want start ≥ 1, confirm ≥ start, the rest > 0)",
			c.Start, c.Confirm, c.End, c.ZoneWideCells, c.WindowSeconds, c.UnseenSeconds)
	}
	return p, nil
}

// InitDerivationModifiers starts the learner when
// config.Config.DerivationModifiers.Enabled and DeriveIv are both on. With
// DeriveIv off it warns and stays off; on invalid thresholds it returns the
// error (the caller logs) and stays off.
func InitDerivationModifiers() error {
	if !config.Config.DerivationModifiers.Enabled {
		return nil
	}
	if !config.Config.DeriveIv {
		log.Warnf("[DERIVE_MODIFIER] derivation_modifiers needs derive_iv; learner not started")
		return nil
	}
	p, err := modifierParamsFromConfig()
	if err != nil {
		return err
	}
	r := newModifierRuntime(p, deriveModifiersQueueSize)
	r.start()
	derivationModifierRuntime = r
	derivationModifierLearnerOn = true
	log.Infof("[DERIVE_MODIFIER] learner started (start=%d confirm=%d end=%d zone_wide_cells=%d window=%s unseen=%s)",
		p.Start, p.Confirm, p.End, p.ZoneWideCells, p.Window, p.Unseen)
	return nil
}

// FlushDerivationModifiers stops accepting events and waits for the worker
// to drain its queue. A no-op when the learner is off; safe to call twice.
func FlushDerivationModifiers() {
	if r := derivationModifierRuntime; r != nil {
		r.shutdown()
	}
}

// submitDerivationModifierEvent hands ev to the learner without ever
// blocking the decode path. A no-op when the learner is off or shutting
// down; a full queue drops the event, reported at most once a second.
func submitDerivationModifierEvent(ev modifierEvent) {
	if r := derivationModifierRuntime; r != nil {
		r.submit(ev)
	}
}

// submitDerivationModifierSighting sends a new record's first sighting to
// the learner, only when its scope has an active modifier (it feeds that
// modifier's last-seen time). With no active modifier it costs one atomic
// load and sends nothing. The save path calls it only when
// derivationModifierLearnerOn, so a learner that is off costs a branch.
func submitDerivationModifierSighting(p *Pokemon, now int64) {
	if !derivationModifierLearnerOn {
		return
	}
	if !derivationModifierIndex.Load().wantsSighting(int32(p.PokemonId), int32(p.Form.ValueOrZero()),
		p.IsStrong.ValueOrZero()) {
		return
	}
	submitDerivationModifierEvent(newModifierEvent(newDeriveEventSighting(p, time.Unix(now, 0))))
}

// QueueLen reports the learner's backlog for metrics.
func (r *modifierRuntime) QueueLen() int { return len(r.events) }

func (r *modifierRuntime) start() {
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		r.run()
	}()
}

// run feeds events until the channel is closed, sweeping every
// sweepInterval.
func (r *modifierRuntime) run() {
	ticker := time.NewTicker(r.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case ev, ok := <-r.events:
			if !ok {
				return
			}
			r.handle(ev)
		case now := <-ticker.C:
			r.publish(r.learner.sweep(now))
		}
	}
}

// handle feeds one event in its cell's zone.
func (r *modifierRuntime) handle(ev modifierEvent) {
	r.publish(r.learner.feed(ev, r.zone(ev.Cell)))
}

// zone is the IANA timezone at cell's centre, "" for cell 0 or an unknown
// zone, cached per cell.
func (r *modifierRuntime) zone(cell s2.CellID) string {
	if cell == 0 {
		return ""
	}
	if i, ok := r.zoneOf[cell]; ok {
		return r.zoneNames[i]
	}
	centre := s2.LatLngFromPoint(cell.Point())
	name := tz.SearchTimezone(centre.Lat.Degrees(), centre.Lng.Degrees())
	i, ok := r.zoneIndex[name]
	if !ok {
		i = uint16(len(r.zoneNames)) // ~600 IANA zones: never overflows
		r.zoneNames = append(r.zoneNames, name)
		r.zoneIndex[name] = i
	}
	r.zoneOf[cell] = i
	return name
}

// publish logs trs and republishes the index and the snapshot from the
// learner's active modifiers: at once after a transition or a coverage
// change, else, for an evidence-only change (now or earlier, unpublished),
// once deriveModifiersEvidenceInterval has passed since the last publish.
// With nothing changed it returns without reading the clock.
func (r *modifierRuntime) publish(trs []modifierTransition) {
	for _, tr := range trs {
		log.Info(formatModifierTransition(tr, r.zoneWideCells))
	}
	dirty, regionChanged := r.learner.takeDirty()
	if len(trs) == 0 && !regionChanged {
		r.evidencePending = r.evidencePending || dirty
		if !r.evidencePending || r.clock().Sub(r.lastPublish) < deriveModifiersEvidenceInterval {
			return
		}
	}
	r.evidencePending, r.lastPublish = false, r.clock()
	active := r.learner.active()
	derivationModifierIndex.Store(buildModifierIndex(active, r.zoneWideCells))
	if len(active) == 0 {
		derivationModifierSnapshot.Store(nil)
	} else {
		derivationModifierSnapshot.Store(&active)
	}
}

// formatModifierTransition is the [DERIVE_MODIFIER] log line for tr.
func formatModifierTransition(tr modifierTransition, zoneWideCells int) string {
	m := tr.Mod
	region := "zone-wide"
	if !m.ZoneWide(zoneWideCells) {
		region = strconv.Itoa(len(m.InCells)) + " cells"
	}
	line := fmt.Sprintf("[DERIVE_MODIFIER] %s %s %s scope=%s zone=%s region=%s obs=%d contra=%d",
		tr.State, m.Type, formatModifierEffect(m), formatModifierScope(m.Scope), m.Zone, region,
		m.Observations, m.Contradictions)
	if tr.State == modEnded {
		line += " reason=" + m.EndReason
	}
	return line
}

func formatModifierScope(s modifierScope) string {
	if s.AllStrong {
		return "all-strong"
	}
	out := strconv.Itoa(int(s.PokemonID)) + "/" + strconv.Itoa(int(s.Form))
	if s.Strong {
		out += "/strong"
	}
	return out
}

// formatModifierEffect renders a modifier's payload: delta=+N, <slot>=<move>,
// cuts=<boundary>:<lo>..<hi>,... (an unbounded end left empty), or "-".
func formatModifierEffect(m *derivationModifier) string {
	switch m.Type {
	case modLevelOffset:
		return fmt.Sprintf("delta=%+d", m.Effect.Delta)
	case modMovePin:
		return m.Effect.Slot + "=" + strconv.Itoa(int(m.Effect.Move))
	case modSizeCutoffs:
		keys := make([]string, 0, len(m.Boundaries))
		for k := range m.Boundaries {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			b := m.Boundaries[k]
			var lo, hi string
			if b.HasLo {
				lo = strconv.FormatFloat(b.Lo, 'f', 4, 64)
			}
			if b.HasHi {
				hi = strconv.FormatFloat(b.Hi, 'f', 4, 64)
			}
			parts[i] = k + ":" + lo + ".." + hi
		}
		return "cuts=" + strings.Join(parts, ",")
	}
	return "-"
}

// submit queues ev without blocking; see submitDerivationModifierEvent.
func (r *modifierRuntime) submit(ev modifierEvent) {
	r.submitMu.RLock()
	defer r.submitMu.RUnlock()
	if r.closed {
		return
	}
	select {
	case r.events <- ev:
	default:
		deriveModifierDrops.Report(func(dropped int64) {
			log.Warnf("[DERIVE_MODIFIER] dropped %d events (queue full)", dropped)
		})
	}
}

// shutdown refuses further submits, closes the channel (run drains it and
// returns) and waits for run. Idempotent.
func (r *modifierRuntime) shutdown() {
	r.submitMu.Lock()
	if !r.closed {
		r.closed = true
		close(r.events)
	}
	r.submitMu.Unlock()
	if r.done != nil {
		<-r.done
	}
}
