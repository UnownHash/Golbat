# cachebench RESULTS

Hardware: **Apple M3 Pro, 12 cores, 36GB RAM, go1.26.3 darwin/arm64** (production: 48+ cores / 360GB — relative comparisons transfer, absolutes do not; 96-goroutine runs oversubscribe 12 cores).
Population: N=10,000,000 entries of ~1KB (pointer values) unless noted. Sources: raw logs under `results/`, appended at bottom.

## Benchmark 1 — read-hot (90% Get / 10% Set, zipf over 10M keys)

ns/op (lower is better); speedup vs ttlcache-hyst baseline in parens.

| candidate | g8 | g32 | g96 |
|---|---|---|---|
| ttlcache | 168.3 (1.05x) | 174.4 (1.00x) | 150.9 (1.10x) |
| ttlcache-hyst | 176.3 (1.00x) | 173.8 (1.00x) | 165.7 (1.00x) |
| otter | 63.4 (2.78x) | 53.6 (3.24x) | 50.0 (3.32x) |
| theine | 100.7 (1.75x) | 85.6 (2.03x) | 85.0 (1.95x) |
| proto | 42.4 (4.16x) | 56.2 (3.09x) | 81.5 (2.03x) |
| ttlcache@100shards | 163.1 (1.08x) | 130.3 (1.33x) | 95.5 (1.73x) |

## Benchmark 2 — touch cost isolation (100% Get, 96 goroutines)

| candidate | touch ns/op | notouch ns/op | touch mechanism |
|---|---|---|---|
| ttlcache | 211.6 | 155.1 | heap.Fix under shard lock per Get |
| ttlcache-hyst | 146.5 | — | hysteresis: re-Set only when remaining < TTL/4 (75b3df0) |
| otter | 30.2 | 24.4 | ExpireAfterRead via expiry calculator (timer wheel) |
| theine | 484.3 | 27.7 | EMULATED: full re-Set per Get (no native touch) |
| proto | 26.9 | 28.7 | single atomic deadline store |
| ttlcache@100shards | 132.1 | 85.3 | heap.Fix under shard lock per Get |

## Benchmark 4 — GetOrSetFunc storm (96 goroutines x same 1k keys)

| candidate | ns/op | single-winner (-race) |
|---|---|---|
| ttlcache | 112.7 | PASS (native GetOrSetFunc) |
| ttlcache-hyst | 102.4 | PASS (native GetOrSetFunc) |
| otter | 28.2 | PASS (ComputeIfAbsent) |
| theine | 14.7 | PASS via adapter striped-mutex EMULATION (no native equivalent) |
| proto | 23.9 | PASS (xsync.Map.Compute) |

## Benchmark 6 — single-goroutine consumer tax (encounterCache pattern, Get+Set pairs)

| candidate | ns/op (Get+Set+alloc) | implied events/s ceiling |
|---|---|---|
| ttlcache | 579 | ~1,728,011 |
| ttlcache-hyst | 530 | ~1,887,505 |
| otter | 536 | ~1,866,716 |
| theine | 820 | ~1,219,066 |
| proto | 456 | ~2,191,060 |

## Benchmark 3 — mass-expiry wave (10M entries expire under ~200k ops/s load)

| candidate | steady p99 Get | wave p99 Get | wave/steady | max goroutines | cb lag p50 / p99 | delivered/expected | GC pause p99 / max | GC CPU |
|---|---|---|---|---|---|---|---|---|
| otter | 3.708µs | 2.292µs | 0.6x | 39 | 1.024s / 2.048s | 10598736/10597764 | 114.688µs / 114.688µs | 0.2s |
| proto | 2.666µs | 2.041µs | 0.8x | 38 | 512ms / 1.024s | 10565194/10562344 | 262.144µs / 262.144µs | 0.3s |
| theine | 30.291µs | 23.541µs | 0.8x | 38 | 1.024s / 2.048s | 10558941/10552856 | 458.752µs / 458.752µs | 0.3s |
| ttlcache | 174.125µs | 11.709µs | 0.1x | 53 | 1ms / 2ms | 10606897/10605942 | 196.608µs / 196.608µs | 0.3s |

## Benchmark 5 — eviction callback throughput (1M short-TTL entries -> tree-writer channel)

| candidate | delivered | dropped | window | rate | max goroutines |
|---|---|---|---|---|---|
| otter | 1000000 | 0 | 9.054s | 110450/s | 20 |
| proto | 1000000 | 0 | 10.065s | 99353/s | 19 |
| theine | 1000000 | 0 | 8.727s | 114585/s | 19 |
| ttlcache | 1000000 | 0 | 9.235s | 108281/s | 200 |

## Benchmark 5b — goroutine bomb (blocked/slow callbacks during a 200k-entry expiry wave)

The ed50bf8 incident, reproduced. Arms: **stall** = consumer fully wedged for 10s then recovers (acute incident); **slow** = consumer sustainably slower than the expiry rate (100µs/event, chronic). `stall goroutines` = goroutine count while the consumer was down. `probe Set max` = worst writer latency observed (does the eviction pipeline backpressure cache writers?).

| candidate/arm | delivered | stall goroutines | drain after recovery | probe Set max |
|---|---|---|---|---|
| otter/stall | 200000/200000 | 19 | 250ms | 80µs |
| otter/slow | 200000/200000 | 19 | 17.002s | 42µs |
| proto/stall | 200000/200000 | 7 | 251ms | 101µs |
| proto/slow | 200000/200000 | 7 | 17.001s | 77µs |
| theine/stall | 200000/200000 | 18 | 1.256s | 76µs |
| theine/slow | 200000/200000 | 18 | 19.006s | 6.968271s |
| ttlcache/stall | 200000/200000 | 200017 | 251ms | 89.86ms |
| ttlcache/slow | 200000/200000 | 199448 | 0s | 6.747ms |

## Benchmark 7 — memory at 10M entries + full Range + GC churn

Churn = ~50k Sets/s of new 1KB entities for 20s against the 10M-entry live heap, with GOGC forced to 5 so cycles actually occur (at GOGC=100 this churn never triggers GC — itself a finding: large-heap deployments see infrequent but heavier cycles).

| candidate | bytes/entry | vs mapref | Range 10M | Range rate | churn GC cycles | churn GC pauses p99/max | churn GC CPU |
|---|---|---|---|---|---|---|---|
| mapref | 1054 | floor | 63ms | 159106248 entries/s | — | —/— | — |
| ttlcache | 1231 | +177B | 1.69s | 5915525 entries/s | 2 | 262.144µs/262.144µs | 5.8s |
| otter | 1116 | +62B | 1.521s | 6574854 entries/s | 2 | 163.84µs/163.84µs | 3.5s |
| theine | 1148 | +94B | 386ms | 25925363 entries/s | 2 | 163.84µs/163.84µs | 6.7s |
| proto | 1113 | +59B | 575ms | 17383409 entries/s | 2 | 229.376µs/229.376µs | 2.7s |

## Raw output

### bench_otter.txt
```
goos: darwin
goarch: arm64
pkg: golbat/cachebench
cpu: Apple M3 Pro
BenchmarkReadHot/otter/g8-12           	49304044	        63.43 ns/op	     108 B/op	       0 allocs/op
BenchmarkReadHot/otter/g32-12          	38202014	        53.64 ns/op	     108 B/op	       0 allocs/op
BenchmarkReadHot/otter/g96-12          	51972039	        49.97 ns/op	     108 B/op	       0 allocs/op
BenchmarkTouch/otter/touch/get-12      	77828475	        30.19 ns/op	       0 B/op	       0 allocs/op
BenchmarkTouch/otter/notouch/get-12    	92857993	        24.35 ns/op	       0 B/op	       0 allocs/op
BenchmarkGetOrSetStorm/otter-12        	100000000	        28.22 ns/op	      24 B/op	       1 allocs/op
BenchmarkSingleGoroutineConsumer/otter-12         	 4943000	       535.7 ns/op	    1108 B/op	       2 allocs/op
PASS
ok  	golbat/cachebench	53.604s
```

### bench_proto.txt
```
goos: darwin
goarch: arm64
pkg: golbat/cachebench
cpu: Apple M3 Pro
BenchmarkReadHot/proto/g8-12           	62304420	        42.43 ns/op	     108 B/op	       0 allocs/op
BenchmarkReadHot/proto/g32-12          	64110127	        56.17 ns/op	     110 B/op	       0 allocs/op
BenchmarkReadHot/proto/g96-12          	46260724	        81.55 ns/op	     109 B/op	       0 allocs/op
BenchmarkTouch/proto/touch/get-12      	100000000	        26.93 ns/op	       0 B/op	       0 allocs/op
BenchmarkTouch/proto/notouch/get-12    	100000000	        28.71 ns/op	       0 B/op	       0 allocs/op
BenchmarkGetOrSetStorm/proto-12        	100000000	        23.95 ns/op	      24 B/op	       1 allocs/op
BenchmarkSingleGoroutineConsumer/proto-12         	 5342182	       456.4 ns/op	    1139 B/op	       3 allocs/op
PASS
ok  	golbat/cachebench	51.941s
```

### bench_theine.txt
```
goos: darwin
goarch: arm64
pkg: golbat/cachebench
cpu: Apple M3 Pro
BenchmarkReadHot/theine/g8-12          	21700856	       100.7 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/theine/g32-12         	23604312	        85.63 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/theine/g96-12         	29593716	        85.04 ns/op	     102 B/op	       0 allocs/op
BenchmarkTouch/theine/touch/get-12     	 4935525	       484.3 ns/op	       0 B/op	       0 allocs/op
BenchmarkTouch/theine/notouch/get-12   	84943348	        27.69 ns/op	       0 B/op	       0 allocs/op
BenchmarkGetOrSetStorm/theine-12       	159612810	        14.72 ns/op	      24 B/op	       1 allocs/op
BenchmarkSingleGoroutineConsumer/theine-12         	 2999176	       820.3 ns/op	    1146 B/op	       2 allocs/op
PASS
ok  	golbat/cachebench	53.878s
```

### bench_ttlcache-hyst.txt
```
goos: darwin
goarch: arm64
pkg: golbat/cachebench
cpu: Apple M3 Pro
BenchmarkReadHot/ttlcache-hyst/g8-12   	14500298	       176.3 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/ttlcache-hyst/g32-12  	14468176	       173.8 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/ttlcache-hyst/g96-12  	14959938	       165.7 ns/op	     102 B/op	       0 allocs/op
BenchmarkTouch/ttlcache-hyst/hysteresis/get-12     	14956881	       146.5 ns/op	       0 B/op	       0 allocs/op
BenchmarkGetOrSetStorm/ttlcache-hyst-12            	23166879	       102.4 ns/op	      48 B/op	       2 allocs/op
BenchmarkSingleGoroutineConsumer/ttlcache-hyst-12  	 4650648	       529.8 ns/op	    1225 B/op	       3 allocs/op
PASS
ok  	golbat/cachebench	42.449s
```

### bench_ttlcache.txt
```
goos: darwin
goarch: arm64
pkg: golbat/cachebench
cpu: Apple M3 Pro
BenchmarkReadHot/ttlcache/g8-12        	15237486	       168.3 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/ttlcache/g32-12       	12417805	       174.4 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/ttlcache/g96-12       	14706376	       150.9 ns/op	     102 B/op	       0 allocs/op
BenchmarkTouch/ttlcache/touch/get-12   	11728413	       211.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkTouch/ttlcache/notouch/get-12 	15282964	       155.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkGetOrSetStorm/ttlcache-12     	21149815	       112.7 ns/op	      48 B/op	       2 allocs/op
BenchmarkSingleGoroutineConsumer/ttlcache-12         	 4508037	       578.7 ns/op	    1228 B/op	       3 allocs/op
PASS
ok  	golbat/cachebench	51.084s
```

### bench_ttlcache_100shards.txt
```
goos: darwin
goarch: arm64
pkg: golbat/cachebench
cpu: Apple M3 Pro
BenchmarkReadHot/ttlcache/g8-12      	15580684	       163.1 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/ttlcache/g32-12     	19513575	       130.3 ns/op	     102 B/op	       0 allocs/op
BenchmarkReadHot/ttlcache/g96-12     	23857754	        95.53 ns/op	     102 B/op	       0 allocs/op
BenchmarkTouch/ttlcache/touch/get-12 	15798498	       132.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkTouch/ttlcache/notouch/get-12         	32303907	        85.26 ns/op	       0 B/op	       0 allocs/op
PASS
ok  	golbat/cachebench	45.251s
```

### bomb_otter.txt
```
=== RUN   TestScenarioCallbackBomb
=== RUN   TestScenarioCallbackBomb/otter/stall
    scenario_test.go:469: RESULT cbbomb/otter/stall delivered=200000 of=200000 stall_goroutines=19 max_goroutines=19 drain_after_recovery=250ms probe_set_max=80µs
=== RUN   TestScenarioCallbackBomb/otter/slow
    scenario_test.go:469: RESULT cbbomb/otter/slow delivered=200000 of=200000 stall_goroutines=19 max_goroutines=19 drain_after_recovery=17.002s probe_set_max=42µs
--- PASS: TestScenarioCallbackBomb (37.38s)
    --- PASS: TestScenarioCallbackBomb/otter/stall (10.31s)
    --- PASS: TestScenarioCallbackBomb/otter/slow (27.07s)
PASS
ok  	golbat/cachebench	37.573s
```

### bomb_proto.txt
```
=== RUN   TestScenarioCallbackBomb
=== RUN   TestScenarioCallbackBomb/proto/stall
    scenario_test.go:469: RESULT cbbomb/proto/stall delivered=200000 of=200000 stall_goroutines=7 max_goroutines=7 drain_after_recovery=251ms probe_set_max=101µs
=== RUN   TestScenarioCallbackBomb/proto/slow
    scenario_test.go:469: RESULT cbbomb/proto/slow delivered=200000 of=200000 stall_goroutines=7 max_goroutines=7 drain_after_recovery=17.001s probe_set_max=77µs
--- PASS: TestScenarioCallbackBomb (37.32s)
    --- PASS: TestScenarioCallbackBomb/proto/stall (10.29s)
    --- PASS: TestScenarioCallbackBomb/proto/slow (27.03s)
PASS
ok  	golbat/cachebench	37.557s
```

### bomb_theine.txt
```
=== RUN   TestScenarioCallbackBomb
=== RUN   TestScenarioCallbackBomb/theine/stall
    scenario_test.go:469: RESULT cbbomb/theine/stall delivered=200000 of=200000 stall_goroutines=18 max_goroutines=18 drain_after_recovery=1.256s probe_set_max=76µs
=== RUN   TestScenarioCallbackBomb/theine/slow
    scenario_test.go:469: RESULT cbbomb/theine/slow delivered=200000 of=200000 stall_goroutines=18 max_goroutines=18 drain_after_recovery=19.006s probe_set_max=6.968271s
--- PASS: TestScenarioCallbackBomb (40.45s)
    --- PASS: TestScenarioCallbackBomb/theine/stall (11.35s)
    --- PASS: TestScenarioCallbackBomb/theine/slow (29.10s)
PASS
ok  	golbat/cachebench	40.685s
```

### bomb_ttlcache.txt
```
=== RUN   TestScenarioCallbackBomb
=== RUN   TestScenarioCallbackBomb/ttlcache/stall
    scenario_test.go:469: RESULT cbbomb/ttlcache/stall delivered=200000 of=200000 stall_goroutines=200017 max_goroutines=200017 drain_after_recovery=251ms probe_set_max=89.86ms
=== RUN   TestScenarioCallbackBomb/ttlcache/slow
    scenario_test.go:469: RESULT cbbomb/ttlcache/slow delivered=200000 of=200000 stall_goroutines=199448 max_goroutines=199448 drain_after_recovery=0s probe_set_max=6.747ms
--- PASS: TestScenarioCallbackBomb (20.48s)
    --- PASS: TestScenarioCallbackBomb/ttlcache/stall (10.38s)
    --- PASS: TestScenarioCallbackBomb/ttlcache/slow (10.11s)
PASS
ok  	golbat/cachebench	20.822s
```

### cb_otter.txt
```
=== RUN   TestScenarioCallbackThroughput
=== RUN   TestScenarioCallbackThroughput/otter
    scenario_test.go:344: RESULT cbthroughput/otter delivered=1000000 dropped=0 of=1000000 window=9.054s rate=110450/s max_goroutines=20
--- PASS: TestScenarioCallbackThroughput (12.15s)
    --- PASS: TestScenarioCallbackThroughput/otter (12.15s)
PASS
ok  	golbat/cachebench	12.345s
```

### cb_proto.txt
```
=== RUN   TestScenarioCallbackThroughput
=== RUN   TestScenarioCallbackThroughput/proto
    scenario_test.go:344: RESULT cbthroughput/proto delivered=1000000 dropped=0 of=1000000 window=10.065s rate=99353/s max_goroutines=19
--- PASS: TestScenarioCallbackThroughput (12.21s)
    --- PASS: TestScenarioCallbackThroughput/proto (12.21s)
PASS
ok  	golbat/cachebench	12.408s
```

### cb_theine.txt
```
=== RUN   TestScenarioCallbackThroughput
=== RUN   TestScenarioCallbackThroughput/theine
    scenario_test.go:344: RESULT cbthroughput/theine delivered=1000000 dropped=0 of=1000000 window=8.727s rate=114585/s max_goroutines=19
--- PASS: TestScenarioCallbackThroughput (11.91s)
    --- PASS: TestScenarioCallbackThroughput/theine (11.91s)
PASS
ok  	golbat/cachebench	12.137s
```

### cb_ttlcache.txt
```
=== RUN   TestScenarioCallbackThroughput
=== RUN   TestScenarioCallbackThroughput/ttlcache
    scenario_test.go:344: RESULT cbthroughput/ttlcache delivered=1000000 dropped=0 of=1000000 window=9.235s rate=108281/s max_goroutines=200
--- PASS: TestScenarioCallbackThroughput (11.26s)
    --- PASS: TestScenarioCallbackThroughput/ttlcache (11.26s)
PASS
ok  	golbat/cachebench	11.656s
```

### correctness.txt
```
=== RUN   TestSingleWinnerAllCandidates
=== RUN   TestSingleWinnerAllCandidates/ttlcache
=== RUN   TestSingleWinnerAllCandidates/ttlcache-hyst
=== RUN   TestSingleWinnerAllCandidates/otter
=== RUN   TestSingleWinnerAllCandidates/theine
=== RUN   TestSingleWinnerAllCandidates/proto
--- PASS: TestSingleWinnerAllCandidates (0.27s)
    --- PASS: TestSingleWinnerAllCandidates/ttlcache (0.06s)
    --- PASS: TestSingleWinnerAllCandidates/ttlcache-hyst (0.06s)
    --- PASS: TestSingleWinnerAllCandidates/otter (0.05s)
    --- PASS: TestSingleWinnerAllCandidates/theine (0.04s)
    --- PASS: TestSingleWinnerAllCandidates/proto (0.07s)
=== RUN   TestReplacementFiresNoEviction
=== RUN   TestReplacementFiresNoEviction/ttlcache
=== RUN   TestReplacementFiresNoEviction/ttlcache-hyst
=== RUN   TestReplacementFiresNoEviction/otter
=== RUN   TestReplacementFiresNoEviction/theine
=== RUN   TestReplacementFiresNoEviction/proto
--- PASS: TestReplacementFiresNoEviction (10.06s)
    --- PASS: TestReplacementFiresNoEviction/ttlcache (2.01s)
    --- PASS: TestReplacementFiresNoEviction/ttlcache-hyst (2.01s)
    --- PASS: TestReplacementFiresNoEviction/otter (2.00s)
    --- PASS: TestReplacementFiresNoEviction/theine (2.01s)
    --- PASS: TestReplacementFiresNoEviction/proto (2.01s)
=== RUN   TestEvictReasonMapping
=== RUN   TestEvictReasonMapping/ttlcache
=== RUN   TestEvictReasonMapping/ttlcache-hyst
=== RUN   TestEvictReasonMapping/otter
=== RUN   TestEvictReasonMapping/theine
=== RUN   TestEvictReasonMapping/proto
--- PASS: TestEvictReasonMapping (8.61s)
    --- PASS: TestEvictReasonMapping/ttlcache (1.50s)
    --- PASS: TestEvictReasonMapping/ttlcache-hyst (1.50s)
    --- PASS: TestEvictReasonMapping/otter (2.00s)
    --- PASS: TestEvictReasonMapping/theine (2.00s)
    --- PASS: TestEvictReasonMapping/proto (1.60s)
=== RUN   TestHysteresisKeepsHotEntriesResident
--- PASS: TestHysteresisKeepsHotEntriesResident (6.06s)
PASS
ok  	golbat/cachebench	26.312s
```

### mem_mapref.txt
```
=== RUN   TestScenarioMemory
=== RUN   TestScenarioMemory/mapref
    scenario_test.go:402: RESULT memory/mapref entries=10000000 heap_delta=9.82GiB bytes_per_entry=1054
    scenario_test.go:419: RESULT range/mapref visited=10000000 in=63ms rate=159106248 entries/s
--- PASS: TestScenarioMemory (2.23s)
    --- PASS: TestScenarioMemory/mapref (2.23s)
PASS
ok  	golbat/cachebench	2.497s
```

### mem_otter.txt
```
=== RUN   TestScenarioMemory
=== RUN   TestScenarioMemory/otter
    scenario_test.go:495: RESULT memory/otter entries=10000000 heap_delta=10.39GiB bytes_per_entry=1116
    scenario_test.go:512: RESULT range/otter visited=10000000 in=1.521s rate=6574854 entries/s
    scenario_test.go:548: RESULT churn/otter gc_cycles=2 gc_pauses=4 gc_pause_p50=4.096µs gc_pause_p99=163.84µs gc_pause_max=163.84µs gc_cpu_delta=3.5s (GOGC=5 forced)
--- PASS: TestScenarioMemory (24.63s)
    --- PASS: TestScenarioMemory/otter (24.63s)
PASS
ok  	golbat/cachebench	24.952s
```

### mem_proto.txt
```
=== RUN   TestScenarioMemory
=== RUN   TestScenarioMemory/proto
    scenario_test.go:495: RESULT memory/proto entries=10000000 heap_delta=10.37GiB bytes_per_entry=1113
    scenario_test.go:512: RESULT range/proto visited=10000000 in=575ms rate=17383409 entries/s
    scenario_test.go:548: RESULT churn/proto gc_cycles=2 gc_pauses=4 gc_pause_p50=5.12µs gc_pause_p99=229.376µs gc_pause_max=229.376µs gc_cpu_delta=2.7s (GOGC=5 forced)
--- PASS: TestScenarioMemory (22.76s)
    --- PASS: TestScenarioMemory/proto (22.76s)
PASS
ok  	golbat/cachebench	23.100s
```

### mem_theine.txt
```
=== RUN   TestScenarioMemory
=== RUN   TestScenarioMemory/theine
    scenario_test.go:495: RESULT memory/theine entries=10000000 heap_delta=10.69GiB bytes_per_entry=1148
    scenario_test.go:512: RESULT range/theine visited=10000000 in=386ms rate=25925363 entries/s
    scenario_test.go:548: RESULT churn/theine gc_cycles=2 gc_pauses=4 gc_pause_p50=16.384µs gc_pause_p99=163.84µs gc_pause_max=163.84µs gc_cpu_delta=6.7s (GOGC=5 forced)
--- PASS: TestScenarioMemory (24.87s)
    --- PASS: TestScenarioMemory/theine (24.87s)
PASS
ok  	golbat/cachebench	25.233s
```

### mem_ttlcache.txt
```
=== RUN   TestScenarioMemory
=== RUN   TestScenarioMemory/ttlcache
    scenario_test.go:495: RESULT memory/ttlcache entries=10000000 heap_delta=11.47GiB bytes_per_entry=1231
    scenario_test.go:512: RESULT range/ttlcache visited=10000000 in=1.69s rate=5915525 entries/s
    scenario_test.go:548: RESULT churn/ttlcache gc_cycles=2 gc_pauses=4 gc_pause_p50=40.96µs gc_pause_p99=262.144µs gc_pause_max=262.144µs gc_cpu_delta=5.8s (GOGC=5 forced)
--- PASS: TestScenarioMemory (24.66s)
    --- PASS: TestScenarioMemory/ttlcache (24.66s)
PASS
ok  	golbat/cachebench	24.994s
```

### wave_otter.txt
```
=== RUN   TestScenarioMassExpiryWave
=== RUN   TestScenarioMassExpiryWave/otter
    scenario_test.go:160: preloading...
    scenario_test.go:166: preload of 10000000 entries took 2.892s
    scenario_test.go:260: RESULT wave/otter steady_get_p50=709ns steady_get_p99=3.708µs wave_get_p50=375ns wave_get_p99=2.292µs wave_vs_steady_p99=0.6x
    scenario_test.go:262: RESULT wave/otter max_goroutines=39 callbacks_delivered=10598736 expected~10597764 lag_p50=1.024s lag_p99=2.048s lag_max=2.012104412s
    scenario_test.go:264: RESULT wave/otter gc_pauses=2 gc_pause_p50=5.12µs gc_pause_p99=114.688µs gc_pause_max=114.688µs gc_cpu_delta=0.2s
--- PASS: TestScenarioMassExpiryWave (158.53s)
    --- PASS: TestScenarioMassExpiryWave/otter (158.53s)
PASS
ok  	golbat/cachebench	158.797s
```

### wave_proto.txt
```
=== RUN   TestScenarioMassExpiryWave
=== RUN   TestScenarioMassExpiryWave/proto
    scenario_test.go:160: preloading...
    scenario_test.go:166: preload of 10000000 entries took 1.694s
    scenario_test.go:260: RESULT wave/proto steady_get_p50=875ns steady_get_p99=2.666µs wave_get_p50=292ns wave_get_p99=2.041µs wave_vs_steady_p99=0.8x
    scenario_test.go:262: RESULT wave/proto max_goroutines=38 callbacks_delivered=10565194 expected~10562344 lag_p50=512ms lag_p99=1.024s lag_max=1.148649s
    scenario_test.go:264: RESULT wave/proto gc_pauses=2 gc_pause_p50=8.192µs gc_pause_p99=262.144µs gc_pause_max=262.144µs gc_cpu_delta=0.3s
--- PASS: TestScenarioMassExpiryWave (157.36s)
    --- PASS: TestScenarioMassExpiryWave/proto (157.36s)
PASS
ok  	golbat/cachebench	157.716s
```

### wave_theine.txt
```
=== RUN   TestScenarioMassExpiryWave
=== RUN   TestScenarioMassExpiryWave/theine
    scenario_test.go:160: preloading...
    scenario_test.go:166: preload of 10000000 entries took 4.143s
    scenario_test.go:260: RESULT wave/theine steady_get_p50=1.166µs steady_get_p99=30.291µs wave_get_p50=833ns wave_get_p99=23.541µs wave_vs_steady_p99=0.8x
    scenario_test.go:262: RESULT wave/theine max_goroutines=38 callbacks_delivered=10558941 expected~10552856 lag_p50=1.024s lag_p99=2.048s lag_max=2.029244936s
    scenario_test.go:264: RESULT wave/theine gc_pauses=2 gc_pause_p50=5.12µs gc_pause_p99=458.752µs gc_pause_max=458.752µs gc_cpu_delta=0.3s
--- PASS: TestScenarioMassExpiryWave (160.32s)
    --- PASS: TestScenarioMassExpiryWave/theine (160.32s)
PASS
ok  	golbat/cachebench	160.632s
```

### wave_ttlcache.txt
```
=== RUN   TestScenarioMassExpiryWave
=== RUN   TestScenarioMassExpiryWave/ttlcache
    scenario_test.go:160: preloading...
    scenario_test.go:166: preload of 10000000 entries took 2.905s
    scenario_test.go:260: RESULT wave/ttlcache steady_get_p50=2.334µs steady_get_p99=174.125µs wave_get_p50=417ns wave_get_p99=11.709µs wave_vs_steady_p99=0.1x
    scenario_test.go:262: RESULT wave/ttlcache max_goroutines=53 callbacks_delivered=10606897 expected~10605942 lag_p50=1ms lag_p99=2ms lag_max=26.051ms
    scenario_test.go:264: RESULT wave/ttlcache gc_pauses=2 gc_pause_p50=8.192µs gc_pause_p99=196.608µs gc_pause_max=196.608µs gc_cpu_delta=0.3s
--- PASS: TestScenarioMassExpiryWave (158.01s)
    --- PASS: TestScenarioMassExpiryWave/ttlcache (158.01s)
PASS
ok  	golbat/cachebench	158.416s
```

