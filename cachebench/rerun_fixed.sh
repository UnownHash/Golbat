#!/bin/bash
# Rerun of scenarios affected by measurement fixes (steady-window GC
# contamination; forced-GC churn) plus the new callback-bomb scenario.
set -u
cd "$(dirname "$0")"
OUT=results
N="${CACHEBENCH_N:-10000000}"
for c in ttlcache otter theine proto; do
  echo "wave $c"; env CACHEBENCH_SCENARIO=1 CACHEBENCH_N=$N CACHEBENCH_CANDIDATE=$c \
    go test -count=1 -run TestScenarioMassExpiryWave -v -timeout 40m . > "$OUT/wave_$c.txt" 2>&1
  echo "bomb $c"; env CACHEBENCH_SCENARIO=1 CACHEBENCH_CANDIDATE=$c \
    go test -count=1 -run TestScenarioCallbackBomb -v -timeout 20m . > "$OUT/bomb_$c.txt" 2>&1
  echo "mem $c"; env CACHEBENCH_SCENARIO=1 CACHEBENCH_N=$N CACHEBENCH_CANDIDATE=$c \
    go test -count=1 -run TestScenarioMemory -v -timeout 40m . > "$OUT/mem_$c.txt" 2>&1
done
echo done
