package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureSizeBucket(t *testing.T) {
	cases := []struct {
		size int
		want int
	}{
		{0, 0}, {4095, 0}, {4096, 1}, {16383, 1}, {16384, 2},
		{65535, 2}, {65536, 3}, {262143, 3}, {262144, 4}, {5 << 20, 4},
	}
	for _, c := range cases {
		if got := captureSizeBucket(c.size); got != c.want {
			t.Errorf("captureSizeBucket(%d) = %d, want %d", c.size, got, c.want)
		}
	}
}

func TestCaptureFilename(t *testing.T) {
	got := captureFilename("GET_MAP_OBJECTS", 1751800000000, 12345)
	want := filepath.Join("GET_MAP_OBJECTS", "1751800000000_12345.bin")
	if got != want {
		t.Errorf("captureFilename = %q, want %q", got, want)
	}
}

func TestSeedCaptureCounts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "ENCOUNTER")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// two files in bucket 0 (<4KiB), one in bucket 2 (16-64KiB), one junk file
	for _, name := range []string{"1_100.bin", "2_200.bin", "3_20000.bin", "junk.txt"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := seedCaptureCounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := counts["ENCOUNTER"]
	if got == nil {
		t.Fatal("no counts for ENCOUNTER")
	}
	if got[0] != 2 || got[1] != 0 || got[2] != 1 {
		t.Errorf("counts = %v, want [2 0 1 0 0]", *got)
	}
}

func TestCaptureWorkerWritesAndEnforcesQuota(t *testing.T) {
	dir := t.TempDir()
	if err := startRawCapture(dir, 1); err != nil { // 1 per bucket
		t.Fatal(err)
	}
	defer stopRawCaptureForTest()

	// two tiny payloads, same bucket -> only the first is kept
	CaptureRawPayload("FORT_DETAILS", []byte("aaaa"))
	CaptureRawPayload("FORT_DETAILS", []byte("bbbb"))

	deadline := time.Now().Add(2 * time.Second)
	var files []os.DirEntry
	for time.Now().Before(deadline) {
		files, _ = os.ReadDir(filepath.Join(dir, "FORT_DETAILS"))
		if len(files) >= 1 {
			// give the worker a moment to (incorrectly) write a second file
			time.Sleep(50 * time.Millisecond)
			files, _ = os.ReadDir(filepath.Join(dir, "FORT_DETAILS"))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 (quota)", len(files))
	}
}

func TestCaptureDisabledIsNoop(t *testing.T) {
	rawCaptureWorkerPtr.Store(nil)
	CaptureRawPayload("ENCOUNTER", []byte("x")) // must not panic
}
