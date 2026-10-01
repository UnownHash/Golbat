#!/usr/bin/env python3
"""Summarise golbat [DERIVE] validation rows.

    python3 tools/derivecheck.py logs/*.log logs/*.log.gz
    zcat logs/old.gz | python3 tools/derivecheck.py
    python3 tools/derivecheck.py --json logs/*.gz > summary.json
    python3 tools/derivecheck.py --pool cache/ditto-pool.json logs/*.gz

Ditto rows are scored against a disguise pool: the one --pool names, else
cache/ditto-pool.json in the current directory (golbat's cached copy of the
published pool), else the pool bundled in the source tree. Rows logged under
an older pool are then judged by the current one. With no pool file found,
each row's logged ditto_pool_member is used instead.

Reads any line containing [DERIVE]; plain text and gzip both work, and a
mixture of the two in one invocation works. Rows stream through single-pass
accumulators, so a year of logs costs no more memory than a day.

The branch classification is computed here from derived_unboosted,
derived_boosted and the observed values rather than read from `ditto_branch`,
which has no size_only class.

Adding a check: give Analysis a new accumulator in __init__, update it in
add(), and print it from report(). Nothing else needs to change.
"""
import sys, os, json, gzip, glob, collections

IV = ("attack", "defense", "stamina", "level", "size")
WEATHER = {0: "NONE", 1: "CLEAR", 2: "RAINY", 3: "PARTLY_CLOUDY",
           4: "OVERCAST", 5: "WINDY", 6: "SNOW", 7: "FOG"}
THRESHOLD = 0.02          # nextFloat() < 0.02f — see decoder/ditto_roll.go
SAMPLE_CAP = 8            # examples retained per anomaly class


def open_any(path):
    """Plain or gzipped, decided by content rather than by extension."""
    with open(path, "rb") as probe:
        magic = probe.read(2)
    if magic == b"\x1f\x8b":
        return gzip.open(path, "rt", errors="replace")
    return open(path, "rt", errors="replace")


def rows_from(paths):
    # Opens one file at a time: a rotated-log glob can exceed the process's
    # file-descriptor limit if every file is opened up front.
    for path in paths or [None]:
        stream = sys.stdin if path is None else open_any(path)
        with stream:
            for line in stream:
                if "[DERIVE]" not in line:
                    continue
                brace = line.find("{")
                if brace < 0:
                    continue
                try:
                    yield json.loads(line[brace:])
                except json.JSONDecodeError:
                    pass


def load_pool(path):
    """The set of (species, form) disguises in a pool file, as golbat
    publishes and caches it."""
    with open(path) as f:
        doc = json.load(f)
    return {(e["pokemon_id"], e["form"]) for e in doc["ditto_disguises"]["species"]}


def find_pool(explicit):
    """(pool, description). An explicit path must load; the defaults are
    tried in order and skipped when absent."""
    if explicit:
        try:
            return load_pool(explicit), explicit
        except (OSError, ValueError, KeyError, TypeError) as err:
            sys.exit(f"--pool {explicit}: {err}")
    here = os.path.dirname(os.path.abspath(__file__))
    for candidate in ("cache/ditto-pool.json", os.path.join(here, "..", "pogo", "ditto-pool.json")):
        if os.path.isfile(candidate):
            return load_pool(candidate), os.path.normpath(candidate)
    return None, "as logged (no pool file found)"


def disguise(row):
    return (row.get("displayed_species"), row.get("form"))


def observed(row):
    return tuple(row.get("observed_" + k) for k in IV)


def derived(row, boosted):
    src = row.get("derived_boosted" if boosted else "derived_unboosted") or {}
    return tuple(src.get(k) for k in IV)


SIZE_UNSET = 0  # HoloPokemonSize POKEMON_SIZE_UNSET: not reported, not a size


def comparable(observed, derived):
    """Compare a row's observed values against a derived branch.

    A size of 0 is POKEMON_SIZE_UNSET -- the encounter did not report one --
    so it is excluded, matching the decoder, which leaves it out of its own
    mismatch comparison for the same reason. Counting it as a real size makes
    an otherwise exact row degrade to size_only, and an opposite-branch row
    that degrades that way disappears from unknown-disguise discovery.
    """
    if observed[4] == SIZE_UNSET:
        return observed[:4] == derived[:4]
    return observed == derived


def classify_branch(row):
    """expected | opposite | size_only | neither | unknown.

    'opposite' is the Ditto indicator: a Ditto is Normal-typed and boosted
    only by PARTLY_CLOUDY, but the game reports weather_boosted_condition
    for the DISGUISE, so a boosted Ditto wearing an unboosted disguise
    lands in the other branch.
    """
    if row.get("is_strong"):
        return "unknown"
    obs = observed(row)
    if None in obs:
        return "unknown"
    boosted = bool(row.get("weather_boosted_condition"))
    if comparable(obs, derived(row, boosted)):
        return "expected"
    if comparable(obs, derived(row, not boosted)):
        return "opposite"
    # "IVs and level exact, size one class off" means the walk is right and
    # only the size classification differs — seen during a size event, which
    # moves the cut points for the species it covers. "Nothing matches" would
    # mean the walk itself is wrong. Conflating them hides the second behind
    # the first; size_only_by shows which species a run of them belongs to.
    for b in (boosted, not boosted):
        if obs[:4] == derived(row, b)[:4]:
            return "size_only"
    return "neither"


def detect_state(row):
    """detectDitto's verdict, with our own predictions held apart from it.

    ditto_detect reads "confirmed" from pokemon.IsDitto alone, and a roll-mark
    sets IsDitto. Counting those as confirmations would let the roll rule
    confirm itself: every mark it made would arrive back as a true positive
    and precision would be 100% by construction, whatever the rule did.

    ditto_roll_marked carries the provenance, so a confirmed row that is also
    roll-marked is reported as "roll_only" — a prediction still waiting on
    independent evidence. A row confirmed WITHOUT the mark is detectDitto's
    own verdict from cross-boost history, and is the only kind that can
    support an accuracy claim.

    This is conservative in one direction: a roll-marked record that
    detectDitto LATER confirms keeps its mark, so it stays in roll_only and
    the true-positive count is a floor rather than an estimate.
    """
    detect = row.get("ditto_detect", "(field absent)")
    if detect == "confirmed" and row.get("ditto_roll_marked"):
        return "roll_only"
    return detect


def bar(n, total, width=28):
    filled = 0 if not total else int(round(width * n / total))
    return "#" * filled + "." * (width - filled)


def pct(n, d):
    return 0.0 if not d else 100.0 * n / d


class Analysis:
    def __init__(self, pool=None, pool_source="as logged"):
        # The disguise pool rows are judged by; None means each row's own
        # logged ditto_pool_member.
        self.pool = pool
        self.pool_source = pool_source
        # The disagreements between the roll and detectDitto, per disguise:
        # marks detectDitto refuted, and Dittos it confirmed that the roll did
        # not mark. Each holds the count, the logged membership seen, and the
        # rolls, so the report can say whether the current pool fixes them.
        self.refuted = collections.defaultdict(lambda: {"n": 0, "logged": set(), "rolls": [], "ids": []})
        self.missed = collections.defaultdict(lambda: {"n": 0, "logged": set(), "rolls": [], "ids": []})
        # Roll-marks never resolved either way, of disguises the pool in use
        # does not list: marks the current pool would no longer make.
        self.unresolved_outside = collections.Counter()
        self.total = 0
        self.paths = collections.Counter()
        self.branch = collections.Counter()
        self.moves_ok = self.moves_bad = 0
        self.quick_ok = self.charged_ok = self.moves_comparable = 0
        self.move_shape = collections.Counter()
        self.move_bad_by = collections.Counter()
        self.cell = collections.Counter()          # (roll fired, detect) -> n
        self.ditto_rolls = []                      # rolls of INDEPENDENTLY known Dittos
        self.nonditto_min = None
        self.nonditto_below = 0
        self.rolls_roll_only = 0                   # this rule's own marks, held apart
        self.roll_deciles = collections.Counter()
        self.rolls_seen = 0
        self.wbc = collections.Counter()
        self.pc_disguise_boosted = self.pc_total = 0
        self.opposite_in_pool = collections.Counter()
        self.opposite_out_pool = collections.Counter()
        self.opposite_low_roll = self.opposite_total = 0
        self.samples = collections.defaultdict(list)
        # Every size-only row by species and form. The samples above keep only
        # SAMPLE_CAP rows each, which is enough to show what one looks like but
        # not where the rest are; a size rule the model lacks shows up here as
        # one species holding them all.
        self.size_only_by = collections.Counter()
        # Why each row was logged. Its presence marks a disagreements-only
        # corpus and switches the report to that mode, where agreements are
        # absent and ratios over them are meaningless.
        self.logged_because = collections.Counter()
        self.disagreements_only = False

    def in_pool(self, row):
        if self.pool is None:
            return bool(row.get("ditto_pool_member"))
        return disguise(row) in self.pool

    def _anomaly_total(self, key):
        return self.moves_bad if key == "moves" else self.branch[key]

    def sample(self, key, row):
        if len(self.samples[key]) < SAMPLE_CAP:
            self.samples[key].append(row)

    def add(self, row):
        self.total += 1
        reasons = row.get("logged_because")
        if reasons is not None:
            self.disagreements_only = True
            for r in reasons:
                self.logged_because[r] += 1
        self.paths[row.get("path")] += 1

        branch = classify_branch(row)
        self.branch[branch] += 1
        if branch in ("opposite", "neither", "size_only"):
            self.sample(branch, row)
        if branch == "size_only":
            self.size_only_by[(row.get("displayed_species"), row.get("form"))] += 1

        d1, d2 = row.get("derived_move_1"), row.get("derived_move_2")
        o1, o2 = row.get("observed_move_1"), row.get("observed_move_2")
        # The decoder emits BOTH derived moves as 0 when it could not resolve
        # the species' move pools — an unknown species, or a masterfile that
        # has not loaded. That is "no opinion", exactly as checkDerivation
        # treats it, so the row is not comparable.
        #
        # The observed side has the same convention from the other direction:
        # the log carries the raw proto values, and an encounter that omits
        # its moves reports both as 0. Counting the row here would score two
        # real predictions against nothing and call both wrong, so the row is
        # comparable only when the observed side has both.
        if None not in (d1, d2, o1, o2) and not (d1 == 0 and d2 == 0) and o1 != 0 and o2 != 0:
            self.moves_comparable += 1
            if d1 == o1:
                self.quick_ok += 1
            if d2 == o2:
                self.charged_ok += 1
            if (d1, d2) == (o1, o2):
                self.moves_ok += 1
                self.move_shape["both correct"] += 1
            else:
                self.moves_bad += 1
                shape = ("charged wrong only" if d1 == o1 else
                         "quick wrong only" if d2 == o2 else "both wrong")
                self.move_shape[shape] += 1
                self.move_bad_by[(row.get("displayed_species"), o1, o2)] += 1
                self.sample("moves", row)

        member = self.in_pool(row)
        fired = bool(row.get("ditto_suspect_roll"))
        state = detect_state(row)
        if member:
            self.cell[(fired, state)] += 1
        roll = row.get("ditto_roll")
        if fired and row.get("ditto_pool_member") and state == "rejected":
            entry = self.refuted[disguise(row)]
        elif state == "confirmed" and not (fired and member):
            entry = self.missed[disguise(row)]
        else:
            entry = None
        if state == "roll_only" and not member:
            self.unresolved_outside[disguise(row)] += 1
        if entry is not None:
            entry["n"] += 1
            entry["logged"].add(bool(row.get("ditto_pool_member")))
            if roll is not None:
                entry["rolls"].append(roll)
            if len(entry["ids"]) < SAMPLE_CAP:
                entry["ids"].append(row.get("encounter_id"))

        if roll is not None:
            self.rolls_seen += 1
            self.roll_deciles[min(int(roll * 10), 9)] += 1
            # is_ditto alone cannot decide this. A roll-mark sets it, and a
            # roll-mark only happens BELOW the threshold, so counting those as
            # known Dittos would let the cut validate itself: "every known
            # Ditto is below 0.02" would be true by construction from a single
            # roll-only row, while section 2 correctly reports that nothing has
            # been independently resolved. detect_state carries the provenance.
            #
            # A roll-only row is not a known non-Ditto either — it is unresolved
            # — so it belongs in neither bucket and is counted on its own.
            #
            # A known non-Ditto is one detectDitto rejected, of a disguise in
            # the pool: the roll means nothing for any other species, so a low
            # roll there says nothing about the cut.
            if state == "roll_only":
                self.rolls_roll_only += 1
            elif row.get("is_ditto"):
                self.ditto_rolls.append(roll)
            elif state == "rejected" and member:
                if self.nonditto_min is None or roll < self.nonditto_min:
                    self.nonditto_min = roll
                if roll < THRESHOLD:
                    self.nonditto_below += 1

        wbc = row.get("weather_boosted_condition")
        self.wbc[wbc] += 1
        if wbc == 3:
            self.pc_total += 1
            if (row.get("disguise_boosted_weathers") or 0) & (1 << 3):
                self.pc_disguise_boosted += 1

        if branch == "opposite":
            self.opposite_total += 1
            key = (row.get("displayed_species"), row.get("form"))
            if row.get("ditto_pool_member"):
                self.opposite_in_pool[key] += 1
            else:
                self.opposite_out_pool[key] += 1
                self.sample("unknown_disguise", row)
            if (roll or 1.0) < THRESHOLD:
                self.opposite_low_roll += 1

    # ---------------------------------------------------------------- report
    def report(self):
        rule = "=" * 72
        print(f"\n{rule}\n  {self.total:,} [DERIVE] rows\n{rule}")
        print("  paths: " + "  ".join(f"{k}={v:,}" for k, v in self.paths.most_common()))
        print(f"  ditto pool: {self.pool_source}" + (f" ({len(self.pool)} disguises)" if self.pool is not None else ""))
        if self.disagreements_only:
            print("  logged because: " + "  ".join(f"{k}={v:,}" for k, v in self.logged_because.most_common()))
            print("  This corpus holds DISAGREEMENTS ONLY: agreeing encounters are counted in the")
            print("  derivation_* metrics, not logged. Percentages below are of logged rows, and")
            print("  the roll's precision, recall and uniformity cannot be computed from it.")

        self._section("1. DERIVATION vs OBSERVED")
        comparable = sum(v for k, v in self.branch.items() if k != "unknown")
        for label, key in (("exact (expected branch)", "expected"),
                           ("OPPOSITE branch", "opposite"),
                           ("size only (IVs+level ok)", "size_only"),
                           ("neither branch", "neither")):
            n = self.branch[key]
            print(f"    {label:24} {n:7,}  {pct(n, comparable):6.2f}%  {bar(n, comparable)}")
        if self.branch["unknown"]:
            print(f"    {'not comparable':24} {self.branch['unknown']:7,}   (strong, or no observed IVs)")
        moves = self.moves_comparable
        if moves:
            print(f"\n    quick move   {self.quick_ok:7,} / {moves:,}   {pct(self.quick_ok, moves):6.2f}%")
            print(f"    charged move {self.charged_ok:7,} / {moves:,}   {pct(self.charged_ok, moves):6.2f}%")
            print(f"    both         {self.moves_ok:7,} / {moves:,}   {pct(self.moves_ok, moves):6.2f}%")
            if self.moves_bad:
                print("\n    mismatch shape:")
                for shape, n in self.move_shape.most_common():
                    if shape != "both correct":
                        print(f"        {shape:22} {n:7,}")
                print("\n    A move the derivation gets right in one slot and wrong in the"
                      "\n    other is the signature of a GRANTED move (event or legacy),"
                      "\n    not a broken draw: the walk stayed in step for the other slot.")
                print("\n    by species and observed pair:")
                for (sp, a, b), n in self.move_bad_by.most_common(10):
                    print(f"        species {sp:4}  observed {a:4}/{b:4}   x{n:,}")

        if self.disagreements_only:
            self._report_ditto_conflicts()
        else:
            self._report_ditto_full()
        self._report_ditto_disagreements()

        self._section("4. WEATHER REPORTING")
        for k, v in sorted(self.wbc.items(), key=lambda kv: (kv[0] is None, kv[0])):
            print(f"    wbc={str(k):4} {WEATHER.get(k, '?'):16} {v:7,}")
        if self.pc_total:
            not_pc = self.pc_total - self.pc_disguise_boosted
            print(f"\n    PARTLY_CLOUDY rows: {self.pc_total:,}; disguise PC-boosted in {self.pc_disguise_boosted:,}")
            if not_pc == 0:
                print("    -> the game reports the DISGUISE's boost. A type/boost comparison")
                print("       against it can never identify a Ditto; the branch check below can.")

        self._section("5. BRANCH SIGNAL  (finds Dittos, pool-blind)")
        print(f"    opposite-branch rows: {self.opposite_total:,}"
              f"   in pool {sum(self.opposite_in_pool.values()):,}"
              f"   OUTSIDE pool {sum(self.opposite_out_pool.values()):,}")
        if self.opposite_total:
            print(f"    rolled below threshold: {self.opposite_low_roll:,} "
                  f"({pct(self.opposite_low_roll, self.opposite_total):.1f}%)")
        if self.opposite_out_pool:
            print("\n    *** UNKNOWN DISGUISES — opposite branch, species not in ditto_pool ***")
            for (sp, form), n in self.opposite_out_pool.most_common(20):
                print(f"        species {sp:5} form {form:5}   x{n}")
            print("    Confirm in game before adding to ditto_pool: a wrong entry")
            print("    false-positives on ~2% of that species' encounters indefinitely.")
        elif self.opposite_total:
            print("    none outside the pool — consistent with ditto_pool being complete.")
        if self.opposite_in_pool:
            print("\n    in-pool, by species:")
            for (sp, form), n in self.opposite_in_pool.most_common(12):
                print(f"        species {sp:5} form {form:5}   x{n}")

        self._section("6. ANOMALIES")
        found = False
        if self.size_only_by:
            print(f"    size only, all {self.branch['size_only']:,} by species/form:")
            for (sp, form), n in self.size_only_by.most_common(12):
                print(f"        species {sp:5} form {form:5}   x{n:,}")
            if len(self.size_only_by) > 12:
                print(f"        ... and {len(self.size_only_by) - 12} more species/forms")
        for key, label in (("size_only", "size only"), ("neither", "neither branch"), ("moves", "move mismatch")):
            if self.samples[key]:
                shown, total = len(self.samples[key]), self._anomaly_total(key)
                more = f", {total - shown:,} more not listed" if total > shown else ""
                print(f"    {label}: showing {shown} of {total:,}{more}")
        for row in self.samples["size_only"]:
            found = True
            print(f"    SIZE ONLY       sp {row.get('displayed_species')} "
                  f"observed size={row.get('observed_size')} "
                  f"derived unboosted={derived(row, False)[4]} boosted={derived(row, True)[4]} "
                  f"id={row.get('encounter_id')}")
        for row in self.samples["neither"]:
            found = True
            print(f"    NEITHER BRANCH  sp {row.get('displayed_species')} "
                  f"obs={observed(row)} unboosted={derived(row, False)} "
                  f"boosted={derived(row, True)} id={row.get('encounter_id')}")
        for row in self.samples["moves"]:
            found = True
            print(f"    MOVE MISMATCH   sp {row.get('displayed_species')} "
                  f"derived={row.get('derived_move_1')}/{row.get('derived_move_2')} "
                  f"observed={row.get('observed_move_1')}/{row.get('observed_move_2')} "
                  f"id={row.get('encounter_id')}")
        if not found:
            print("    none.")
        print()

    def as_json(self):
        return {
            "rows": self.total,
            "paths": dict(self.paths),
            "branch": dict(self.branch),
            "size_only_by": {f"{sp}:{form}": n for (sp, form), n in self.size_only_by.most_common()},
            "moves": {"comparable": self.moves_comparable, "both_correct": self.moves_ok,
                      "quick_correct": self.quick_ok, "charged_correct": self.charged_ok,
                      "shape": dict(self.move_shape)},
            "ditto_2x2": {f"{'fired' if k[0] else 'not_fired'}/{k[1]}": v
                          for k, v in self.cell.items()},
            "threshold": {
                "known_ditto_max_roll": max(self.ditto_rolls) if self.ditto_rolls else None,
                "known_ditto_n": len(self.ditto_rolls),
                "non_ditto_min_roll": self.nonditto_min,
                "non_dittos_below_threshold": self.nonditto_below,
                "roll_only_excluded": self.rolls_roll_only,
            },
            "opposite_branch": {
                "total": self.opposite_total,
                "in_pool": sum(self.opposite_in_pool.values()),
                "out_of_pool": sum(self.opposite_out_pool.values()),
                "unknown_disguises": [{"species": sp, "form": fm, "count": n}
                                      for (sp, fm), n in self.opposite_out_pool.most_common()],
            },
            "weather_boosted_condition": {str(k): v for k, v in self.wbc.items()},
            "ditto_pool": self.pool_source,
            "ditto_refuted": [{"species": sp, "form": fm, "count": e["n"],
                               "in_pool_now": (sp, fm) in self.pool if self.pool is not None else None}
                              for (sp, fm), e in self.refuted.items()],
            "ditto_missed": [{"species": sp, "form": fm, "count": e["n"],
                              "in_pool_now": (sp, fm) in self.pool if self.pool is not None else None}
                             for (sp, fm), e in self.missed.items()],
        }

    def _report_ditto_conflicts(self):
        self._section("2. DITTO: conflicts between the roll, detectDitto and the stats")
        kinds = (("roll_refuted", "roll marked, detectDitto refuted"),
                 ("roll_missed_confirmed", "detectDitto confirmed, roll did not fire"),
                 ("roll_missed_opposite", "stats show a Ditto, roll did not fire"))
        for key, text in kinds:
            print(f"    {text:44} {self.logged_because[key]:7,}")
        if not any(self.logged_because[k] for k, _ in kinds):
            print("\n    none: the roll, detectDitto and the stats agreed on every encounter.")
        self._section(f"3. THRESHOLD  (rule: nextFloat() < {THRESHOLD})")
        if self.ditto_rolls:
            print(f"    confirmed Dittos logged: {len(self.ditto_rolls):,}, rolls "
                  f"{min(self.ditto_rolls):.7f} .. {max(self.ditto_rolls):.7f}")
            if max(self.ditto_rolls) >= THRESHOLD:
                print(f"    -> *** a confirmed Ditto rolled >= {THRESHOLD} — the rule missed it ***")
        else:
            print("    no confirmed Ditto logged, so none rolled above the threshold.")

    def _report_ditto_full(self):
        self._section("2. DITTO: roll rule vs detectDitto  (pool species only)")
        states = ["confirmed", "rejected", "no_evidence", "roll_only"]
        print(f"    {'':16}" + "".join(f"{s:>14}" for s in states))
        for fired in (True, False):
            label = "roll fired" if fired else "roll did not"
            print(f"    {label:16}" + "".join(f"{self.cell[(fired, s)]:>14,}" for s in states))
        tp, fp = self.cell[(True, "confirmed")], self.cell[(True, "rejected")]
        fn, tn = self.cell[(False, "confirmed")], self.cell[(False, "rejected")]
        resolved = tp + fp + fn + tn
        if resolved:
            print(f"\n    independently resolved by detectDitto: {resolved:,}")
            print("      (the roll_only column is this rule's own marks, not evidence about it)")
            if tp + fp:
                print(f"      precision  {pct(tp, tp + fp):6.2f}%   ({tp} correct of {tp + fp} marked)")
            if tp + fn:
                print(f"      recall     {pct(tp, tp + fn):6.2f}%   ({tp} found of {tp + fn} real)")
            if fp:
                print(f"      *** {fp} FALSE POSITIVE(S) — roll marked, detectDitto refuted ***")
            if fn:
                print(f"      *** {fn} FALSE NEGATIVE(S) — real Ditto the roll missed ***")
        else:
            print("\n    nothing INDEPENDENTLY resolved yet. detectDitto needs two sightings")
            print("    in different weather; until it has them, a confirmed row that is also")
            print("    roll-marked is this rule agreeing with itself, so the error rate is")
            print("    not yet measurable.")

        self._section(f"3. THRESHOLD  (rule: nextFloat() < {THRESHOLD})")
        if self.ditto_rolls:
            print(f"    known Dittos      n={len(self.ditto_rolls):5,}   max roll {max(self.ditto_rolls):.7f}"
                  "   (detectDitto-confirmed only)")
        else:
            print("    known Dittos      n=    0   — nothing independently confirmed, so the cut")
            print("                                 cannot be checked yet")
        if self.nonditto_min is not None:
            print(f"    non-Dittos                    min roll {self.nonditto_min:.7f}")
        if self.ditto_rolls:
            hi = max(self.ditto_rolls)
            if hi < THRESHOLD:
                print(f"    -> every known Ditto is below {THRESHOLD}: the cut holds.")
            else:
                print(f"    -> *** a known Ditto rolled {hi:.7f} >= {THRESHOLD} — rule missed it ***")
        print(f"    non-Dittos below threshold: {self.nonditto_below:,}   (candidates not yet confirmed)")
        if self.rolls_roll_only:
            print(f"    roll-only rows excluded:    {self.rolls_roll_only:,}   (this rule's own marks — "
                  "below the cut by construction, so they cannot test it)")
        if self.rolls_seen >= 200:
            expect = self.rolls_seen / 10
            worst = max(abs(self.roll_deciles[i] - expect) for i in range(10))
            verdict = "fine" if worst < 4 * expect ** 0.5 else "SKEWED, investigate"
            print(f"    uniformity: {self.rolls_seen:,} rolls, worst decile {pct(worst, expect):.1f}% off flat  ({verdict})")

    def _report_ditto_disagreements(self):
        """Each disguise the roll and detectDitto disagreed on, and whether
        the pool in use settles it."""
        self._section("3a. DITTO DISAGREEMENTS, by disguise")
        if self.unresolved_outside:
            listed = "  ".join(f"{sp}:{fm} x{n:,}" for (sp, fm), n in self.unresolved_outside.most_common())
            print(f"  roll-marks never resolved, of disguises not in the pool in use: "
                  f"{sum(self.unresolved_outside.values()):,} ({listed}) — the current pool would not make them.")
        if not self.refuted and not self.missed:
            print("    none: the roll and detectDitto agreed on every resolved encounter.")
            return

        def logged(e):
            return "/".join(sorted("in" if m else "out" for m in e["logged"])) or "?"

        def rolls(e):
            return f"{min(e['rolls']):.5f}..{max(e['rolls']):.5f}" if e["rolls"] else "-"

        header = f"    {'species':>7} {'form':>5} {'count':>6}  {'pool when logged':16} {'rolls':17} verdict"
        if self.refuted:
            print("  roll fired, detectDitto rejected:")
            print(header)
            for (sp, fm), e in sorted(self.refuted.items(), key=lambda kv: -kv[1]["n"]):
                now = (sp, fm) in self.pool if self.pool is not None else True
                verdict = ("fixed: no longer in the pool" if not now
                           else "STILL IN THE POOL: would still be marked")
                print(f"    {sp:>7} {fm:>5} {e['n']:>6,}  {logged(e):16} {rolls(e):17} {verdict}")
                print(f"{'':20}encounters: {' '.join(str(i) for i in e['ids'])}")
        if self.missed:
            print("  confirmed by detectDitto, roll did not fire:")
            print(header)
            for (sp, fm), e in sorted(self.missed.items(), key=lambda kv: -kv[1]["n"]):
                now = (sp, fm) in self.pool if self.pool is not None else False
                above = any(r >= THRESHOLD for r in e["rolls"])
                if above:
                    verdict = "ROLL ABOVE THE CUT: the rule missed it"
                elif now:
                    verdict = "fixed: now in the pool"
                else:
                    verdict = "NOT IN THE POOL: a disguise to add"
                print(f"    {sp:>7} {fm:>5} {e['n']:>6,}  {logged(e):16} {rolls(e):17} {verdict}")
                print(f"{'':20}encounters: {' '.join(str(i) for i in e['ids'])}")

    @staticmethod
    def _section(title):
        print(f"\n{'-' * 72}\n  {title}\n{'-' * 72}")


def main():
    argv = sys.argv[1:]
    want_json = "--json" in argv
    pool_path = None
    args = []
    i = 0
    while i < len(argv):
        if argv[i] == "--json":
            pass
        elif argv[i] == "--pool":
            if i + 1 >= len(argv):
                sys.exit("--pool needs a path")
            pool_path = argv[i + 1]
            i += 1
        else:
            args.append(argv[i])
        i += 1
    pool, pool_source = find_pool(pool_path)

    paths = []
    for arg in args:
        expanded = sorted(glob.glob(arg)) if any(c in arg for c in "*?[") else [arg]
        paths.extend(p for p in expanded if os.path.isfile(p))
    if args and not paths:
        sys.exit("no readable files matched")

    analysis = Analysis(pool, pool_source)
    for row in rows_from(paths):
        analysis.add(row)
    if not analysis.total:
        sys.exit("no [DERIVE] rows found")

    if want_json:
        json.dump(analysis.as_json(), sys.stdout, indent=2)
        print()
    else:
        analysis.report()


if __name__ == "__main__":
    main()
