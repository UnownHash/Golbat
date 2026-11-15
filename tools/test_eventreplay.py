"""Tests for tools/eventreplay.py (reader and detector).

    python3 -m unittest discover -s tools -p 'test_*.py' -v
"""
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
from eventreplay import Detector, Params, read_records  # noqa: E402

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
        "weather": 0, "cell": "89c25c", "spawn_id": 1, "first_seen": ts - 60,
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


if __name__ == "__main__":
    unittest.main()
