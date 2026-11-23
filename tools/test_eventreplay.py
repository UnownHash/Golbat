"""Tests for tools/eventreplay.py (reader, detector, CLI and outputs).

    python3 -m unittest discover -s tools -p 'test_*.py' -v
"""
import calendar
import contextlib
import gzip
import io
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import eventreplay  # noqa: E402
from eventreplay import (Detector, Modifier, Params, SamplingRule,  # noqa: E402
                         Transition, compare_runs, format_transition,
                         modifiers_json, read_records, thin)

T0 = 1_763_200_800  # 2025-11-15T10:00:00Z, a bucket boundary
BUCKET = 900


def branch(level=20, size=3, attack=10, defense=10, stamina=10):
    return {"attack": attack, "defense": defense, "stamina": stamina,
            "level": level, "size": size}


def dev(ts, pokemon_id=959, form=3272, strong=True, ivs="agree",
        level="agree", size="agree", quick="agree", charged="agree",
        obs_level=20, der_level=20, obs_size=3, der_size=3, size_roll=0.5,
        obs_move_1=200, der_move_1=200, obs_move_2=268, der_move_2=268,
        utc_offset=0):
    """A synthetic deviation row in the writer's format."""
    return {
        "kind": "deviation", "ts": ts, "utc_offset": utc_offset,
        "encounter_id": "123456789012345678", "pokemon_id": pokemon_id,
        "form": form, "costume": 0, "strong": strong, "boosted": False,
        "weather": 0, "cell": "89c25c", "spawn_id": "5f1c0e2a9b7d", "first_seen": ts - 60,
        "account": "abcd",
        "observed": branch(level=obs_level, size=obs_size),
        "derived": branch(level=der_level, size=der_size),
        "opposite": branch(),
        "size_roll": size_roll,
        "observed_move_1": obs_move_1, "observed_move_2": obs_move_2,
        "derived_move_1": der_move_1, "derived_move_2": der_move_2,
        "move_1_in_pool": True, "move_2_in_pool": obs_move_2 != 494,
        "agree": {"ivs": ivs, "level": level, "size": size,
                  "quick": quick, "charged": charged},
    }


def summ(bucket, pokemon_id=959, form=3272, strong=True, ok=0, n=0,
         dims=("ivs", "level", "size", "quick", "charged"), size_rolls=None,
         utc_offset=0):
    """A synthetic summary row: every dimension in dims gets {ok, n}."""
    rec = {
        "kind": "summary", "bucket": bucket, "bucket_seconds": BUCKET,
        "pokemon_id": pokemon_id, "form": form, "strong": strong,
        "boosted": False, "utc_offset": utc_offset, "checked": n,
        "size_rolls": size_rolls,
    }
    for d in ("ivs", "level", "size", "quick", "charged"):
        rec[d] = {"ok": ok, "n": n} if d in dims else {"ok": 0, "n": 0}
    return rec


def level_dev(ts, **kw):
    kw.setdefault("level", "disagree")
    kw.setdefault("obs_level", 25)
    kw.setdefault("der_level", 20)
    return dev(ts, **kw)


def feed_all(det, recs):
    out = []
    for r in recs:
        out.extend(det.feed(r))
    return out


def find(mods, type_, scope=None):
    return [m for m in mods if m.type == type_
            and (scope is None or m.scope == scope)]


class DetectorTest(unittest.TestCase):
    def test_level_offset_lifecycle(self):
        det = Detector(Params())
        scope = {"strong": True, "pokemon_id": 959, "form": 3272}

        trs = feed_all(det, [level_dev(T0 + i) for i in range(2)])
        self.assertEqual(find([t.modifier for t in trs], "level_offset"), [])

        trs = det.feed(level_dev(T0 + 2))
        self.assertEqual([(t.state, t.ts) for t in trs
                          if t.modifier.scope == scope],
                         [("candidate", T0 + 2)])
        mod = trs[0].modifier
        self.assertEqual(mod.effect, {"type": "level_offset", "delta": 5})
        self.assertEqual((mod.status, mod.start, mod.observations),
                         ("candidate", T0, 3))

        trs = feed_all(det, [level_dev(T0 + i) for i in range(3, 10)])
        self.assertEqual([t.state for t in trs if t.modifier is mod],
                         ["confirmed"])
        self.assertEqual(trs[-1].ts, T0 + 9)
        self.assertEqual((mod.status, mod.observations, mod.contradictions),
                         ("confirmed", 10, 0))

        # The bucket holding the deviations does not count toward the end.
        self.assertEqual(det.feed(summ(T0, ok=50, n=60)), [])
        # Agreements after the last deviation: 12, then 8 more → ended.
        self.assertEqual(det.feed(summ(T0 + BUCKET, ok=12, n=12)), [])
        trs = det.feed(summ(T0 + 2 * BUCKET, ok=8, n=8))
        self.assertEqual([(t.state, t.ts) for t in trs if t.modifier is mod],
                         [("ended", T0 + 3 * BUCKET)])
        self.assertEqual((mod.status, mod.end), ("ended", T0 + 3 * BUCKET))
        self.assertNotIn(mod, det.active())
        self.assertIn(mod, det.all())

        # A matching deviation after the end opens a new candidate.
        trs = feed_all(det, [level_dev(T0 + 4 * BUCKET + i) for i in range(3)])
        new = [t.modifier for t in trs if t.modifier.scope == scope]
        self.assertEqual(len(new), 1)
        self.assertIsNot(new[0], mod)
        self.assertEqual(new[0].status, "candidate")

    def test_level_offset_needs_ivs_agree_and_no_agreement(self):
        det = Detector(Params())
        feed_all(det, [level_dev(T0 + i, ivs="disagree") for i in range(5)])
        self.assertEqual(find(det.all(), "level_offset"), [])

        det = Detector(Params())
        det.feed(level_dev(T0 + BUCKET))
        # A level agreement in scope since the first deviation resets it.
        det.feed(summ(T0 + BUCKET, ok=1, n=2))
        feed_all(det, [level_dev(T0 + 2 * BUCKET + i) for i in range(2)])
        self.assertEqual(find(det.all(), "level_offset"), [])
        det.feed(level_dev(T0 + 2 * BUCKET + 5))
        self.assertEqual(len(find(det.all(), "level_offset")), 1)

    def test_level_offset_window(self):
        det = Detector(Params(window=1800))
        feed_all(det, [level_dev(T0), level_dev(T0 + 1000),
                       level_dev(T0 + 2000)])
        self.assertEqual(find(det.all(), "level_offset"), [])
        det.feed(level_dev(T0 + 2500))
        self.assertEqual(find(det.all(), "level_offset")[0].start, T0 + 1000)

    def test_level_offset_broad_strong_scope(self):
        det = Detector(Params())
        broad = {"strong": True}
        feed_all(det, [level_dev(T0 + i) for i in range(4)])
        self.assertEqual(find(det.all(), "level_offset", broad), [],
                         "one species alone must not make a broad candidate")
        trs = det.feed(level_dev(T0 + 10, pokemon_id=19, form=45))
        got = [t for t in trs if t.modifier.scope == broad]
        self.assertEqual([t.state for t in got], ["candidate"])
        self.assertEqual(got[0].modifier.effect,
                         {"type": "level_offset", "delta": 5})
        self.assertEqual(got[0].modifier.observations, 5)
        # Non-strong deviations never feed the broad scope.
        det = Detector(Params())
        feed_all(det, [level_dev(T0 + i, strong=False, pokemon_id=p)
                       for i, p in enumerate((1, 2, 3, 4))])
        self.assertEqual(find(det.all(), "level_offset", broad), [])
        self.assertEqual(find(det.all(), "level_offset",
                              {"strong": False}), [])

    def test_move_pin_out_of_pool(self):
        det = Detector(Params())
        scope = {"strong": True, "pokemon_id": 959, "form": 3272}
        trs = feed_all(det, [dev(T0 + i, charged="disagree", obs_move_2=494)
                             for i in range(3)])
        pins = [t for t in trs if t.modifier.type == "move_pin"]
        self.assertEqual([t.state for t in pins], ["candidate"])
        mod = pins[0].modifier
        self.assertEqual(mod.scope, scope)
        self.assertEqual(mod.effect,
                         {"type": "move_pin", "slot": "charged", "move": 494})

        det.feed(dev(T0 + 3, charged="disagree", obs_move_2=300))
        self.assertEqual(mod.contradictions, 1)
        trs = feed_all(det, [dev(T0 + 10 + i, charged="disagree",
                                 obs_move_2=494) for i in range(20)])
        self.assertEqual([t for t in trs if t.modifier is mod], [])
        self.assertEqual((mod.status, mod.observations), ("candidate", 23))
        # The quick slot is a separate key; it never touches the charged pin.
        self.assertEqual(find(det.all(), "move_pin")[1:], [])

    def test_move_pin_confirms_without_contradiction(self):
        det = Detector(Params())
        trs = feed_all(det, [dev(T0 + i, quick="disagree", obs_move_1=250)
                             for i in range(10)])
        self.assertEqual([t.state for t in trs], ["candidate", "confirmed"])
        self.assertEqual(trs[0].modifier.effect,
                         {"type": "move_pin", "slot": "quick", "move": 250})

    def test_size_cutoff_interval(self):
        det = Detector(Params())
        rolls = (0.008, 0.009, 0.010, 0.011)
        trs = feed_all(det, [dev(T0 + i, pokemon_id=19, form=45, strong=False,
                                 size="disagree", obs_size=1, der_size=2,
                                 size_roll=r) for i, r in enumerate(rolls)])
        sizes = [t for t in trs if t.modifier.type == "size_cutoffs"]
        self.assertEqual([t.state for t in sizes], ["candidate"])
        mod = sizes[0].modifier
        self.assertEqual(mod.scope,
                         {"strong": False, "pokemon_id": 19, "form": 45})
        lo, hi = mod.effect["boundaries"]["1/2"]
        self.assertGreaterEqual(lo, 0.011)
        self.assertFalse(lo < 0.004 <= (hi if hi is not None else 1.0),
                         "interval must exclude the base cut 0.004")
        self.assertEqual(set(mod.effect["boundaries"]), {"1/2"},
                         "boundaries consistent with base cuts are omitted")

        # Summary rolls since the start tighten the upper end.
        det.feed(summ(T0 + BUCKET, pokemon_id=19, form=45, strong=False,
                      ok=40, n=40, dims=(),
                      size_rolls={"1": [0.009, 0.0115], "2": [0.012, 0.028],
                                  "3": [0.03, 0.9]}))
        self.assertEqual(mod.effect["boundaries"],
                         {"1/2": [0.0115, 0.012]})

        # An XS observed below the XXS evidence contradicts the boundary.
        det.feed(dev(T0 + BUCKET + 5, pokemon_id=19, form=45, strong=False,
                     size="disagree", obs_size=2, der_size=1,
                     size_roll=0.003))
        self.assertEqual(mod.contradictions, 1)

    def test_size_cutoff_rattata_shift(self):
        # The observed Rattata event: the two lower cuts moved up by 0.008
        # (XXS < 0.012, XS < 0.037), the upper cuts unchanged. Uniform rolls,
        # logged as the writer would: a deviation where the classes differ,
        # every IV-agreeing roll in its bucket's size_rolls.
        import random as _random
        rng = _random.Random(7)
        game = (0.012, 0.037, 0.971, 0.996)
        base = (0.004, 0.029, 0.971, 0.996)

        def cls(roll, cuts):
            return 1 + sum(roll >= c for c in cuts)

        recs = []
        for b in range(8):
            rolls, n = {}, 0
            for i in range(1500):
                ts = T0 + b * BUCKET + i * BUCKET // 1500
                r = rng.random()
                obs, der = cls(r, game), cls(r, base)
                lo_hi = rolls.setdefault(str(obs), [r, r])
                lo_hi[0], lo_hi[1] = min(lo_hi[0], r), max(lo_hi[1], r)
                n += 1
                if obs != der:
                    recs.append(dev(ts, pokemon_id=19, form=45, strong=False,
                                    size="disagree", obs_size=obs,
                                    der_size=der, size_roll=r))
            recs.append(summ(T0 + b * BUCKET, pokemon_id=19, form=45,
                             strong=False, ok=n, n=n, size_rolls=rolls))
        recs.sort(key=eventreplay.order_key)
        trs, det = replay(recs)
        mods = find(det.active(), "size_cutoffs")
        self.assertEqual(len(mods), 1)
        mod = mods[0]
        self.assertEqual(mod.status, "confirmed")
        self.assertEqual(mod.contradictions, 0)
        bounds = mod.effect["boundaries"]
        self.assertEqual(set(bounds), {"1/2", "2/3"},
                         "upper cuts agree with the base and are not listed")
        for name, cut in (("1/2", 0.012), ("2/3", 0.037)):
            lo, hi = bounds[name]
            self.assertTrue(lo < cut <= hi, (name, lo, hi))
            self.assertLess(hi - lo, 0.001, (name, lo, hi))
        self.assertNotIn("ended", [t.state for t in trs])

    def size_event(self):
        """A Rattata 1/2 cut moved up: XXS observed at 0.008-0.011."""
        det = Detector(Params())
        trs = feed_all(det, [dev(T0 + i, pokemon_id=19, form=45, strong=False,
                                 size="disagree", obs_size=1, der_size=2,
                                 size_roll=r)
                             for i, r in enumerate((0.008, 0.009, 0.011))])
        return det, trs[0].modifier

    def test_size_cutoff_survives_far_agreements(self):
        det, mod = self.size_event()
        for b in range(1, 40):
            trs = det.feed(summ(T0 + b * BUCKET, pokemon_id=19, form=45,
                                strong=False, ok=200, n=200,
                                size_rolls={"2": [0.015, 0.028],
                                            "3": [0.03, 0.97]}))
            self.assertEqual(trs, [])
        self.assertEqual(mod.status, "candidate")
        self.assertIn(mod, det.active())
        self.assertEqual(mod.effect["boundaries"], {"1/2": [0.011, 0.015]})

    def test_size_cutoff_ends_on_base_classified_roll(self):
        det, mod = self.size_event()
        # XS at 0.006 inside the bucket of the last deviation: not yet.
        self.assertEqual(det.feed(summ(T0, pokemon_id=19, form=45,
                                       strong=False, ok=1, n=1,
                                       size_rolls={"2": [0.006, 0.02]})), [])
        self.assertEqual(mod.status, "candidate")
        trs = det.feed(summ(T0 + BUCKET, pokemon_id=19, form=45, strong=False,
                            ok=1, n=1, size_rolls={"2": [0.006, 0.02]}))
        self.assertEqual([(t.state, t.ts) for t in trs],
                         [("ended", T0 + 2 * BUCKET)])
        self.assertEqual((mod.status, mod.end), ("ended", T0 + 2 * BUCKET))

    def test_size_cutoff_moved_down_ends(self):
        det = Detector(Params())
        trs = feed_all(det, [dev(T0 + i, size="disagree", obs_size=5,
                                 der_size=4, size_roll=r)
                             for i, r in enumerate((0.70, 0.75, 0.78))])
        mod = trs[0].modifier
        self.assertEqual(det.feed(summ(T0 + BUCKET, ok=9, n=9,
                                       size_rolls={"4": [0.1, 0.65]})), [])
        trs = det.feed(summ(T0 + 2 * BUCKET, ok=9, n=9,
                            size_rolls={"4": [0.1, 0.72]}))
        self.assertEqual([t.state for t in trs], ["ended"])
        self.assertEqual(mod.end, T0 + 3 * BUCKET)

    def test_candidate_confirms_at_once_when_start_reaches_confirm(self):
        det = Detector(Params(start=3, confirm=3))
        trs = feed_all(det, [dev(T0 + i, ivs="disagree") for i in range(3)])
        self.assertEqual([(t.state, t.ts) for t in trs],
                         [("candidate", T0 + 2), ("confirmed", T0 + 2)])
        self.assertEqual(trs[0].modifier.status, "confirmed")

    def test_size_cutoff_strong(self):
        det = Detector(Params())
        trs = feed_all(det, [dev(T0 + i, size="disagree", obs_size=5,
                                 der_size=4, size_roll=r)
                             for i, r in enumerate((0.70, 0.75, 0.78))])
        mod = trs[0].modifier
        self.assertEqual(mod.effect["boundaries"], {"4/5": [None, 0.70]})

    def test_unexplained(self):
        det = Detector(Params())
        trs = feed_all(det, [dev(T0 + i, ivs="disagree", level="disagree",
                                 obs_level=25) for i in range(3)])
        self.assertEqual([(t.state, t.modifier.type) for t in trs],
                         [("candidate", "unexplained")])
        self.assertEqual(trs[0].modifier.effect, {"type": "unexplained"})
        self.assertEqual(trs[0].modifier.scope,
                         {"strong": True, "pokemon_id": 959, "form": 3272})
        self.assertEqual(det.active(), [trs[0].modifier])

    def test_transition_carries_utc_offset(self):
        det = Detector(Params())
        trs = feed_all(det, [dev(T0 + i, ivs="disagree", utc_offset=-300)
                             for i in range(3)])
        self.assertEqual(trs[0].utc_offset, -300)


class UnseenEndTest(unittest.TestCase):
    def _pin_until(self, last):
        """A charged pin on strong 959/3272 from T0 to last, then only
        other species: the event-only form stops spawning."""
        recs = [dev(t, charged="disagree", obs_move_2=494)
                for t in range(T0, last + 1, 60)]
        recs += [summ(T0 + 8 * BUCKET + b * BUCKET, pokemon_id=19, form=0,
                      strong=False, ok=50, n=50) for b in range(8)]
        recs.sort(key=eventreplay.order_key)
        return recs

    def test_ends_at_last_seen_when_scope_vanishes(self):
        last = T0 + 1800
        trs, det = replay(self._pin_until(last))
        self.assertEqual([t.state for t in trs],
                         ["candidate", "confirmed", "ended"])
        end = trs[-1]
        self.assertEqual((end.ts, end.modifier.end, end.modifier.end_reason),
                         (last, last, "unseen"))
        self.assertIn("by=unseen", format_transition(end))
        self.assertEqual(det.active(), [])

    def test_agreements_keep_scope_seen(self):
        # Agreements on the slot during quiet spells keep it alive, below
        # the `end` count; nothing ends.
        recs = [dev(T0 + i, charged="disagree", obs_move_2=494)
                for i in range(10)]
        recs += [summ(T0 + b * BUCKET, ok=1, n=1, dims=("charged",))
                 for b in range(1, 12)]
        recs.sort(key=eventreplay.order_key)
        trs, det = replay(recs)
        self.assertEqual([t.state for t in trs], ["candidate", "confirmed"])
        self.assertEqual(len(det.active()), 1)

    def test_sightings_keep_scope_seen(self):
        # Encountered once an hour (sampling), but still spawning: the
        # sightings keep the pin active until they stop.
        recs = [dev(T0 + i, charged="disagree", obs_move_2=494)
                for i in range(10)]
        recs += [summ(T0 + b * BUCKET, ok=0, n=0) | {"seen": 40}
                 for b in range(1, 8)]
        recs += [summ(T0 + b * BUCKET, pokemon_id=19, form=0, strong=False,
                      ok=50, n=50) for b in range(8, 16)]
        recs.sort(key=eventreplay.order_key)
        trs, _ = replay(recs)
        self.assertEqual([t.state for t in trs],
                         ["candidate", "confirmed", "ended"])
        self.assertEqual((trs[-1].ts, trs[-1].modifier.end_reason),
                         (T0 + 8 * BUCKET, "unseen"))

    def test_no_end_before_unseen_passes(self):
        last = T0 + 1800
        recs = [r for r in self._pin_until(last)
                if eventreplay.order_key(r)[0] <= last + 3600]
        trs, _ = replay(recs)
        self.assertEqual([t.state for t in trs], ["candidate", "confirmed"])

    def test_new_cycle_after_unseen_end(self):
        last = T0 + 600
        recs = self._pin_until(last)
        later = T0 + 3 * 3600
        recs += [dev(later + i, charged="disagree", obs_move_2=494)
                 for i in range(3)]
        recs.sort(key=eventreplay.order_key)
        trs, _ = replay(recs)
        self.assertEqual([t.state for t in trs],
                         ["candidate", "confirmed", "ended", "candidate"])
        self.assertIsNot(trs[-1].modifier, trs[0].modifier)


def replay(recs, params=None):
    det = Detector(params or Params())
    return feed_all(det, recs), det


def move_event(hours=5, per_hour=6):
    """A charged-move pin on strong 959/3272 lasting `hours`, `per_hour`
    deviations an hour 2 min apart, with plain agreement summaries between
    and agreements after the event."""
    recs = []
    for h in range(hours):
        for i in range(per_hour):
            recs.append(dev(T0 + h * 3600 + i * 120, charged="disagree",
                            obs_move_2=494))
        for b in range(4):
            recs.append(summ(T0 + h * 3600 + b * BUCKET, ok=0, n=per_hour,
                             dims=("ivs",)))
    for b in range(8):
        recs.append(summ(T0 + hours * 3600 + b * BUCKET, ok=30, n=30))
    recs.sort(key=eventreplay.order_key)
    return recs


class OutputTest(unittest.TestCase):
    def test_format_transition(self):
        ts = calendar.timegm((2026, 11, 15, 10, 3, 12))
        mod = Modifier(type="move_pin",
                       scope={"strong": True, "pokemon_id": 959,
                              "form": 3272},
                       effect={"type": "move_pin", "slot": "charged",
                               "move": 494},
                       status="candidate", start=ts - 60, observations=3)
        self.assertEqual(
            format_transition(Transition(ts, 0, "candidate", mod)),
            "2026-11-15T10:03:12Z (local 10:03 +00:00) CANDIDATE move_pin "
            "charged=494 scope=strong 959/3272 obs=3 contra=0")
        mod2 = Modifier(type="level_offset", scope={"strong": True},
                        effect={"type": "level_offset", "delta": -2},
                        status="confirmed", start=ts, observations=10,
                        contradictions=1)
        self.assertEqual(
            format_transition(Transition(ts, -330, "confirmed", mod2)),
            "2026-11-15T10:03:12Z (local 04:33 -05:30) CONFIRMED "
            "level_offset delta=-2 scope=all-strong obs=10 contra=1")
        mod3 = Modifier(type="unexplained",
                        scope={"strong": False, "pokemon_id": 19, "form": 45},
                        effect={"type": "unexplained"}, status="ended",
                        start=ts, end=ts, observations=4)
        self.assertEqual(
            format_transition(Transition(ts, 60, "ended", mod3)),
            "2026-11-15T10:03:12Z (local 11:03 +01:00) ENDED unexplained "
            "scope=19/45 obs=4 contra=0")

    def test_modifiers_json_shape(self):
        start = calendar.timegm((2026, 11, 15, 10, 0, 0))
        active = Modifier(type="move_pin",
                          scope={"strong": True, "pokemon_id": 959,
                                 "form": 3272},
                          effect={"type": "move_pin", "slot": "charged",
                                  "move": 494},
                          status="confirmed", start=start,
                          observations=412)
        broad = Modifier(type="level_offset", scope={"strong": True},
                         effect={"type": "level_offset", "delta": 5},
                         status="candidate", start=start, observations=5,
                         contradictions=1)
        ended = Modifier(type="unexplained",
                         scope={"strong": False, "pokemon_id": 19,
                                "form": 45},
                         effect={"type": "unexplained"}, status="ended",
                         start=start, end=start + 900, observations=3)
        out = modifiers_json([active, broad, ended], start + 3600)
        json.dumps(out)  # serialisable
        self.assertEqual(set(out), {"version", "generated_at", "modifiers"})
        self.assertEqual(out["version"], 1)
        self.assertEqual(out["generated_at"], "2026-11-15T11:00:00Z")
        self.assertEqual(len(out["modifiers"]), 2, "ended ones are omitted")
        m = out["modifiers"][0]
        self.assertEqual(set(m), {"id", "scope", "window", "effect",
                                  "status", "evidence"})
        self.assertEqual(m["id"], "2026-11-15-move_pin-strong-959-3272-"
                                  "charged-494")
        self.assertEqual(m["scope"], {"strong": True,
                                      "pokemon": [{"id": 959,
                                                   "form": 3272}]})
        self.assertEqual(m["window"], {"start": "2026-11-15T10:00:00Z",
                                       "end": None, "time_basis": "utc"})
        self.assertEqual(m["effect"], {"type": "move_pin", "slot": "charged",
                                       "move": 494})
        self.assertEqual(m["status"], "confirmed")
        self.assertEqual(m["evidence"], {"observations": 412,
                                         "contradictions": 0})
        b = out["modifiers"][1]
        self.assertEqual(b["scope"], {"strong": True})
        self.assertEqual(b["id"], "2026-11-15-level_offset-all-strong-delta+5")
        self.assertEqual(b["evidence"], {"observations": 5,
                                         "contradictions": 1})


class ModifierIdTest(unittest.TestCase):
    def test_size_cutoffs_id_stable_while_growing(self):
        # A size_cutoffs effect gains boundaries as evidence arrives; its id
        # must not change with them, or one cycle reads as two modifiers.
        det = Detector(Params())
        feed_all(det, [dev(T0 + i, pokemon_id=19, form=45, strong=False,
                           size="disagree", obs_size=1, der_size=2,
                           size_roll=r)
                       for i, r in enumerate((0.008, 0.009, 0.011))])
        first = modifiers_json(det.active(), T0 + 10)["modifiers"]
        feed_all(det, [dev(T0 + 20 + i, pokemon_id=19, form=45, strong=False,
                           size="disagree", obs_size=5, der_size=4,
                           size_roll=r) for i, r in enumerate((0.98, 0.985))])
        second = modifiers_json(det.active(), T0 + 30)["modifiers"]
        self.assertEqual(len(first), 1)
        self.assertEqual(len(second), 1)
        self.assertNotEqual(first[0]["effect"], second[0]["effect"],
                            "the boundaries must have grown")
        self.assertEqual(first[0]["id"], second[0]["id"])
        self.assertEqual(first[0]["id"], "2025-11-15-size_cutoffs-19-45")


class SamplingTest(unittest.TestCase):
    def test_tz_key_uses_local_hour_and_offset(self):
        # At UTC+30 min, local hours start at :30 UTC, and a second offset
        # has its own minimum.
        recs = [summ(T0 + b * BUCKET, ok=10, n=10, utc_offset=30)
                for b in range(4)]
        recs += [summ(T0, ok=10, n=10, utc_offset=60)]
        recs.sort(key=eventreplay.order_key)
        got = list(thin(iter(recs), SamplingRule(pct=0, min_per_hour=10)))
        by = [(r["utc_offset"], r["bucket"], r["checked"]) for r in got]
        self.assertEqual(sorted(by), sorted([
            (30, T0, 10), (30, T0 + BUCKET, 0),          # local 10:30 hour
            (30, T0 + 2 * BUCKET, 10), (30, T0 + 3 * BUCKET, 0),  # 11:30
            (60, T0, 10)]))
        glob = list(thin(iter(recs), SamplingRule(pct=0, min_per_hour=10,
                                                  key="global")))
        self.assertEqual(sum(r["checked"] for r in glob), 10,
                         "one UTC-hour minimum shared by both offsets")

    def test_strong_pct_and_cost(self):
        recs = [summ(T0, ok=100, n=100),
                summ(T0, pokemon_id=19, form=0, strong=False, ok=100, n=100)]
        cost = eventreplay.SamplingCost()
        got = list(thin(iter(recs), SamplingRule(pct=0, min_per_hour=0,
                                                 strong_pct=100), cost))
        self.assertEqual({r["pokemon_id"]: r["checked"] for r in got},
                         {959: 100, 19: 0})
        self.assertEqual((cost.checked, cost.kept, cost.strong_checked,
                          cost.strong_kept), (200, 100, 100, 100))

    def test_thin_full_rate_is_identity(self):
        recs = move_event()
        self.assertEqual(list(thin(iter(recs),
                                   SamplingRule(pct=100, min_per_hour=0))),
                         recs)

    def test_thin_keeps_minimum_and_scales_counts(self):
        recs = move_event(hours=3, per_hour=6)
        got = list(thin(iter(recs), SamplingRule(pct=0, min_per_hour=2)))
        devs = [r for r in got if r["kind"] == "deviation"]
        self.assertEqual(len(devs), 6, "two per hour, the first two")
        self.assertEqual([d["ts"] for d in devs],
                         [T0 + h * 3600 + i * 120 for h in range(3)
                          for i in range(2)])
        for r in got:
            if r["kind"] != "summary":
                continue
            for d in ("ivs", "level", "size", "quick", "charged"):
                self.assertLessEqual(r[d]["ok"], r[d]["n"])
            self.assertLessEqual(r["checked"], 30)
        # pct=0: only the per-hour minimum survives. In the event's hours
        # the two kept deviations are bucket 0's first checks; the other
        # buckets count nothing.
        self.assertEqual([r["checked"] for r in got
                          if r["kind"] == "summary"
                          and r["bucket"] < T0 + 3 * 3600],
                         [2, 0, 0, 0] * 3)
        # After the event: two checks an hour, all in the first bucket.
        after = [r["checked"] for r in got if r["kind"] == "summary"
                 and r["bucket"] >= T0 + 3 * 3600]
        self.assertEqual(after, [2, 0, 0, 0, 2, 0, 0, 0])

    def test_thin_quota_deviation_not_counted_as_agreement(self):
        # The one check the hourly minimum keeps is the deviation, which
        # disagrees on charged: the thinned summary must not report it as
        # an agreement.
        recs = [dev(T0 + 60, charged="disagree", obs_move_2=494),
                summ(T0, ok=9, n=10, dims=("charged",))]
        for seed in range(20):
            got = list(thin(iter(recs), SamplingRule(pct=0, min_per_hour=1,
                                                     seed=seed)))
            s = [r for r in got if r["kind"] == "summary"][0]
            self.assertEqual(s["charged"], {"ok": 0, "n": 1}, f"seed {seed}")

    def test_sampling_reports_delay_or_missed(self):
        recs = move_event()
        full, _ = replay(recs)
        rows = compare_runs(full, replay(list(thin(
            iter(recs), SamplingRule(pct=0, min_per_hour=1))))[0])
        pin = [r for r in rows if r["type"] == "move_pin"]
        self.assertEqual(len(pin), 1)
        self.assertTrue(pin[0]["start"] == "missed" or pin[0]["start"] > 0,
                        pin[0])

        rows = compare_runs(full, replay(list(thin(
            iter(recs), SamplingRule(pct=100, min_per_hour=1))))[0])
        pin = [r for r in rows if r["type"] == "move_pin"]
        self.assertEqual(len(pin), 1)
        self.assertEqual((pin[0]["start"], pin[0]["confirm"], pin[0]["end"]),
                         (0, 0, 0))
        self.assertEqual(pin[0]["scope"],
                         {"strong": True, "pokemon_id": 959, "form": 3272})
        self.assertEqual(pin[0]["effect"],
                         {"type": "move_pin", "slot": "charged", "move": 494})

    def test_sampling_delays_detection(self):
        # Half the deviations: candidate and confirm come later, not never.
        recs = move_event(hours=5, per_hour=10)
        full, _ = replay(recs)
        rows = compare_runs(full, replay(list(thin(
            iter(recs), SamplingRule(pct=50, min_per_hour=1, seed=3))))[0])
        pin = [r for r in rows if r["type"] == "move_pin"][0]
        self.assertGreaterEqual(pin["start"], 0)
        self.assertGreater(pin["confirm"], 0)


class FlushedSummaryTest(unittest.TestCase):
    def test_flushed_long_bucket_ends_modifier_at_flush_time(self):
        # A 60-minute bucket flushed 10 minutes in (60 -> 5 minute restart):
        # its agreements end the modifier at the flush, not at T0 + 3600.
        recs = [level_dev(T0 - 600 + i * 60) for i in range(10)]
        long_sum = summ(T0, ok=30, n=30)
        long_sum["bucket_seconds"] = long_sum["bucket_s"] = 3600
        long_sum["flushed_at"] = T0 + 600
        recs.append(long_sum)
        trs, det = replay(recs)
        self.assertEqual([t.state for t in trs],
                         ["candidate", "confirmed", "ended"])
        self.assertEqual(trs[-1].ts, T0 + 600)
        self.assertEqual(trs[-1].modifier.end, T0 + 600)

    def test_flushed_summary_last_seen_is_flush_time(self):
        recs = [level_dev(T0 + i * 60) for i in range(10)]
        long_sum = summ(T0, ok=5, n=5)
        long_sum["bucket_seconds"] = long_sum["bucket_s"] = 3600
        long_sum["flushed_at"] = T0 + 600
        recs.append(long_sum)
        _, det = replay(recs)
        (t,) = [t for t in det._active]
        self.assertEqual(t.last_seen, T0 + 600)


class SamplingHourTest(unittest.TestCase):
    def test_bucket_straddling_quota_hour_is_rejected(self):
        # Codex: a 7-minute bucket over 11:00 would charge its counts to
        # the 10:00 hour and undercount the retained encounters.
        start = T0 + 3600 - 180
        s = summ(start, n=10, ok=10)
        s["bucket_seconds"] = s["bucket_s"] = 420
        with self.assertRaisesRegex(eventreplay.SamplingStraddleError, "straddles"):
            list(thin(iter([s]), SamplingRule(pct=0, min_per_hour=1)))

    def test_flushed_bucket_inside_one_hour_is_accepted(self):
        s = summ(T0, n=10, ok=10)
        s["bucket_seconds"] = s["bucket_s"] = 3600
        s["flushed_at"] = T0 + 600
        got = list(thin(iter([s]), SamplingRule(pct=0, min_per_hour=1)))
        self.assertEqual(got[0]["checked"], 1)

    def test_main_reports_straddle_as_error(self):
        with tempfile.TemporaryDirectory() as d:
            f = os.path.join(d, "e.jsonl")
            s = summ(T0 + 3600 - 180, n=10, ok=10)
            s["bucket_seconds"] = s["bucket_s"] = 420
            with open(f, "w") as fh:
                fh.write(json.dumps(s) + "\n")
            err = io.StringIO()
            with contextlib.redirect_stderr(err), \
                    contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(SystemExit) as cm:
                    eventreplay.main([f, "--simulate-sampling",
                                      "pct=0,min_per_hour=1"])
            self.assertNotEqual(cm.exception.code, 0)
            self.assertIn("straddles", err.getvalue())


class MainTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.file = os.path.join(self.dir.name, "events.jsonl")
        with open(self.file, "w") as f:
            for r in move_event():
                f.write(json.dumps(r) + "\n")

    def run_main(self, *args):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = eventreplay.main([self.file, *args])
            except SystemExit as e:
                code = e.code
        return code, out.getvalue(), err.getvalue()

    def test_main_prints_timeline(self):
        code, out, _ = self.run_main()
        self.assertEqual(code, 0)
        states = [l.split()[4] for l in out.splitlines()]
        self.assertEqual(states, ["CANDIDATE", "CONFIRMED", "ENDED"])

    def test_main_emit_modifiers(self):
        path = os.path.join(self.dir.name, "mods.json")
        # Mid-event (--at): the pin is active and emitted.
        code, _, _ = self.run_main("--emit-modifiers", path,
                                   "--at", "2025-11-15T12:00:00Z")
        self.assertEqual(code, 0)
        with open(path) as f:
            got = json.load(f)
        self.assertEqual(got["generated_at"], "2025-11-15T12:00:00Z")
        self.assertEqual([m["effect"] for m in got["modifiers"]],
                         [{"type": "move_pin", "slot": "charged",
                           "move": 494}])
        # At the end of the run it has ended, so nothing is emitted.
        code, _, _ = self.run_main("--emit-modifiers", path)
        self.assertEqual(code, 0)
        with open(path) as f:
            self.assertEqual(json.load(f)["modifiers"], [])

    def test_main_simulate_sampling(self):
        code, out, _ = self.run_main("--simulate-sampling",
                                     "pct=100,min_per_hour=1")
        self.assertEqual(code, 0)
        self.assertIn("move_pin", out)
        self.assertIn("charged=494", out)
        self.assertIn("encounters kept:", out)
        code, _, _ = self.run_main(
            "--simulate-sampling",
            "pct=5,min_per_hour=12,strong_pct=20,key=global")
        self.assertEqual(code, 0)

    def test_main_rejects_unknown_sampling_key(self):
        code, _, err = self.run_main("--simulate-sampling", "pct=5,bogus=1")
        self.assertEqual(code, 2)
        self.assertIn("bogus", err)


class ReaderTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)

    def path(self, name):
        return os.path.join(self.dir.name, name)

    def write(self, name, recs, gz=False, tail=b""):
        data = b"".join(json.dumps(r).encode() + b"\n" for r in recs) + tail
        p = self.path(name)
        with (gzip.open(p, "wb") if gz else open(p, "wb")) as f:
            f.write(data)
        return p

    def test_corrupt_gzip_body(self):
        p = self.path("bad.gz")
        with open(p, "wb") as f:
            f.write(gzip.compress(b"x")[:10] + b"\xff" * 64)
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            self.assertEqual(list(read_records([p])), [])
        self.assertEqual(len(err.getvalue().strip().splitlines()), 1)

    def test_bucket_length_change_mid_file(self):
        # A restart in the same day changed bucket_minutes from 60 to 5: the
        # shutdown wrote the 60-minute bucket's summary early, then 5-minute
        # rows follow. Rows order by their own bucket, never back in time.
        def tag(r, n):
            r["bucket_s"] = n
            return r
        old_sum = tag(summ(0, n=1, ok=1), 3600)
        old_sum["bucket_seconds"] = 3600
        new_sum = tag(summ(1200, n=1, ok=1), 300)
        new_sum["bucket_seconds"] = 300
        recs = [tag(dev(100, level="disagree"), 3600), old_sum,
                tag(dev(1210, level="disagree"), 300), new_sum]
        p = self.write("mid.jsonl", recs)
        keys = [eventreplay.order_key(r) for r in read_records([p])]
        self.assertEqual(keys, sorted(keys))
        self.assertEqual(len(keys), 4)

    def test_first_bucket_long_does_not_rewrite_times(self):
        # A file starting with a 60-minute bucket: a deviation at 899 after
        # one at 1000 is valid (same bucket) and keeps its time.
        recs = [dev(1000, level="disagree"), dev(899, level="disagree")]
        for r in recs:
            r["bucket_s"] = 3600
        got = list(read_records([self.write("long.jsonl", recs)]))
        self.assertEqual(sorted(r["ts"] for r in got), [899, 1000])
        self.assertTrue(all("event_ts" not in r for r in got))

    def test_legacy_rows_without_bucket_s_clamp(self):
        recs = [dev(1000), dev(899)]
        got = list(read_records([self.write("old.jsonl", recs)]))
        self.assertEqual([r["ts"] for r in got], [900, 1000])

    def test_merge_order(self):
        # Day file 2 holds a summary for a bucket that straddled midnight;
        # its bucket end, not its file, decides where it goes.
        a = self.write("a.jsonl", [dev(T0 + 1), dev(T0 + 100),
                                   summ(T0), dev(T0 + 2000)])
        b = self.write("b.jsonl.gz", [dev(T0 + 50), dev(T0 + 950),
                                      summ(T0 + BUCKET), dev(T0 + 3000)],
                       gz=True)
        got = list(read_records([b, a]))
        keys = [eventreplay.order_key(r) for r in got]
        self.assertEqual(len(got), 8)
        self.assertEqual(keys, sorted(keys))
        self.assertEqual([r.get("ts", r.get("bucket")) for r in got],
                         [T0 + 1, T0 + 50, T0 + 100, T0, T0 + 950,
                          T0 + BUCKET, T0 + 2000, T0 + 3000])

    def test_delayed_deviation_never_steps_back(self):
        # The Codex sequence: a deviation at 899, the summary ending 900
        # (written when 901 arrived), 901, then a delayed check stamped 898
        # that the writer counted in the 900 bucket. The writer now puts its
        # ts at that bucket's start (event_ts keeps 898); a file from before
        # that carries ts 898, which the reader clamps the same way.
        late = dev(T0 + 900)
        late["event_ts"] = T0 + 898
        for name, delayed in (("clamped", late), ("old", dev(T0 + 898))):
            with self.subTest(name):
                p = self.write(name + ".jsonl", [dev(T0 + 899), summ(T0),
                                                 dev(T0 + 901), delayed])
                got = list(read_records([p]))
                keys = [eventreplay.order_key(r) for r in got]
                self.assertEqual(keys, sorted(keys))
                self.assertEqual([r.get("ts", r.get("bucket")) for r in got],
                                 [T0 + 899, T0, T0 + 900, T0 + 901])
                self.assertEqual(got[2]["event_ts"], T0 + 898)

    def test_restart_inside_bucket_orders_by_time(self):
        # Shutdown wrote the open bucket's summary (ordered at the bucket
        # end); the restart appended earlier-timestamped deviations of the
        # same bucket after it.
        p = self.write("a.jsonl", [dev(T0 + 10), summ(T0), dev(T0 + 500),
                                   dev(T0 + 600), dev(T0 + BUCKET + 5),
                                   summ(T0 + BUCKET)])
        got = list(read_records([p]))
        keys = [eventreplay.order_key(r) for r in got]
        self.assertEqual(keys, sorted(keys))
        self.assertEqual([r.get("ts", r.get("bucket")) for r in got],
                         [T0 + 10, T0 + 500, T0 + 600, T0,
                          T0 + BUCKET + 5, T0 + BUCKET])
        # --at inside the bucket sees every deviation up to the cutoff.
        seen = []
        for r in read_records([p]):
            if eventreplay.order_key(r)[0] > T0 + 550:
                break
            seen.append(r)
        self.assertEqual([r["ts"] for r in seen], [T0 + 10, T0 + 500])

    def test_bucket_start_beats_epoch_inference(self):
        # Codex: 7-minute buckets are aligned to Go's zero time, not the
        # Unix epoch. Rows at 11:55:02 then 11:54:58 are one writer bucket;
        # epoch alignment would put them in different buckets and move the
        # second to the later bucket's start.
        edge = (T0 // 420 + 1) * 420  # an epoch multiple of 420 s
        start = edge - 240  # the writer's bucket spans [edge-240, edge+180)
        hi, lo = edge + 2, edge - 2
        recs = [dev(hi), dev(lo)]
        for r in recs:
            r["bucket_s"], r["bucket_start"] = 420, start
        got = list(read_records([self.write("b7.jsonl", recs)]))
        self.assertEqual([r["ts"] for r in got], [lo, hi])
        self.assertTrue(all("event_ts" not in r for r in got))
        # --at between them sees the second row at its own time.
        seen = [r for r in got if eventreplay.order_key(r)[0] <= lo + 1]
        self.assertEqual([r["ts"] for r in seen], [lo])

    def test_shutdown_summary_sorts_at_flushed_at(self):
        # Fable: a 60-minute bucket shut down 10 minutes in writes its
        # summary at once; later 5-minute buckets must replay after it, not
        # after the long bucket's end.
        def tag(r, n, start):
            r["bucket_s"], r["bucket_start"] = n, start
            return r
        long_sum = summ(T0, n=2, ok=2)
        long_sum["bucket_seconds"] = long_sum["bucket_s"] = 3600
        long_sum["flushed_at"] = T0 + 600
        d1 = tag(dev(T0 + 1210, level="disagree"), 300, T0 + 1200)
        s1 = summ(T0 + 1200, n=1, ok=0)
        s1["bucket_seconds"] = s1["bucket_s"] = 300
        d2 = tag(dev(T0 + 1810, level="disagree"), 300, T0 + 1800)
        recs = [tag(dev(T0 + 100, level="disagree"), 3600, T0), long_sum,
                d1, s1, d2]
        got = list(read_records([self.write("shrink.jsonl", recs)]))
        keys = [eventreplay.order_key(r) for r in got]
        self.assertEqual(keys, sorted(keys))
        self.assertEqual(eventreplay.order_key(long_sum), (T0 + 600, 0))
        self.assertEqual(got.index(long_sum), 1)  # before both later buckets
        self.assertEqual(got[2:], [d1, s1, d2])

    def test_reads_truncated_gzip_and_partial_line(self):
        recs = [dev(T0 + i * 10) for i in range(200)]
        full = self.path("full.gz")
        self.write("full.gz", recs, gz=True)
        with open(full, "rb") as f:
            blob = f.read()
        cut = self.path("cut.gz")
        with open(cut, "wb") as f:
            f.write(blob[: len(blob) * 2 // 3])
        plain = self.write("plain.jsonl", [dev(T0 + 5), dev(T0 + 15)],
                           tail=b'{"kind": "deviation", "ts": 17')

        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            got = list(read_records([cut, plain]))
        warnings = [l for l in err.getvalue().splitlines() if l.strip()]
        self.assertEqual(len(warnings), 2, warnings)
        self.assertTrue(any("cut.gz" in w for w in warnings))
        self.assertTrue(any("plain.jsonl" in w for w in warnings))
        from_cut = [r for r in got if r["ts"] % 10 == 0]
        self.assertGreater(len(from_cut), 0)
        self.assertLess(len(from_cut), 200)
        self.assertEqual([r["ts"] for r in from_cut],
                         [T0 + i * 10 for i in range(len(from_cut))],
                         "every complete record before the cut is yielded")
        self.assertEqual([r["ts"] for r in got if r["ts"] % 10 == 5],
                         [T0 + 5, T0 + 15])


GOLDEN = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                      "testdata", "derive-events-golden.jsonl")


class GoldenTest(unittest.TestCase):
    """testdata/derive-events-golden.jsonl is real Go writer output
    (TestDeriveEventsGolden pins it byte for byte), so this checks the
    script reads the format the writer produces."""

    def test_golden_reads_and_feeds_detector(self):
        recs = list(read_records([GOLDEN]))
        # The shutdown summary's flushed_at equals the deviation's ts (the
        # test clock is fixed) and a summary sorts first at a tie.
        self.assertEqual([r["kind"] for r in recs], ["summary", "deviation"])
        s, d = recs
        # The deviation fields the detector reads.
        self.assertIsInstance(d["ts"], int)
        self.assertEqual(d["event_ts"], d["ts"])  # not delayed: unclamped
        self.assertEqual((d["pokemon_id"], d["form"], d["strong"],
                          d["utc_offset"]), (959, 3272, True, 60))
        self.assertEqual(d["agree"], {"ivs": "agree", "level": "disagree",
                                      "size": "agree", "quick": "agree",
                                      "charged": "disagree"})
        self.assertEqual(d["observed"]["level"] - d["derived"]["level"], 2)
        self.assertEqual((d["observed"]["size"], d["size_roll"]), (4, 0.9734))
        self.assertEqual((d["observed_move_1"], d["observed_move_2"]),
                         (350, 999))
        self.assertIsInstance(d["spawn_id"], str)  # opaque salted hash
        self.assertEqual(len(d["spawn_id"]), 12)
        # The summary fields it reads.
        self.assertEqual(s["bucket_seconds"], 900)
        self.assertEqual((d["bucket_s"], s["bucket_s"]), (900, 900))
        self.assertEqual(d["bucket_start"], s["bucket"])
        self.assertEqual(s["flushed_at"], 1784109780)  # the shutdown flush
        self.assertEqual(s["seen"], 1)
        self.assertEqual(eventreplay.order_key(s)[0], s["flushed_at"])
        for dim, ok in (("ivs", 2), ("level", 1), ("size", 2), ("quick", 2),
                        ("charged", 1)):
            self.assertEqual(s[dim], {"ok": ok, "n": 2}, dim)
        self.assertEqual(eventreplay._parse_rolls(s["size_rolls"]),
                         {4: (0.9734, 0.9801)})

        # One deviation per track: pending, below the start threshold.
        det = Detector(Params())
        self.assertEqual(feed_all(det, recs), [])
        det = Detector(Params(start=1, confirm=1))
        trs = feed_all(det, recs)
        self.assertEqual(
            sorted((t.modifier.type, json.dumps(t.modifier.effect,
                                                sort_keys=True))
                   for t in trs if t.state == "candidate"),
            [("level_offset", '{"delta": 2, "type": "level_offset"}'),
             ("move_pin",
              '{"move": 999, "slot": "charged", "type": "move_pin"}')])


if __name__ == "__main__":
    unittest.main()
