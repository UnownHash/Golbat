#!/usr/bin/env python3
"""Assemble RESULTS.md from results/*.txt produced by run.sh."""
import glob
import os
import re
import sys

RESULTS = sys.argv[1] if len(sys.argv) > 1 else "results"
CANDIDATES = ["ttlcache", "ttlcache-hyst", "otter", "theine", "proto"]

bench_re = re.compile(
    r"^(Benchmark\S+?)-\d+\s+(\d+)\s+([\d.]+) ns/op(?:\s+(\d+) B/op\s+(\d+) allocs/op)?"
)

def parse_bench_lines():
    rows = {}
    for path in sorted(glob.glob(f"{RESULTS}/bench_*.txt")):
        shards100 = "100shards" in path
        for line in open(path):
            m = bench_re.match(line.strip())
            if not m:
                continue
            name = m.group(1)
            if shards100:
                name = name.replace("/ttlcache/", "/ttlcache@100shards/")
            rows[name] = {"ns_op": float(m.group(3)), "b_op": m.group(4), "allocs": m.group(5)}
    return rows

def result_lines(prefix):
    out = {}
    for path in sorted(glob.glob(f"{RESULTS}/*.txt")):
        for line in open(path):
            m = re.search(rf"RESULT {prefix}/(\S+) (.*)", line)
            if m:
                out.setdefault(m.group(1), []).append(m.group(2).strip())
    return out

def kv(s):
    d = dict(p.split("=", 1) for p in s.split() if "=" in p)
    m = re.search(r"expected~(\d+)", s)
    if m:
        d["expected"] = m.group(1)
    return d

def fmt_speedup(base_ns, ns):
    return f"{base_ns / ns:.2f}x" if ns else "—"

benches = parse_bench_lines()

def bench_ns(bench, cand, sub=""):
    key = f"Benchmark{bench}/{cand}{sub}"
    return benches.get(key, {}).get("ns_op")

lines = []
A = lines.append
A("# cachebench RESULTS")
A("")
A("Hardware: **Apple M3 Pro, 12 cores, 36GB RAM, go1.26.3 darwin/arm64** "
  "(production: 48+ cores / 360GB — relative comparisons transfer, absolutes do not; "
  "96-goroutine runs oversubscribe 12 cores).")
A(f"Population: N={os.environ.get('CACHEBENCH_N', '10,000,000')} entries of ~1KB (pointer values) "
  "unless noted. Sources: raw logs under `results/`, appended at bottom.")
A("")

# --- Benchmark 1
A("## Benchmark 1 — read-hot (90% Get / 10% Set, zipf over 10M keys)")
A("")
A("ns/op (lower is better); speedup vs ttlcache-hyst baseline in parens.")
A("")
A("| candidate | g8 | g32 | g96 |")
A("|---|---|---|---|")
for c in CANDIDATES + ["ttlcache@100shards"]:
    cells = []
    for g in (8, 32, 96):
        ns = bench_ns("ReadHot", c, f"/g{g}")
        base = bench_ns("ReadHot", "ttlcache-hyst", f"/g{g}")
        cells.append(f"{ns:.1f} ({fmt_speedup(base, ns)})" if ns else "—")
    A(f"| {c} | " + " | ".join(cells) + " |")
A("")

# --- Benchmark 2
A("## Benchmark 2 — touch cost isolation (100% Get, 96 goroutines)")
A("")
A("| candidate | touch ns/op | notouch ns/op | touch mechanism |")
A("|---|---|---|---|")
mech = {
    "ttlcache": "heap.Fix under shard lock per Get",
    "ttlcache-hyst": "hysteresis: re-Set only when remaining < TTL/4 (75b3df0)",
    "otter": "ExpireAfterRead via expiry calculator (timer wheel)",
    "theine": "EMULATED: full re-Set per Get (no native touch)",
    "proto": "single atomic deadline store",
}
for c in CANDIDATES + ["ttlcache@100shards"]:
    t = bench_ns("Touch", c, "/hysteresis/get") or bench_ns("Touch", c, "/touch/get")
    nt = bench_ns("Touch", c, "/notouch/get")
    A(f"| {c} | {t:.1f}" if t else f"| {c} | —", )
    lines[-1] += f" | {nt:.1f}" if nt else " | —"
    lines[-1] += f" | {mech.get(c.split('@')[0], '')} |"
A("")

# --- Benchmark 4
A("## Benchmark 4 — GetOrSetFunc storm (96 goroutines x same 1k keys)")
A("")
A("| candidate | ns/op | single-winner (-race) |")
A("|---|---|---|")
sw = {
    "ttlcache": "PASS (native GetOrSetFunc)",
    "ttlcache-hyst": "PASS (native GetOrSetFunc)",
    "otter": "PASS (ComputeIfAbsent)",
    "theine": "PASS via adapter striped-mutex EMULATION (no native equivalent)",
    "proto": "PASS (xsync.Map.Compute)",
}
for c in CANDIDATES:
    ns = bench_ns("GetOrSetStorm", c)
    A(f"| {c} | {ns:.1f} | {sw[c]} |" if ns else f"| {c} | — | {sw[c]} |")
A("")

# --- Benchmark 6
A("## Benchmark 6 — single-goroutine consumer tax (encounterCache pattern, Get+Set pairs)")
A("")
A("| candidate | ns/op (Get+Set+alloc) | implied events/s ceiling |")
A("|---|---|---|")
for c in CANDIDATES:
    ns = bench_ns("SingleGoroutineConsumer", c)
    if ns:
        A(f"| {c} | {ns:.0f} | ~{1e9 / ns:,.0f} |")
    else:
        A(f"| {c} | — | — |")
A("")

# --- Benchmark 3 (wave)
A("## Benchmark 3 — mass-expiry wave (10M entries expire under ~200k ops/s load)")
A("")
A("| candidate | steady p99 Get | wave p99 Get | wave/steady | max goroutines | cb lag p50 / p99 | delivered/expected | GC pause p99 / max | GC CPU |")
A("|---|---|---|---|---|---|---|---|---|")
wave = result_lines("wave")
for c, entries in wave.items():
    d = {}
    for e in entries:
        d.update(kv(e))
    A("| {c} | {sp99} | {wp99} | {ratio} | {mg} | {l50} / {l99} | {dv}/{ex} | {gp99} / {gmax} | {gcpu} |".format(
        c=c, sp99=d.get("steady_get_p99", "—"), wp99=d.get("wave_get_p99", "—"),
        ratio=d.get("wave_vs_steady_p99", "—"), mg=d.get("max_goroutines", "—"),
        l50=d.get("lag_p50", "—"), l99=d.get("lag_p99", "—"),
        dv=d.get("callbacks_delivered", "—"), ex=d.get("expected~", d.get("expected", "—")),
        gp99=d.get("gc_pause_p99", "—"), gmax=d.get("gc_pause_max", "—"),
        gcpu=d.get("gc_cpu_delta", "—")))
A("")

# --- Benchmark 5
A("## Benchmark 5 — eviction callback throughput (1M short-TTL entries -> tree-writer channel)")
A("")
A("| candidate | delivered | dropped | window | rate | max goroutines |")
A("|---|---|---|---|---|---|")
for c, entries in result_lines("cbthroughput").items():
    d = kv(entries[0])
    A(f"| {c} | {d.get('delivered')} | {d.get('dropped')} | {d.get('window')} | {d.get('rate')} | {d.get('max_goroutines')} |")
A("")

# --- Benchmark 5b (bomb)
A("## Benchmark 5b — goroutine bomb (blocked/slow callbacks during a 200k-entry expiry wave)")
A("")
A("The ed50bf8 incident, reproduced. Arms: **stall** = consumer fully wedged for 10s then recovers "
  "(acute incident); **slow** = consumer sustainably slower than the expiry rate (100µs/event, chronic). "
  "`stall goroutines` = goroutine count while the consumer was down. "
  "`probe Set max` = worst writer latency observed (does the eviction pipeline backpressure cache writers?).")
A("")
A("| candidate/arm | delivered | stall goroutines | drain after recovery | probe Set max |")
A("|---|---|---|---|---|")
for c, entries in result_lines("cbbomb").items():
    d = kv(entries[0])
    A(f"| {c} | {d.get('delivered')}/{d.get('of')} | {d.get('stall_goroutines')} | {d.get('drain_after_recovery')} | {d.get('probe_set_max')} |")
A("")

# --- Benchmark 7
A("## Benchmark 7 — memory at 10M entries + full Range + GC churn")
A("")
A("Churn = ~50k Sets/s of new 1KB entities for 20s against the 10M-entry live heap, with GOGC forced to 5 so cycles actually occur (at GOGC=100 this churn never triggers GC — itself a finding: large-heap deployments see infrequent but heavier cycles).")
A("")
A("| candidate | bytes/entry | vs mapref | Range 10M | Range rate | churn GC cycles | churn GC pauses p99/max | churn GC CPU |")
A("|---|---|---|---|---|---|---|---|")
mem = result_lines("memory")
rng = result_lines("range")
churn = result_lines("churn")
mapref_bpe = None
if "mapref" in mem:
    mapref_bpe = float(kv(mem["mapref"][0]).get("bytes_per_entry", 0))
for c in ["mapref"] + [x for x in CANDIDATES if x != "ttlcache-hyst"]:
    if c not in mem:
        continue
    m = kv(mem[c][0])
    r = kv(rng[c][0]) if c in rng else {}
    ch = kv(churn[c][0]) if c in churn else {}
    bpe = float(m.get("bytes_per_entry", 0))
    vs = f"+{bpe - mapref_bpe:.0f}B" if mapref_bpe and c != "mapref" else "floor"
    A(f"| {c} | {bpe:.0f} | {vs} | {r.get('in', '—')} | {r.get('rate', '—')} {'' if not r else 'entries/s'} | "
      f"{ch.get('gc_cycles', '—')} | {ch.get('gc_pause_p99', '—')}/{ch.get('gc_pause_max', '—')} | {ch.get('gc_cpu_delta', '—')} |")
A("")

A("## Raw output")
A("")
for path in sorted(glob.glob(f"{RESULTS}/*.txt")):
    if "run-console" in path:
        continue
    A(f"### {os.path.basename(path)}")
    A("```")
    A(open(path).read().rstrip())
    A("```")
    A("")

print("\n".join(lines))
