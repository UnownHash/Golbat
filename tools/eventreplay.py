#!/usr/bin/env python3
"""Replay golbat's derivation events log and propose event modifiers.

    python3 tools/eventreplay.py events/2026-11-14.jsonl.gz events/2026-11-15.jsonl

Inputs are the files golbat writes with derivation events logging on: JSON
lines, one file per UTC day, older days gzipped. Plain and gzip files can be
mixed (gzip is recognised by its magic bytes, not the name), in any order.
Two record kinds are read; anything else is skipped:

- `deviation`: one encounter whose derived IVs, level, size or moves
  disagreed with the game's (a move outside its pool included). Ordered by
  `ts`.
- `summary`: per bucket (15 min by default) and (pokemon_id, form, strong,
  boosted, utc_offset), how many checks agreed on each dimension, plus the
  raw size-roll range per observed size class. Every check counts, the
  deviations' too. Ordered by bucket end (`bucket` + `bucket_seconds`): a
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
  deviation that would empty an interval is a contradiction. null is an
  unbounded end.
- unexplained: IVs disagree, i.e. the walk itself did not match, so the
  other dimensions say nothing. Per species, no effect payload.

Lifecycle, with the thresholds as Params:

- candidate: `start` matching deviations within `window` seconds. For
  level_offset, move_pin and unexplained there must also be no agreement on
  the dimension in scope since the first of them; a summary bucket counts
  when it starts at or after that deviation. size_cutoffs skips that test
  since a moved cut leaves most rolls agreeing.
- confirmed: `confirm` matching deviations with no contradiction.
- ended: after the last matching deviation, summaries for the scope whose
  bucket starts after it add up to `end` agreements on the dimension with
  no matching deviation in between. The transition time is that summary's
  bucket end. A later matching deviation starts a new candidate cycle.
"""
import gzip
import heapq
import json
import sys
from collections import deque
from dataclasses import dataclass, field
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
        except (EOFError, OSError) as e:  # truncated or corrupt gzip
            warn(f"stream ends early ({e})")


def read_records(paths: list[str]) -> Iterator[dict]:
    """Records from every file, merged by order_key."""
    return heapq.merge(*(_file_records(p) for p in paths), key=order_key)


# --- detector ---------------------------------------------------------------

@dataclass
class Params:
    start: int = 3      # matching deviations within window → candidate
    confirm: int = 10   # matching deviations, no contradiction → confirmed
    end: int = 20       # agreements after the last deviation → ended
    window: int = 1800  # seconds the `start` deviations must fall within


@dataclass
class Modifier:
    type: str           # level_offset | move_pin | size_cutoffs | unexplained
    scope: dict
    effect: dict
    status: str         # candidate | confirmed | ended
    start: int | None = None  # first matching deviation of this cycle
    end: int | None = None    # the ended transition's time
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
    mod: Modifier | None = None
    last_match: int = 0
    end_ok: int = 0
    rolls: dict = field(default_factory=dict)  # size class -> [min, max]


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

    def active(self) -> list[Modifier]:
        return [m for m in self._mods if m.status in ACTIVE]

    def all(self) -> list[Modifier]:
        return list(self._mods)

    def feed(self, rec: dict) -> list[Transition]:
        if rec.get("kind") == "deviation":
            return self._deviation(rec)
        if rec.get("kind") == "summary":
            return self._summary(rec)
        return []

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
            t.pending.append((ts, _species(rec), roll,
                              rec.get("observed", {}).get("size")))
            while t.pending and t.pending[0][0] < ts - p.window:
                t.pending.popleft()
            species = {s for _, s, _, _ in t.pending}
            if len(t.pending) < p.start or len(species) < t.min_species:
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
            t.pending.clear()
            t.mod, t.last_match, t.end_ok = mod, ts, 0
            self._mods.append(mod)
            return [Transition(ts, off, "candidate", mod)]

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
                if t.mod is None:
                    if (t.gate and ok > 0 and t.pending
                            and bucket >= t.pending[0][0]):
                        t.pending.clear()
                    continue
                if t.type == "size_cutoffs" and bucket >= t.mod.start:
                    self._summary_rolls(t, rec.get("size_rolls"))
                if bucket <= t.last_match:
                    continue
                t.end_ok += ok
                if t.end_ok >= self.p.end:
                    mod = t.mod
                    mod.status, mod.end = "ended", bucket_end
                    t.mod, t.end_ok = None, 0
                    out.append(Transition(bucket_end, off, "ended", mod))
        return out

    def _summary_rolls(self, t, size_rolls):
        for cls, r in (size_rolls or {}).items():
            if r is None:
                continue
            try:
                cls = int(cls)
            except ValueError:
                continue
            _add_roll(t.rolls, cls, r[0], r[1])
        t.mod.effect = _size_effect(t.rolls, t.scope["strong"])


def main() -> int:
    """Minimal driver: print every transition. The full CLI comes later."""
    det = Detector(Params())
    for rec in read_records(sys.argv[1:]):
        for t in det.feed(rec):
            m = t.modifier
            print(t.ts, t.state.upper(), m.type, json.dumps(m.effect),
                  json.dumps(m.scope),
                  f"obs={m.observations} contra={m.contradictions}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
