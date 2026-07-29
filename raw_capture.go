package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// Raw payload capture for the protobench decode harness (Phase 0 of
// docs/superpowers/specs/2026-07-05-proto-decoding-gc-design.md).
// Samples are size-stratified per method so the corpus includes rare complex
// shapes. Disabled by default; when disabled the decode-path cost is one
// atomic load. The worker owns all quota state; the producer never blocks
// (channel full => drop + count, per the decode-path invariant in CLAUDE.md).

const captureBuckets = 5

type captureItem struct {
	method string
	data   []byte
}

type rawCaptureWorker struct {
	dir            string
	perBucketLimit int
	ch             chan captureItem
	drops          atomic.Int64
	writeFails     atomic.Int64
	counts         map[string]*[captureBuckets]int // worker-goroutine only
	stop           chan struct{}
}

var rawCaptureWorkerPtr atomic.Pointer[rawCaptureWorker]

// captureSizeBucket: <4KiB, <16KiB, <64KiB, <256KiB, >=256KiB.
func captureSizeBucket(size int) int {
	switch {
	case size < 4<<10:
		return 0
	case size < 16<<10:
		return 1
	case size < 64<<10:
		return 2
	case size < 256<<10:
		return 3
	default:
		return 4
	}
}

func captureFilename(method string, tsMs int64, size int) string {
	return filepath.Join(method, fmt.Sprintf("%d_%d.bin", tsMs, size))
}

// seedCaptureCounts restores per-bucket counts from files already on disk so
// capture can resume across restarts without exceeding quotas.
func seedCaptureCounts(dir string) (map[string]*[captureBuckets]int, error) {
	counts := make(map[string]*[captureBuckets]int)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		method := e.Name()
		files, err := os.ReadDir(filepath.Join(dir, method))
		if err != nil {
			return nil, err
		}
		c := &[captureBuckets]int{}
		for _, f := range files {
			name := f.Name()
			if !strings.HasSuffix(name, ".bin") {
				continue
			}
			// <unixMilli>_<size>.bin
			base := strings.TrimSuffix(name, ".bin")
			_, sizeStr, ok := strings.Cut(base, "_")
			if !ok {
				continue
			}
			size, err := strconv.Atoi(sizeStr)
			if err != nil {
				continue
			}
			c[captureSizeBucket(size)]++
		}
		counts[method] = c
	}
	return counts, nil
}

func startRawCapture(dir string, perBucketLimit int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("raw capture dir: %w", err)
	}
	counts, err := seedCaptureCounts(dir)
	if err != nil {
		return fmt.Errorf("raw capture seed: %w", err)
	}
	w := &rawCaptureWorker{
		dir:            dir,
		perBucketLimit: perBucketLimit,
		ch:             make(chan captureItem, 1024),
		counts:         counts,
		stop:           make(chan struct{}),
	}
	go w.run()
	rawCaptureWorkerPtr.Store(w)
	log.Infof("[RAW_CAPTURE] enabled, dir=%s limit=%d/bucket", dir, perBucketLimit)
	return nil
}

// CaptureRawPayload samples a raw proto payload. Cheap no-op when disabled.
func CaptureRawPayload(method string, data []byte) {
	w := rawCaptureWorkerPtr.Load()
	if w == nil {
		return
	}
	select {
	case w.ch <- captureItem{method: method, data: data}:
	default:
		if n := w.drops.Add(1); n%1000 == 1 {
			log.Warnf("[RAW_CAPTURE] channel full, dropped %d samples so far", n)
		}
	}
}

func (w *rawCaptureWorker) run() {
	for {
		select {
		case <-w.stop:
			return
		case item := <-w.ch:
			w.write(item)
		}
	}
}

func (w *rawCaptureWorker) write(item captureItem) {
	c := w.counts[item.method]
	if c == nil {
		c = &[captureBuckets]int{}
		w.counts[item.method] = c
	}
	bucket := captureSizeBucket(len(item.data))
	if c[bucket] >= w.perBucketLimit {
		return
	}
	// The worker is single-threaded, so filenames only collide when two
	// same-size payloads land in the same millisecond. Bump tsMs until the
	// path is free rather than silently overwriting (which would double-count
	// quota for one file on disk).
	tsMs := time.Now().UnixMilli()
	var path string
	const maxCollisionAttempts = 1000
	for attempt := 0; ; attempt++ {
		path = filepath.Join(w.dir, captureFilename(item.method, tsMs, len(item.data)))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		if attempt >= maxCollisionAttempts {
			log.Warnf("[RAW_CAPTURE] giving up on filename collision after %d attempts: %s", maxCollisionAttempts, path)
			return
		}
		tsMs++
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		if n := w.writeFails.Add(1); n%1000 == 1 {
			log.Warnf("[RAW_CAPTURE] mkdir: %v (%d write failures so far)", err, n)
		}
		return
	}
	if err := os.WriteFile(path, item.data, 0o644); err != nil {
		if n := w.writeFails.Add(1); n%1000 == 1 {
			log.Warnf("[RAW_CAPTURE] write: %v (%d write failures so far)", err, n)
		}
		return
	}
	c[bucket]++
}

// stopRawCaptureForTest disables capture and stops the worker (tests only).
func stopRawCaptureForTest() {
	if w := rawCaptureWorkerPtr.Swap(nil); w != nil {
		close(w.stop)
	}
}
