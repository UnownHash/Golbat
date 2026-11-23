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

// The derivation modifier learner's runtime (design §6–7): one goroutine
// owns the modifierLearner, fed by a channel from the encounter check and,
// while a scope has an active modifier, its first sightings. It logs every
// transition and republishes derivationModifierIndex and the API snapshot
// after a transition or a shrink of a confirmed modifier's coverage
// (zone-wide lost, a cell excluded or no longer covered, size boundaries
// moved) at once, and after growth (a venue cell covered, zone-wide gained)
// or an evidence-only change at most once per
// deriveModifiersEvidenceInterval. A publish re-clones only the modifiers
// changed since the last one. State is in memory only.

// deriveModifiersQueueSize is the learner's channel capacity: 16384
// pointer-free events, ~1 MB, never scanned by the GC.
const deriveModifiersQueueSize = 16384

// deriveModifiersSweepInterval is how often the worker ends unseen
// modifiers and prunes idle tracks when no event arrives.
const deriveModifiersSweepInterval = time.Minute

// deriveModifiersEvidenceInterval is the least time between two
// republishes for growth and evidence-only changes, which can come with
// every event.
const deriveModifiersEvidenceInterval = time.Second

// modifierRuntime is the learner goroutine's state. Everything but the
// channel and the submit guard is owned by run (or a test calling handle).
type modifierRuntime struct {
	learner *modifierLearner
	// sweepInterval is run's sweep period (deriveModifiersSweepInterval;
	// tests shorten it before start).
	sweepInterval time.Duration
	// clock times evidence-only republishes (time.Now; tests replace it).
	clock func() time.Time
	// evidencePending: a growth or evidence-only change is not yet
	// published; lastPublish is when the index and snapshot were last
	// rebuilt.
	evidencePending bool
	lastPublish     time.Time
	// published maps each active modifier (the learner's own) to its last
	// published copy and that copy's version: an unchanged modifier's copy
	// is reused. clones counts the copies made (tests).
	published map[*derivationModifier]publishedModifier
	clones    int

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
		sweepInterval: deriveModifiersSweepInterval,
		clock:         time.Now,
		events:        make(chan modifierEvent, queueSize),
		zoneOf:        make(map[s2.CellID]uint16),
		zoneNames:     []string{""},
		zoneIndex:     map[string]uint16{"": 0},
	}
}

// publishedModifier is a published, immutable copy of a modifier as of
// version.
type publishedModifier struct {
	version uint64
	mod     *derivationModifier
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
	p := modifierParams{Start: c.Start, Confirm: c.Confirm,
		Window: time.Duration(c.WindowSeconds) * time.Second, Unseen: time.Duration(c.UnseenSeconds) * time.Second}
	// Confirm is judged on the zone record, which holds modRecordSize checks.
	if p.Start < 1 || p.Confirm < p.Start || p.Confirm > modRecordSize || p.Window <= 0 || p.Unseen <= 0 {
		return p, fmt.Errorf("invalid thresholds start=%d confirm=%d window_seconds=%d unseen_seconds=%d "+
			"(want 1 ≤ start ≤ confirm ≤ %d, the rest > 0)",
			c.Start, c.Confirm, c.WindowSeconds, c.UnseenSeconds, modRecordSize)
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
	log.Infof("[DERIVE_MODIFIER] learner started (start=%d confirm=%d window=%s unseen=%s)",
		p.Start, p.Confirm, p.Window, p.Unseen)
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
//
// ev.Unix is overwritten with the server's clock: the learner's windows and
// unseen ends run on one clock, and a client or map timestamp (a sighting's
// save time) can run ahead — one future-stamped sighting once ended every
// modifier.
func submitDerivationModifierEvent(ev modifierEvent) {
	if r := derivationModifierRuntime; r != nil {
		ev.Unix = time.Now().Unix()
		r.submit(ev)
	}
}

// submitDerivationModifierSighting sends a new record's first sighting to
// the learner, only when its scope has an active modifier that covers
// something (it feeds that modifier's last-seen time). With none it costs one
// atomic load and sends nothing. The save path calls it only when
// derivationModifierLearnerOn, so a learner that is off costs a branch.
func submitDerivationModifierSighting(p *Pokemon, now int64) {
	if !derivationModifierLearnerOn {
		return
	}
	if !derivationModifierIndex.Load().wantsSighting(int32(p.PokemonId), int32(p.Form.ValueOrZero()),
		p.IsStrong.ValueOrZero()) {
		return
	}
	// now (the sighting's map time) is replaced by the server clock at submit.
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
// shrink, else, for growth or an evidence-only change (now or earlier,
// unpublished), once deriveModifiersEvidenceInterval has passed since the
// last publish. With nothing changed it returns without reading the clock.
func (r *modifierRuntime) publish(trs []modifierTransition) {
	for _, tr := range trs {
		log.Info(formatModifierTransition(tr))
	}
	d := r.learner.takeDirty()
	if len(trs) == 0 && !d.shrink {
		r.evidencePending = r.evidencePending || d.changed
		if !r.evidencePending || r.clock().Sub(r.lastPublish) < deriveModifiersEvidenceInterval {
			return
		}
	}
	r.evidencePending, r.lastPublish = false, r.clock()
	active := r.activeCopies()
	derivationModifierIndex.Store(buildModifierIndex(active))
	if len(active) == 0 {
		derivationModifierSnapshot.Store(nil)
	} else {
		derivationModifierSnapshot.Store(&active)
	}
}

// activeCopies returns published copies of the learner's active modifiers,
// reusing the previous copy of each modifier whose version is unchanged
// (copies are immutable once published; the index and the snapshot share
// them) and forgetting the modifiers no longer active.
func (r *modifierRuntime) activeCopies() []*derivationModifier {
	tracks := r.learner.activeTracks
	out := make([]*derivationModifier, 0, len(tracks))
	next := make(map[*derivationModifier]publishedModifier, len(tracks))
	for _, t := range tracks {
		m := t.mod
		p, ok := r.published[m]
		if !ok || p.version != m.version {
			p = publishedModifier{version: m.version, mod: m.clone()}
			r.clones++
		}
		next[m] = p
		out = append(out, p.mod)
	}
	r.published = next
	return out
}

// formatModifierTransition is the [DERIVE_MODIFIER] log line for tr.
// region is the number of covered venue cells, or zone-wide with the
// excluded and the venue cells it has: "zone-wide less 1 plus 3 cells".
func formatModifierTransition(tr modifierTransition) string {
	m := tr.Mod
	region := strconv.Itoa(len(m.Cells)) + " cells"
	if m.ZoneWide {
		region = "zone-wide"
		if n := len(m.Excluded); n > 0 {
			region += " less " + strconv.Itoa(n)
		}
		if n := len(m.Cells); n > 0 {
			region += " plus " + strconv.Itoa(n) + " cells"
		}
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
