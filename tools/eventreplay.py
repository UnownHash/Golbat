#!/usr/bin/env python3
"""Replay golbat's derivation events log and propose event modifiers.

    python3 tools/eventreplay.py logs/derive-events-2026-11-14.jsonl.gz logs/derive-events-2026-11-15.jsonl
    python3 tools/eventreplay.py logs/derive-events-*.jsonl* --emit-modifiers mods.json
    python3 tools/eventreplay.py logs/derive-events-*.jsonl* --at 2026-11-15T12:00:00Z --emit-modifiers mods.json
    python3 tools/eventreplay.py logs/derive-events-*.jsonl* --simulate-sampling pct=5,min_per_hour=1
    python3 tools/eventreplay.py logs/derive-events-*.jsonl* --start 3 --confirm 10 --end 20 --window 1800

Inputs are the files golbat writes with derivation events logging on
(dir/derive-events-YYYY-MM-DD.jsonl[.gz], dir from [derivation_events]): JSON
lines, one file per UTC day, older days gzipped. Plain and gzip files can be
mixed (gzip is recognised by its magic bytes, not the name), in any order.
Two record kinds are read; anything else is skipped:

- `deviation`: one encounter whose derived IVs, level, size or moves
  disagreed with the game's (a move outside its pool included). Ordered by
  `ts`, which the writer places inside the bucket the check was counted in:
  a delayed check counted in a later bucket gets that bucket's start, one
  stamped ahead of the writer's clock gets the clock. `event_ts` is the
  check's own time. Files from before `event_ts` carry the check's own time
  in `ts`; the reader clamps a row stamped before the bucket it was written
  in to that bucket's start, so replay never steps back across a summary.
- `summary`: per bucket (15 min by default) and (pokemon_id, form, strong,
  boosted, utc_offset), how many checks agreed on each dimension, plus the
  raw size-roll range per observed size class. Every check counts, the
  deviations' too, except that size_rolls come only from checks whose IVs
  agreed (when the walk did not match, its roll is noise). Ordered by bucket end (`bucket` + `bucket_seconds`): a
  summary means "this bucket is complete", and one for a bucket straddling
  UTC midnight can sit in the next day's file.

Files are merged by that order key (each file is near-ordered on its own;
small disorder passes through rather than failing). A gzip stream cut short
or an undecodable line, typically a half-written last line of today's file,
gets one warning per file on stderr and the readable records still count.

The replay is streaming: at any point the detector knows only what it has
read. It proposes four kinds of modifier, each within a scope of either
every strong Pokemon ({"strong": true}) or one species and form
({"strong": s, "pokemon_id": p, "form": f}); boosted and utc_offset are not
part of a scope.

- level_offset: level disagrees while IVs agree, keyed by the delta
  observed - derived. Tracked per species; strong deviations are also
  tracked at the all-strong scope, which needs two species among its recent
  deviations before it becomes a candidate.
- move_pin: the quick or charged move disagrees (IVs agreeing), keyed by
  slot and observed move. The same slot observed as a different move is a
  contradiction.
- size_cutoffs: size disagrees (IVs agreeing), per species. The effect is
  each size boundary's interval [max roll seen below it, min roll seen above
  it] on the raw roll, from the deviations plus summary size_rolls since the
  effect started, listing only boundaries whose interval excludes the base
  cut (0.004/0.029/0.971/0.996; strong: 0.8 between XL and XXL). A
  deviation that would empty an interval is a contradiction, and summary
  rolls that would are left out. null is an unbounded end.
- unexplained: IVs disagree, i.e. the walk itself did not match, so the
  other dimensions say nothing. Per species, no effect payload.

Lifecycle, with the thresholds as Params:

- candidate: `start` matching deviations within `window` seconds. For
  level_offset, move_pin and unexplained there must also be no agreement on
  the dimension in scope since the first of them; a summary bucket counts
  when it starts at or after that deviation. size_cutoffs skips that test
  since a moved cut leaves most rolls agreeing.
- confirmed: `confirm` matching deviations with no contradiction (at once,
  with the candidate, when `start` >= `confirm`).
- ended: after the last matching deviation, summaries for the scope whose
  bucket starts after it add up to `end` agreements on the dimension with
  no matching deviation in between. The transition time is that summary's
  bucket end. A later matching deviation starts a new candidate cycle.
- size_cutoffs ends differently: a moved cut leaves most rolls agreeing, so
  agreement counts would end it during every quiet spell. It ends only on
  contrary evidence: a summary (bucket starting after the last matching
  deviation) with a roll in the disputed region of a listed boundary that is
  classified by the base cut. For a cut moved up ([lo, hi] with lo >= base
  cut), a class above the boundary with its min roll in [base cut, lo); for
  a cut moved down (hi < base cut), a class at or below it with its max roll
  in [hi, base cut). One such roll suffices: the derivation is
  deterministic, so the base cut must be back.
- Every type also ends when its scope goes unseen: nothing of the scope (a
  deviation row, or a summary with an agreement on the dimension or a first
  sighting, "seen") for `unseen` seconds. Sightings need no encounter, so
  with them the rule works at any encounter rate; files from before the
  writer counted sightings have only checks, which a sampling scanner
  leaves hours apart. The transition time is when the scope was last seen,
  so it prints late and out of time order. Event-only forms (strong
  Pokemon that spawn only during the event) vanish when it ends and never
  produce the agreements the rules above wait for. A sparsely seen scope
  can also end this way mid-event; a later deviation starts a new cycle.
  Ended lines carry by=agreement, by=contrary or by=unseen.

Outputs:

- By default, the timeline: one line per transition as it happens, with the
  UTC time, the local time at the record that caused it (its utc_offset),
  the state, type, effect, scope and the evidence counts at that moment:
    2026-11-15T10:03:12Z (local 10:03 +00:00) CANDIDATE move_pin charged=494 scope=strong 959/3272 obs=3 contra=0
  A scope prints as "strong 959/3272", "959/3272" (not strong) or
  "all-strong".
- --emit-modifiers PATH writes the active (candidate and confirmed)
  modifiers as shareable JSON (version 1; design record §6), as of the end
  of the run, or of --at, which stops the replay at that time. Scope is
  {"strong": s} or {"strong": s, "pokemon": [{"id": p, "form": f}]}; the
  window is UTC (time_basis "utc") from the cycle's first matching
  deviation, end null; generated_at is the replay's time (--at, else the
  last record's). The id is <start date>-<type>-<scope>-<effect>, e.g.
  2026-11-15-move_pin-strong-959-3272-charged-494; size_cutoffs and
  unexplained have no effect part (a size effect's boundaries grow while
  it runs, and its id must stay the same).
- --simulate-sampling pct=P,min_per_hour=K[,seed=S][,strong_pct=Q]
  [,key=tz|global] replays the files twice, in full and thinned to what a
  scanner sampling encounters at P% (Q% for strong pokemon) with a minimum
  of K per key per hour would have logged, and prints per effect (cycle of
  the full run) how much later the thinned run saw its start, confirmation
  and end: seconds, "missed" when it never did, "-" when the full run did
  not either. key=tz (default) keys the minimum by (pokemon_id, form,
  strong, utc_offset) per local hour; key=global by (pokemon_id, form,
  strong) per UTC hour. A last line gives the encounters the rule keeps, of
  those the files hold, overall and for strong pokemon. A scanner keying
  its minimum per area would keep more; the files carry no area.

Thinning is an approximation from the logged counts, not a resample of
encounters. Per key per hour, the first
min_per_hour deviation rows are kept and later ones with probability
pct/100 (strong_pct/100 for strong). A summary's checks share that hourly minimum: with `remaining` the
minimum left at its bucket's start (deviations of the bucket count as its
first checks), each dimension's n and `checked` become
min(n, remaining) + Binomial(n - min(n, remaining), pct/100), and ok is the
number of agreements among the kept checks, drawn hypergeometrically. The
summary's deviations kept by the minimum are among the kept checks as
themselves: where one disagrees on a dimension, that kept check is a
disagreement, and ok is drawn over the rest.
seen (sightings) is kept as it is: sighting needs no encounter.
size_rolls are kept as they are, so size_cutoffs end detection (contrary
rolls) is not thinned and its end delays read optimistic. The draws use
Python's random seeded with `seed`, so a run is reproducible.
"""
import argparse
import gzip
import heapq
import json
import random
import sys
import zlib
from collections import Counter, deque
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Iterator

GZIP_MAGIC = b"\x1f\x8b"

# The cut on the raw size roll between class lo and lo+1 ("lo/hi") outside
# events; classes 1-5 are XXS, XS, M, XL, XXL. Strong Pokemon are XL or XXL
# only, split at 0.8. See derive/encounter classifySize and strongSize.
BASE_CUTS = {"1/2": 0.004, "2/3": 0.029, "3/4": 0.971, "4/5": 0.996}
STRONG_CUTS = {"4/5": 0.8}

ACTIVE = ("candidate", "confirmed")


# --- reader -----------------------------------------------------------------

def order_key(rec: dict) -> tuple:
    """When a record happened, for merging: a deviation at `ts`, a summary
    at its bucket's end. A summary sorts before a deviation at the same
    second, which belongs to the next bucket."""
    if rec.get("kind") == "summary":
        return (rec["bucket"] + rec["bucket_seconds"], 0)
    return (rec["ts"], 1)


DEFAULT_BUCKET_SECONDS = 900


def _bucket_ordered(records: Iterator[dict]) -> Iterator[dict]:
    """Sorts a file's records by order_key within each bucket. The writer
    puts a bucket's summary at its end, but a shutdown writes the open
    bucket's summary and a restart inside the same bucket appends earlier
    deviations after it, so file order is not time order there. Records are
    held until one from a later bucket appears, then the held bucket is
    sorted and emitted: memory is one bucket's records. The bucket length
    comes from the summaries seen (default 15 minutes before the first).

    A deviation stamped before the held bucket (a delayed check, which the
    writer counted in the open bucket) is moved to that bucket's start, with
    its own time kept in `event_ts`. The current writer already does this;
    files written before it did not, and their delayed rows would otherwise
    sort before a summary already emitted."""
    bs = DEFAULT_BUCKET_SECONDS
    held: list[dict] = []
    cur = None
    for rec in records:
        if rec["kind"] == "summary":
            bs = rec["bucket_seconds"] or bs
            b = rec["bucket"]
        else:
            b = rec["ts"] // bs * bs
            if cur is not None and b < cur:
                rec.setdefault("event_ts", rec["ts"])
                rec["ts"] = b = cur
        if cur is not None and b > cur:
            held.sort(key=order_key)
            yield from held
            held = []
        if cur is None or b > cur:
            cur = b
        held.append(rec)
    held.sort(key=order_key)
    yield from held


def _file_records(path: str) -> Iterator[dict]:
    """The usable records of one file in file order. A truncated gzip
    stream or an undecodable line warns once for the file."""
    warned = False

    def warn(what):
        nonlocal warned
        if not warned:
            warned = True
            print(f"eventreplay: {path}: {what}; using the readable records",
                  file=sys.stderr)

    with open(path, "rb") as raw:
        gz = raw.read(2) == GZIP_MAGIC
    f = gzip.open(path, "rb") if gz else open(path, "rb")
    with f:
        try:
            for line in f:
                if not line.strip():
                    continue
                try:
                    rec = json.loads(line)
                except ValueError:  # JSONDecodeError, UnicodeDecodeError
                    warn("undecodable line (half-written?)")
                    continue
                if not isinstance(rec, dict):
                    continue
                kind = rec.get("kind")
                if kind == "deviation" and "ts" in rec:
                    yield rec
                elif (kind == "summary" and "bucket" in rec
                      and "bucket_seconds" in rec):
                    yield rec
        except (EOFError, OSError, zlib.error) as e:  # truncated/corrupt gzip
            warn(f"stream ends early ({e})")


def read_records(paths: list[str]) -> Iterator[dict]:
    """Records from every file, merged by order_key (each file first
    ordered within its buckets)."""
    return heapq.merge(*(_bucket_ordered(_file_records(p)) for p in paths),
                       key=order_key)


# --- detector ---------------------------------------------------------------

@dataclass
class Params:
    start: int = 3      # matching deviations within window → candidate
    confirm: int = 10   # matching deviations, no contradiction → confirmed
    end: int = 20       # agreements after the last deviation → ended
    window: int = 1800  # seconds the `start` deviations must fall within
    unseen: int = 3600  # seconds with no check of the scope → ended


@dataclass
class Modifier:
    type: str           # level_offset | move_pin | size_cutoffs | unexplained
    scope: dict
    effect: dict
    status: str         # candidate | confirmed | ended
    start: int | None = None  # first matching deviation of this cycle
    end: int | None = None    # the ended transition's time
    end_reason: str | None = None  # agreement | contrary | unseen
    observations: int = 0
    contradictions: int = 0


@dataclass
class Transition:
    ts: int
    utc_offset: int     # minutes, from the record that caused it
    state: str          # candidate | confirmed | ended
    modifier: Modifier


@dataclass
class _Track:
    """One (type, scope, effect key): its recent deviations while not
    active, else the active modifier and the agreements seen since its last
    matching deviation."""
    type: str
    scope: dict
    dim: str            # the summary/agree dimension it watches
    effect: dict
    gate: bool          # agreements since the first pending deviation reset it
    min_species: int = 1
    pending: deque = field(default_factory=deque)  # (ts, species, roll, class)
    species: Counter = field(default_factory=Counter)  # over pending
    mod: Modifier | None = None
    last_match: int = 0
    last_seen: int = 0  # latest check of the scope while active
    last_off: int = 0   # utc_offset of the record that set last_seen
    end_ok: int = 0
    rolls: dict = field(default_factory=dict)  # size class -> [min, max]


def _clear_pending(t):
    t.pending.clear()
    t.species.clear()


def _species(rec):
    return (rec["pokemon_id"], rec["form"])


def _species_scope(rec):
    return {"strong": bool(rec["strong"]), "pokemon_id": rec["pokemon_id"],
            "form": rec["form"]}


def _scope_key(scope):
    return tuple(sorted(scope.items()))


def _boundaries(rolls, cuts):
    """Per boundary "lo/hi": [max roll of a class <= lo, min roll of a class
    >= hi], either None when unseen."""
    out = {}
    for name in cuts:
        lo = int(name.split("/")[0])
        below = [r[1] for c, r in rolls.items() if c <= lo]
        above = [r[0] for c, r in rolls.items() if c > lo]
        out[name] = [max(below) if below else None,
                     min(above) if above else None]
    return out


def _size_effect(rolls, strong):
    cuts = STRONG_CUTS if strong else BASE_CUTS
    listed = {}
    for name, (lo, hi) in _boundaries(rolls, cuts).items():
        cut = cuts[name]
        if (lo is not None and lo >= cut) or (hi is not None and hi < cut):
            listed[name] = [lo, hi]
    return {"type": "size_cutoffs", "boundaries": listed}


def _add_roll(rolls, cls, lo, hi):
    r = rolls.get(cls)
    if r is None:
        rolls[cls] = [lo, hi]
    else:
        r[0], r[1] = min(r[0], lo), max(r[1], hi)


def _empty_intervals(rolls, strong):
    cuts = STRONG_CUTS if strong else BASE_CUTS
    return sum(1 for lo, hi in _boundaries(rolls, cuts).values()
               if lo is not None and hi is not None and lo >= hi)


class Detector:
    """Streams records (in order_key order) and reports modifier
    transitions. See the module docstring for the rules."""

    def __init__(self, params: Params):
        self.p = params
        self._tracks: dict = {}    # (type, scope key, effect key) -> _Track
        self._by_scope: dict = {}  # scope key -> [_Track]
        self._mods: list[Modifier] = []
        self._active: list = []    # tracks with a modifier

    def active(self) -> list[Modifier]:
        return [m for m in self._mods if m.status in ACTIVE]

    def all(self) -> list[Modifier]:
        return list(self._mods)

    def feed(self, rec: dict) -> list[Transition]:
        kind = rec.get("kind")
        if kind not in ("deviation", "summary"):
            return []
        out = self._sweep_unseen(order_key(rec)[0])
        if kind == "deviation":
            return out + self._deviation(rec)
        return out + self._summary(rec)

    def _sweep_unseen(self, now):
        """End the active modifiers whose scope has had no check for
        `unseen` seconds, at the time it was last seen."""
        out = []
        for t in [t for t in self._active
                  if now - t.last_seen > self.p.unseen]:
            out.append(self._end(t, t.last_seen, t.last_off, "unseen"))
        return out

    def _seen(self, rec, ts, off):
        """Note a check of rec's scopes at ts for their active tracks."""
        scopes = [_species_scope(rec)]
        if rec["strong"]:
            scopes.append({"strong": True})
        for sc in scopes:
            for t in self._by_scope.get(_scope_key(sc), ()):
                if t.mod is not None and ts > t.last_seen:
                    t.last_seen, t.last_off = ts, off

    # tracks

    def _track(self, type_, scope, effect, dim, gate, min_species=1):
        sk = _scope_key(scope)
        key = (type_, sk, json.dumps(effect, sort_keys=True))
        t = self._tracks.get(key)
        if t is None:
            t = _Track(type_, scope, dim, effect, gate, min_species)
            self._tracks[key] = t
            self._by_scope.setdefault(sk, []).append(t)
        return t

    def _scope_tracks(self, type_, scope, dim):
        return [t for t in self._by_scope.get(_scope_key(scope), ())
                if t.type == type_ and t.dim == dim]

    # deviations

    def _deviation(self, rec):
        agree = rec.get("agree") or {}
        ts, off = rec["ts"], rec.get("utc_offset", 0)
        scope = _species_scope(rec)
        out = []
        self._seen(rec, ts, off)

        if agree.get("ivs") == "disagree":
            t = self._track("unexplained", scope, {"type": "unexplained"},
                            "ivs", gate=True)
            out += self._match(t, rec, ts, off)
            return out
        if agree.get("ivs") != "agree":
            return out

        if agree.get("level") == "disagree":
            delta = rec["observed"]["level"] - rec["derived"]["level"]
            effect = {"type": "level_offset", "delta": delta}
            scopes = [(scope, 1)]
            if rec["strong"]:
                scopes.append(({"strong": True}, 2))
            for sc, min_species in scopes:
                t = self._track("level_offset", sc, effect, "level",
                                gate=True, min_species=min_species)
                self._contradict_others(t)
                out += self._match(t, rec, ts, off)

        for slot, n in (("quick", 1), ("charged", 2)):
            if agree.get(slot) != "disagree":
                continue
            effect = {"type": "move_pin", "slot": slot,
                      "move": rec[f"observed_move_{n}"]}
            t = self._track("move_pin", scope, effect, slot, gate=True)
            self._contradict_others(t)
            out += self._match(t, rec, ts, off)

        if agree.get("size") == "disagree":
            t = self._track("size_cutoffs", scope,
                            {"type": "size_cutoffs"}, "size", gate=False)
            out += self._size_match(t, rec, ts, off)
        return out

    def _contradict_others(self, t):
        """A deviation for t contradicts every other active track of the
        same type, scope and dimension (another delta, another move)."""
        for o in self._scope_tracks(t.type, t.scope, t.dim):
            if o is not t and o.mod is not None:
                o.mod.contradictions += 1

    def _match(self, t, rec, ts, off, roll=None):
        """Count one matching deviation for t; returns its transitions."""
        p = self.p
        if t.mod is None:
            sp = _species(rec)
            t.pending.append((ts, sp, roll,
                              rec.get("observed", {}).get("size")))
            t.species[sp] += 1
            while t.pending and t.pending[0][0] < ts - p.window:
                old = t.pending.popleft()[1]
                t.species[old] -= 1
                if not t.species[old]:
                    del t.species[old]
            if (len(t.pending) < p.start
                    or len(t.species) < t.min_species):
                return []
            mod = Modifier(type=t.type, scope=dict(t.scope),
                           effect=dict(t.effect), status="candidate",
                           start=t.pending[0][0],
                           observations=len(t.pending))
            if t.type == "size_cutoffs":
                t.rolls = {}
                for _, _, r, cls in t.pending:
                    _add_roll(t.rolls, cls, r, r)
                mod.effect = _size_effect(t.rolls, t.scope["strong"])
            _clear_pending(t)
            t.mod, t.last_match, t.end_ok = mod, ts, 0
            t.last_seen, t.last_off = ts, off
            self._mods.append(mod)
            self._active.append(t)
            out = [Transition(ts, off, "candidate", mod)]
            if mod.observations >= p.confirm:
                mod.status = "confirmed"
                out.append(Transition(ts, off, "confirmed", mod))
            return out

        mod = t.mod
        mod.observations += 1
        t.last_match, t.end_ok = ts, 0
        if (mod.status == "candidate" and mod.observations >= p.confirm
                and mod.contradictions == 0):
            mod.status = "confirmed"
            return [Transition(ts, off, "confirmed", mod)]
        return []

    def _size_match(self, t, rec, ts, off):
        roll, cls = rec["size_roll"], rec["observed"]["size"]
        if t.mod is not None:
            trial = {c: list(r) for c, r in t.rolls.items()}
            _add_roll(trial, cls, roll, roll)
            strong = t.scope["strong"]
            if _empty_intervals(trial, strong) > _empty_intervals(t.rolls,
                                                                  strong):
                t.mod.contradictions += 1
                return []
            t.rolls = trial
            t.mod.effect = _size_effect(t.rolls, strong)
        return self._match(t, rec, ts, off, roll=roll)

    # summaries

    def _summary(self, rec):
        bucket = rec["bucket"]
        bucket_end = bucket + rec["bucket_seconds"]
        off = rec.get("utc_offset", 0)
        scopes = [_species_scope(rec)]
        if rec["strong"]:
            scopes.append({"strong": True})
        out = []
        for sc in scopes:
            for t in self._by_scope.get(_scope_key(sc), ()):
                ok = (rec.get(t.dim) or {}).get("ok", 0)
                seen = ok > 0 or rec.get("seen", 0) > 0
                if t.mod is not None and seen and bucket_end > t.last_seen:
                    # Agreements and sightings leave no row of their own:
                    # seen at some point in the bucket, so at its end.
                    t.last_seen, t.last_off = bucket_end, off
                if t.mod is None:
                    if (t.gate and ok > 0 and t.pending
                            and bucket >= t.pending[0][0]):
                        _clear_pending(t)
                    continue
                if t.type == "size_cutoffs":
                    rolls = _parse_rolls(rec.get("size_rolls"))
                    if (bucket > t.last_match
                            and _base_classified(t, rolls)):
                        out.append(self._end(t, bucket_end, off, "contrary"))
                    elif bucket >= t.mod.start:
                        self._summary_rolls(t, rolls)
                    continue
                if bucket <= t.last_match:
                    continue
                t.end_ok += ok
                if t.end_ok >= self.p.end:
                    out.append(self._end(t, bucket_end, off, "agreement"))
        return out

    def _end(self, t, ts, off, reason):
        mod = t.mod
        mod.status, mod.end, mod.end_reason = "ended", ts, reason
        t.mod, t.end_ok = None, 0
        self._active.remove(t)
        return Transition(ts, off, "ended", mod)

    @staticmethod
    def _summary_rolls(t, rolls):
        """Merge a summary's rolls into t's evidence, leaving out a class
        whose range would empty an interval."""
        strong = t.scope["strong"]
        for cls, (lo, hi) in rolls.items():
            trial = {c: list(r) for c, r in t.rolls.items()}
            _add_roll(trial, cls, lo, hi)
            if _empty_intervals(trial, strong) <= _empty_intervals(t.rolls,
                                                                   strong):
                t.rolls = trial
        t.mod.effect = _size_effect(t.rolls, strong)


def _parse_rolls(size_rolls):
    """A summary's size_rolls as {class: (min, max)}, bad entries out."""
    out = {}
    for cls, r in (size_rolls or {}).items():
        try:
            out[int(cls)] = (float(r[0]), float(r[1]))
        except (TypeError, ValueError, IndexError):
            continue
    return out


def _base_classified(t, rolls):
    """Whether rolls hold one in a listed boundary's disputed region that
    the base cut classifies, i.e. the moved cut is gone."""
    cuts = STRONG_CUTS if t.scope["strong"] else BASE_CUTS
    for name, (lo, hi) in t.mod.effect["boundaries"].items():
        cut, below = cuts[name], int(name.split("/")[0])
        for cls, (rmin, rmax) in rolls.items():
            if (cls > below and lo is not None and lo >= cut
                    and cut <= rmin < lo):
                return True  # cut moved up, a roll under it seen above
            if (cls <= below and hi is not None and hi < cut
                    and hi <= rmax < cut):
                return True  # cut moved down, a roll over it seen below
    return False


# --- timeline and modifier JSON ---------------------------------------------

def _iso(ts: int) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).strftime(
        "%Y-%m-%dT%H:%M:%SZ")


def _num(v) -> str:
    return "-" if v is None else f"{v:g}"


def _scope_text(scope: dict) -> str:
    if "pokemon_id" not in scope:
        return "all-strong" if scope["strong"] else "all"
    sp = f"{scope['pokemon_id']}/{scope['form']}"
    return f"strong {sp}" if scope["strong"] else sp


def _effect_text(effect: dict) -> str:
    """The effect's payload, "" for unexplained."""
    t = effect["type"]
    if t == "move_pin":
        return f"{effect['slot']}={effect['move']}"
    if t == "level_offset":
        return f"delta={effect['delta']:+d}"
    if t == "size_cutoffs":
        b = effect.get("boundaries") or {}
        return " ".join(f"{name}=[{_num(lo)},{_num(hi)}]"
                        for name, (lo, hi) in sorted(b.items())
                        ) or "boundaries=none"
    return ""


def format_transition(t: Transition) -> str:
    """One timeline line; see the module docstring."""
    m = t.modifier
    off = t.utc_offset
    local = datetime.fromtimestamp(t.ts, timezone.utc) + timedelta(minutes=off)
    sign = "-" if off < 0 else "+"
    parts = [_iso(t.ts),
             f"(local {local:%H:%M} {sign}{abs(off) // 60:02d}:"
             f"{abs(off) % 60:02d})",
             t.state.upper(), m.type, _effect_text(m.effect),
             f"scope={_scope_text(m.scope)}",
             f"obs={m.observations} contra={m.contradictions}"]
    if t.state == "ended" and m.end_reason:
        parts.append(f"by={m.end_reason}")
    return " ".join(p for p in parts if p)


def _modifier_id(m: Modifier) -> str:
    sc = m.scope
    if "pokemon_id" in sc:
        scope = f"{sc['pokemon_id']}-{sc['form']}"
        if sc["strong"]:
            scope = "strong-" + scope
    else:
        scope = "all-strong" if sc["strong"] else "all"
    e = m.effect
    if m.type == "move_pin":
        effect = f"{e['slot']}-{e['move']}"
    elif m.type == "level_offset":
        effect = f"delta{e['delta']:+d}"
    else:
        # size_cutoffs too: its boundaries grow while the cycle runs, and
        # the id must not change with them.
        effect = ""
    return "-".join(p for p in (_iso(m.start)[:10], m.type, scope, effect)
                    if p)


def modifiers_json(mods: list[Modifier], generated_at: int) -> dict:
    """The shareable snapshot (design record §6) of the active modifiers in
    mods; ended ones are left out."""
    out = []
    for m in mods:
        if m.status not in ACTIVE:
            continue
        scope = {"strong": m.scope["strong"]}
        if "pokemon_id" in m.scope:
            scope["pokemon"] = [{"id": m.scope["pokemon_id"],
                                 "form": m.scope["form"]}]
        out.append({
            "id": _modifier_id(m),
            "scope": scope,
            "window": {"start": _iso(m.start), "end": None,
                       "time_basis": "utc"},
            "effect": dict(m.effect),
            "status": m.status,
            "evidence": {"observations": m.observations,
                         "contradictions": m.contradictions},
        })
    return {"version": 1, "generated_at": _iso(generated_at),
            "modifiers": out}


# --- sampling simulation ----------------------------------------------------

DIMS = ("ivs", "level", "size", "quick", "charged")


@dataclass
class SamplingRule:
    pct: float          # percent of encounters sampled past the minimum
    min_per_hour: int   # always sampled per key per hour
    seed: int = 1
    strong_pct: float | None = None  # pct for strong pokemon; None = pct
    # "tz": key (pokemon_id, form, strong, utc_offset) per LOCAL hour, as a
    # scanner keying its minimum by timezone; "global": (pokemon_id, form,
    # strong) per UTC hour.
    key: str = "tz"


@dataclass
class SamplingCost:
    """Encounters a rule keeps: kept / checked, of the encounters the full
    log holds (an encounter-everything scanner's)."""
    checked: int = 0
    kept: int = 0
    strong_checked: int = 0
    strong_kept: int = 0


def _binomial(rng, n, p):
    if p >= 1:
        return n
    if p <= 0:
        return 0
    return sum(rng.random() < p for _ in range(n))


def _hypergeom(rng, ok, n, k):
    """Successes in k draws without replacement from n holding ok."""
    if k >= n:
        return ok
    got = 0
    for i in range(k):
        if rng.random() * (n - i) < ok - got:
            got += 1
    return got


def thin(records: Iterator[dict], rule: SamplingRule,
         cost: SamplingCost | None = None) -> Iterator[dict]:
    """records as a sampling scanner would have logged them; see the module
    docstring. Changed records are copies; pct=100 yields them unchanged.
    cost, when given, accumulates the summaries' checked counts in full and
    kept."""
    rng = random.Random(rule.seed)
    base_p = rule.pct / 100
    strong_p = base_p if rule.strong_pct is None else rule.strong_pct / 100
    # key -> [minimum used by summaries, deviations kept by the minimum and
    # not yet covered by a summary, as (ts, boosted, utc_offset, dimensions
    # they disagree on), latest UTC hour seen]
    quota: dict = {}
    newest = None
    for rec in records:
        t = rec["ts"] if rec["kind"] == "deviation" else rec["bucket"]
        utc_hour = t // 3600
        if newest is None or utc_hour > newest:
            newest = utc_hour
            for k in [k for k, v in quota.items() if v[2] < newest - 2]:
                del quota[k]
        strong = bool(rec["strong"])
        p = strong_p if strong else base_p
        if rule.key == "tz":
            off = rec.get("utc_offset", 0)
            key = (rec["pokemon_id"], rec["form"], strong, off,
                   (t + off * 60) // 3600)
        else:
            key = (rec["pokemon_id"], rec["form"], strong, utc_hour)
        q = quota.setdefault(key, [0, [], utc_hour])
        q[2] = max(q[2], utc_hour)

        if rec["kind"] == "deviation":
            if q[0] + len(q[1]) < rule.min_per_hour:
                agree = rec.get("agree") or {}
                q[1].append((rec["ts"], bool(rec.get("boosted")),
                             rec.get("utc_offset", 0),
                             frozenset(d for d in DIMS
                                       if agree.get(d) == "disagree")))
                yield rec
            elif rng.random() < p:
                yield rec
            continue

        # A summary covers its bucket's deviations with its own boosted and
        # utc_offset: they count as its first checks, inside its share of
        # the minimum, not on top of it.
        lo, hi = rec["bucket"], rec["bucket"] + rec["bucket_seconds"]
        own = (bool(rec.get("boosted")), rec.get("utc_offset", 0))
        covered = [d for d in q[1]
                   if lo <= d[0] < hi and (d[1], d[2]) == own]
        q[1] = [d for d in q[1] if d not in covered]
        remaining = max(0, rule.min_per_hour - q[0] - len(q[1]))
        out = dict(rec)
        for d in DIMS:
            c = rec.get(d)
            if not isinstance(c, dict):
                continue
            n, ok = c.get("n", 0), c.get("ok", 0)
            base = min(n, remaining)
            kept = base + _binomial(rng, n - base, p)
            # Kept deviations disagreeing on d are kept disagreements, not
            # draws; base >= forced, as they are among its first checks.
            forced = min(kept, sum(1 for dv in covered if d in dv[3]))
            out[d] = {**c, "n": kept,
                      "ok": _hypergeom(rng, ok, n - forced, kept - forced)}
        checked = rec.get("checked", 0)
        base = min(checked, remaining)
        out["checked"] = base + _binomial(rng, checked - base, p)
        q[0] += base
        if cost is not None:
            cost.checked += checked
            cost.kept += out["checked"]
            if strong:
                cost.strong_checked += checked
                cost.strong_kept += out["checked"]
        yield out


def _effect_key(m: Modifier) -> str:
    """The detector's track key for m's effect: a size_cutoffs effect
    changes while it runs, so only its type identifies it."""
    effect = {"type": m.type} if m.type == "size_cutoffs" else m.effect
    return json.dumps(effect, sort_keys=True)


def _cycles(transitions: list[Transition]) -> list[dict]:
    """One entry per modifier cycle with its transition times, in start
    order."""
    by_mod: dict = {}
    for t in transitions:
        c = by_mod.setdefault(id(t.modifier), {
            "mod": t.modifier,
            "key": (t.modifier.type, _scope_key(t.modifier.scope),
                    _effect_key(t.modifier)),
            "candidate": None, "confirmed": None, "ended": None})
        c[t.state] = t.ts
    return sorted(by_mod.values(), key=lambda c: c["candidate"])


def compare_runs(full: list[Transition],
                 sampled: list[Transition]) -> list[dict]:
    """Per cycle of the full run, the delay in seconds of the sampled run's
    start (candidate), confirm and end: "missed" when the sampled run never
    saw it, None when the full run did not either. A cycle is matched by
    the first unused sampled cycle of the same type, scope and effect whose
    active span overlaps it."""
    inf = float("inf")
    pool = _cycles(sampled)
    used = set()
    rows = []
    for f in _cycles(full):
        f_end = f["ended"] if f["ended"] is not None else inf
        match = None
        for i, s in enumerate(pool):
            s_end = s["ended"] if s["ended"] is not None else inf
            if (i not in used and s["key"] == f["key"]
                    and s["candidate"] <= f_end and f["candidate"] <= s_end):
                match = s
                used.add(i)
                break
        row = {"type": f["mod"].type, "scope": dict(f["mod"].scope),
               "effect": dict(f["mod"].effect), "at": f["candidate"]}
        for name, state in (("start", "candidate"), ("confirm", "confirmed"),
                            ("end", "ended")):
            if f[state] is None:
                row[name] = None
            elif match is None or match[state] is None:
                row[name] = "missed"
            else:
                row[name] = match[state] - f[state]
        rows.append(row)
    return rows


def _share(kept, total) -> str:
    return f"{kept} of {total} ({100 * kept / total:.1f}%)" if total else "0 of 0"


def _print_cost(cost: SamplingCost, out) -> None:
    print(f"encounters kept: {_share(cost.kept, cost.checked)}; "
          f"strong {_share(cost.strong_kept, cost.strong_checked)}",
          file=out)


def _delay_text(v) -> str:
    if v is None:
        return "-"
    return v if isinstance(v, str) else f"{v:+d}s"


def _print_comparison(rows: list[dict], out) -> None:
    head = ("full_start", "type", "scope", "effect", "start", "confirm",
            "end")
    table = [head] + [
        (_iso(r["at"]), r["type"], _scope_text(r["scope"]),
         _effect_text(r["effect"]) or "-", _delay_text(r["start"]),
         _delay_text(r["confirm"]), _delay_text(r["end"])) for r in rows]
    widths = [max(len(row[i]) for row in table) for i in range(len(head))]
    for row in table:
        print("  ".join(c.ljust(w) for c, w in zip(row, widths)).rstrip(),
              file=out)


# --- CLI --------------------------------------------------------------------

def _parse_time(s: str) -> int:
    try:
        dt = datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        raise argparse.ArgumentTypeError(f"not an ISO 8601 time: {s!r}")
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return int(dt.timestamp())


def _parse_rule(s: str) -> SamplingRule:
    fields = {"pct": float, "min_per_hour": int, "seed": int,
              "strong_pct": float, "key": str}
    got = {}
    for part in s.split(","):
        k, sep, v = part.partition("=")
        k = k.strip()
        if not sep or k not in fields:
            raise argparse.ArgumentTypeError(
                f"unknown key {k!r} in {s!r} (keys: pct, min_per_hour, "
                f"seed, strong_pct, key)")
        try:
            got[k] = fields[k](v)
        except ValueError:
            raise argparse.ArgumentTypeError(f"bad value for {k}: {v!r}")
    if got.get("key", "tz") not in ("tz", "global"):
        raise argparse.ArgumentTypeError(
            f"key must be tz or global, not {got['key']!r}")
    missing = [k for k in ("pct", "min_per_hour") if k not in got]
    if missing:
        raise argparse.ArgumentTypeError(f"missing {', '.join(missing)}")
    return SamplingRule(**got)


def _replay(records, params, at, on_transition=None):
    """Feeds records up to at (None: all); returns the detector, its
    transitions and the time of the last record fed."""
    det, trs, last = Detector(params), [], None
    for rec in records:
        t = order_key(rec)[0]
        if at is not None and t > at:
            break
        last = t
        for tr in det.feed(rec):
            trs.append(tr)
            if on_transition:
                on_transition(tr)
    return det, trs, last


def main(argv: list[str]) -> int:
    d = Params()
    ap = argparse.ArgumentParser(
        prog="eventreplay.py", description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("files", nargs="+", metavar="FILE",
                    help="events files, plain or gzip, any order")
    ap.add_argument("--start", type=int, default=d.start,
                    help=f"deviations to become a candidate ({d.start})")
    ap.add_argument("--confirm", type=int, default=d.confirm,
                    help=f"deviations to confirm ({d.confirm})")
    ap.add_argument("--end", type=int, default=d.end,
                    help=f"agreements to end ({d.end})")
    ap.add_argument("--window", type=int, default=d.window,
                    help=f"seconds the start deviations fall within "
                         f"({d.window})")
    ap.add_argument("--unseen", type=int, default=d.unseen,
                    help=f"seconds without a check of the scope to end "
                         f"({d.unseen})")
    ap.add_argument("--emit-modifiers", metavar="PATH",
                    help="write the active modifiers as JSON")
    ap.add_argument("--at", type=_parse_time, metavar="ISO8601",
                    help="stop the replay at this time (naive = UTC)")
    ap.add_argument("--simulate-sampling", type=_parse_rule,
                    metavar="pct=P,min_per_hour=K[,seed=S]",
                    help="compare detection under thinned sampling instead "
                         "of printing the timeline (approximation: see "
                         "above; size end detection is not thinned)")
    args = ap.parse_args(argv)
    params = Params(start=args.start, confirm=args.confirm, end=args.end,
                    window=args.window, unseen=args.unseen)

    if args.simulate_sampling:
        det, full, last = _replay(read_records(args.files), params, args.at)
        cost = SamplingCost()
        _, sampled, _ = _replay(
            thin(read_records(args.files), args.simulate_sampling, cost),
            params, args.at)
        _print_comparison(compare_runs(full, sampled), sys.stdout)
        _print_cost(cost, sys.stdout)
    else:
        det, _, last = _replay(
            read_records(args.files), params, args.at,
            lambda tr: print(format_transition(tr)))

    if args.emit_modifiers:
        when = args.at if args.at is not None else (last or 0)
        with open(args.emit_modifiers, "w") as f:
            json.dump(modifiers_json(det.active(), when), f, indent=2)
            f.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
