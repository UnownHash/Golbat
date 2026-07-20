#!/bin/bash
# Official full-scale benchmark run for the cache investigation.
# One candidate per process for the 10M-entry cases: a 10M x ~1KB population
# is ~11GB and the 36GB test host cannot hold two candidates at once.
#
# Usage: ./run.sh [results_dir]   (default cachebench/results)
set -uo pipefail
cd "$(dirname "$0")"
OUT="${1:-results}"
mkdir -p "$OUT"
N="${CACHEBENCH_N:-10000000}"
echo "== cachebench full run: N=$N, results in $OUT =="

run() { # run <logfile> <env...> -- <go test args...>
  local log="$OUT/$1"; shift
  local envs=()
  while [ "$1" != "--" ]; do envs+=("$1"); shift; done
  shift
  echo "-> ${envs[*]} go test $* (log: $log)"
  env "${envs[@]}" go test "$@" >"$log" 2>&1
  local rc=$?
  [ $rc -ne 0 ] && echo "   FAILED rc=$rc (see $log)"
  return 0
}

# 0. Correctness (race-verified single-winner + eviction semantics), all candidates.
run correctness.txt CACHEBENCH_N=$N -- -race -count=1 \
  -run 'TestSingleWinner|TestReplacement|TestEvictReason|TestHysteresis' -v -timeout 20m .

# 1. Benchmarks 1/2/4/6 per candidate (process isolation).
for c in ttlcache ttlcache-hyst otter theine proto; do
  run "bench_$c.txt" CACHEBENCH_N=$N CACHEBENCH_CANDIDATE=$c -- \
    -bench . -benchmem -benchtime 2s -count=1 -timeout 120m .
done

# 1b. Shard-count sensitivity: ttlcache at production-like 100 shards.
run bench_ttlcache_100shards.txt CACHEBENCH_N=$N CACHEBENCH_CANDIDATE=ttlcache CACHEBENCH_SHARDS=100 -- \
  -bench 'BenchmarkReadHot|BenchmarkTouch' -benchmem -benchtime 2s -count=1 -timeout 120m .

# 3. Mass-expiry wave per candidate.
for c in ttlcache otter theine proto; do
  run "wave_$c.txt" CACHEBENCH_SCENARIO=1 CACHEBENCH_N=$N CACHEBENCH_CANDIDATE=$c -- \
    -count=1 -run TestScenarioMassExpiryWave -v -timeout 40m .
done

# 5. Callback throughput per candidate (1M short-TTL entries).
for c in ttlcache otter theine proto; do
  run "cb_$c.txt" CACHEBENCH_SCENARIO=1 CACHEBENCH_CANDIDATE=$c -- \
    -count=1 -run TestScenarioCallbackThroughput -v -timeout 20m .
done

# 5b. Goroutine bomb: blocking callbacks under mass expiry (the ed50bf8 incident shape).
for c in ttlcache otter theine proto; do
  run "bomb_$c.txt" CACHEBENCH_SCENARIO=1 CACHEBENCH_CANDIDATE=$c -- \
    -count=1 -run TestScenarioCallbackBomb -v -timeout 20m .
done

# 7. Memory + Range + GC churn per candidate, plus the plain-map floor.
for c in ttlcache otter theine proto mapref; do
  run "mem_$c.txt" CACHEBENCH_SCENARIO=1 CACHEBENCH_N=$N CACHEBENCH_CANDIDATE=$c -- \
    -count=1 -run TestScenarioMemory -v -timeout 40m .
done

echo "== done =="
