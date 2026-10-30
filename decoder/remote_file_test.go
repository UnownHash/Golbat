package decoder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A cache write that fails part way must leave the previous cache readable.
// It is replaced by rename, so the only states anyone can observe are the old
// file and the new one; no temp file is left behind either way.
func TestWriteFileAtomicLeavesTheOldFileOrTheNewOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "doc.json")

	if err := writeFileAtomic(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}

	// Make the rename's target unreplaceable: a directory where the file goes.
	blocked := filepath.Join(dir, "blocked.json")
	if err := os.MkdirAll(filepath.Join(blocked, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(blocked, []byte("new")); err == nil {
		t.Fatal("writing over a directory succeeded")
	}

	entries, _ := os.ReadDir(filepath.Join(dir, "sub"))
	for _, e := range entries {
		if e.Name() != "doc.json" {
			t.Errorf("left %s behind", e.Name())
		}
	}
	top, _ := os.ReadDir(dir)
	for _, e := range top {
		if e.Name() != "sub" && e.Name() != "blocked.json" {
			t.Errorf("left %s behind after a failed write", e.Name())
		}
	}
}

func TestRemoteFileWatcherStartsOnce(t *testing.T) {
	r := &remoteFile{name: "test", apply: func([]byte) error { return nil }}
	if err := r.watch(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := r.watch(time.Hour); err == nil {
		t.Error("a second watcher started")
	}
}
