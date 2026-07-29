package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPartialCorpus(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ENCOUNTER"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ENCOUNTER", "1_4.bin"), []byte{1, 2, 3, 4}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ENCOUNTER", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err) // top-level file, not in a method dir: ignored
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["ENCOUNTER"]) != 1 {
		t.Fatalf("got %v", got)
	}
	p := got["ENCOUNTER"][0]
	if p.Method != "ENCOUNTER" || len(p.Data) != 4 {
		t.Fatalf("payload = %+v", p)
	}
}

func TestLoadEmptyAndMissing(t *testing.T) {
	empty := t.TempDir()
	got, err := Load(empty)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty dir: got %v, err %v", got, err)
	}
	if _, err := Load(filepath.Join(empty, "nope")); err == nil {
		t.Fatal("missing dir: want error")
	}
}
