package decoder

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang/geo/s2"

	"golbat/derive/encounter"
)

// testClock is an injectable clock the writer reads through now().
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func newTestDeriveEventsWriter(t *testing.T, dir string, clock *testClock) *deriveEventsWriter {
	t.Helper()
	w, err := newDeriveEventsWriter(dir, 15*time.Minute, 14, clock.now)
	if err != nil {
		t.Fatalf("newDeriveEventsWriter: %v", err)
	}
	return w
}

// agreeingEventCheck is a check that agrees on every dimension, at ts.
func agreeingEventCheck(ts time.Time) deriveEventCheck {
	v := encounter.Values{Attack: 13, Defense: 13, Stamina: 15, Level: 38, Size: 4}
	return deriveEventCheck{
		Time:        ts,
		EncounterID: 15812345678901234567,
		PokemonID:   959,
		Form:        3272,
		Weather:     5,
		Strong:      true,
		Lat:         51.5,
		Lon:         -0.12,
		Account:     "SecretTrainerName",
		Check: derivationCheck{
			Observed: v, Derived: v, Opposite: encounter.Values{Attack: 15, Defense: 15, Stamina: 15, Level: 43, Size: 3},
			SizeRoll: 0.9734,
			Moves:    derivedMoveResult{Move1: 350, Move2: 494, QuickPool: []int{350}, ChargedPool: []int{494, 268}, Resolved: true},
			Move1:    350, Move2: 494,
		},
		Agree: dimAgreement{IVs: dimAgree, Level: dimAgree, Size: dimAgree, Quick: dimAgree, Charged: dimAgree},
	}
}

// readEventLines parses every JSON line of a plain or gzipped events file.
func readEventLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var r interface{ Read([]byte) (int, error) } = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatalf("gzip %s: %v", path, err)
		}
		r = gz
	}
	var rows []map[string]any
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var row map[string]any
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return rows
}

func eventsFile(dir string, day time.Time) string {
	return filepath.Join(dir, "derive-events-"+day.UTC().Format("2006-01-02")+".jsonl")
}

func rowsOfKind(rows []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, r := range rows {
		if r["kind"] == kind {
			out = append(out, r)
		}
	}
	return out
}

func TestDeriveEventsWriterDeviationRow(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{time.Date(2026, 11, 15, 10, 3, 0, 0, time.UTC)}
	w := newTestDeriveEventsWriter(t, dir, clock)

	ev := agreeingEventCheck(clock.t)
	ev.Check.Move2 = 999 // outside the charged pool
	ev.Agree.Charged = dimDisagree
	w.handle(ev)
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	path := eventsFile(dir, clock.t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"lat"`, `"lon"`, "SecretTrainerName", "51.5", "-0.12"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("file contains %s:\n%s", banned, raw)
		}
	}
	devs := rowsOfKind(readEventLines(t, path), "deviation")
	if len(devs) != 1 {
		t.Fatalf("got %d deviation rows, want 1", len(devs))
	}
	d := devs[0]
	if d["move_2_in_pool"] != false || d["move_1_in_pool"] != true {
		t.Errorf("in_pool = %v/%v, want true/false", d["move_1_in_pool"], d["move_2_in_pool"])
	}
	if got := d["agree"].(map[string]any); got["charged"] != "disagree" || got["ivs"] != "agree" {
		t.Errorf("agree = %v", got)
	}
	if d["encounter_id"] != "15812345678901234567" {
		t.Errorf("encounter_id = %v", d["encounter_id"])
	}
	if d["observed_move_2"] != 999.0 || d["derived_move_2"] != 494.0 {
		t.Errorf("moves = %v / %v", d["observed_move_2"], d["derived_move_2"])
	}
	if obs := d["observed"].(map[string]any); obs["level"] != 38.0 || obs["size"] != 4.0 {
		t.Errorf("observed = %v", obs)
	}
	if acct, _ := d["account"].(string); len(acct) != 12 {
		t.Errorf("account = %q, want 12 hex chars", acct)
	}
	if d["ts"] != float64(clock.t.Unix()) || d["size_roll"] != 0.9734 {
		t.Errorf("ts/size_roll = %v/%v", d["ts"], d["size_roll"])
	}
}

func TestDeriveEventsWriterSummaryCounts(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{time.Date(2026, 11, 15, 10, 1, 0, 0, time.UTC)}
	w := newTestDeriveEventsWriter(t, dir, clock)

	for i, roll := range []float64{0.9712, 0.9958, 0.9800} {
		ev := agreeingEventCheck(clock.t.Add(time.Duration(i) * time.Minute))
		ev.Check.SizeRoll = roll
		w.handle(ev)
	}
	dev := agreeingEventCheck(clock.t.Add(4 * time.Minute))
	dev.Check.Observed.Level = 40
	dev.Check.Observed.Size = 5
	dev.Check.SizeRoll = 0.9991
	dev.Agree.Level = dimDisagree
	dev.Agree.Size = dimDisagree
	w.handle(dev)

	clock.t = clock.t.Add(15 * time.Minute)
	w.handle(agreeingEventCheck(clock.t)) // next bucket: flushes the first
	if err := w.close(); err != nil {
		t.Fatal(err)
	}

	sums := rowsOfKind(readEventLines(t, eventsFile(dir, clock.t)), "summary")
	if len(sums) != 2 {
		t.Fatalf("got %d summaries, want 2", len(sums))
	}
	s := sums[0]
	if s["bucket"] != float64(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC).Unix()) || s["bucket_seconds"] != 900.0 {
		t.Errorf("bucket = %v/%v", s["bucket"], s["bucket_seconds"])
	}
	if s["checked"] != 4.0 {
		t.Errorf("checked = %v, want 4", s["checked"])
	}
	if lv := s["level"].(map[string]any); lv["ok"] != 3.0 || lv["n"] != 4.0 {
		t.Errorf("level = %v, want ok 3 n 4", lv)
	}
	if iv := s["ivs"].(map[string]any); iv["ok"] != 4.0 || iv["n"] != 4.0 {
		t.Errorf("ivs = %v", iv)
	}
	rolls := s["size_rolls"].(map[string]any)
	if r4 := rolls["4"].([]any); r4[0] != 0.9712 || r4[1] != 0.9958 {
		t.Errorf("size_rolls[4] = %v", r4)
	}
	if r5 := rolls["5"].([]any); r5[0] != 0.9991 || r5[1] != 0.9991 {
		t.Errorf("size_rolls[5] = %v", r5)
	}
	if s["pokemon_id"] != 959.0 || s["form"] != 3272.0 || s["strong"] != true || s["boosted"] != false {
		t.Errorf("key = %v", s)
	}
	if sums[1]["checked"] != 1.0 {
		t.Errorf("second summary checked = %v", sums[1]["checked"])
	}
}

func TestDeriveEventsWriterTickFlushesIdleBucket(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{time.Date(2026, 11, 15, 10, 1, 0, 0, time.UTC)}
	w := newTestDeriveEventsWriter(t, dir, clock)
	w.handle(agreeingEventCheck(clock.t))

	w.tick() // bucket not over yet
	if _, err := os.Stat(eventsFile(dir, clock.t)); err == nil {
		if rows := rowsOfKind(readEventLines(t, eventsFile(dir, clock.t)), "summary"); len(rows) != 0 {
			t.Fatalf("summary written before bucket end")
		}
	}
	clock.t = time.Date(2026, 11, 15, 10, 15, 0, 0, time.UTC)
	w.tick()
	if rows := rowsOfKind(readEventLines(t, eventsFile(dir, clock.t)), "summary"); len(rows) != 1 {
		t.Fatalf("got %d summaries after tick, want 1", len(rows))
	}
	_ = w.close()
}

func TestDeriveEventsWriterDayRollover(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{time.Date(2026, 11, 14, 23, 59, 0, 0, time.UTC)}
	w := newTestDeriveEventsWriter(t, dir, clock)
	yesterday := clock.t
	w.handle(deviatingEventCheck(clock.t))

	clock.t = time.Date(2026, 11, 15, 0, 1, 0, 0, time.UTC)
	w.handle(deviatingEventCheck(clock.t))
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	w.waitCompress()

	if _, err := os.Stat(eventsFile(dir, yesterday)); !os.IsNotExist(err) {
		t.Errorf("yesterday's plain file still exists (err %v)", err)
	}
	if rows := readEventLines(t, eventsFile(dir, yesterday)+".gz"); len(rows) == 0 {
		t.Errorf("yesterday's gzip is empty")
	}
	if _, err := os.Stat(eventsFile(dir, clock.t)); err != nil {
		t.Errorf("today's plain file: %v", err)
	}
}

func TestDeriveEventsWriterAppendsAndCompressesLeftovers(t *testing.T) {
	dir := t.TempDir()
	today := time.Date(2026, 11, 15, 10, 1, 0, 0, time.UTC)
	old := today.AddDate(0, 0, -3)
	if err := os.WriteFile(eventsFile(dir, today), []byte(`{"kind":"summary","checked":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventsFile(dir, old), []byte(`{"kind":"summary","checked":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	clock := &testClock{today}
	w := newTestDeriveEventsWriter(t, dir, clock)
	w.handle(agreeingEventCheck(today))
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	w.waitCompress()

	if rows := readEventLines(t, eventsFile(dir, today)); len(rows) < 2 {
		t.Errorf("today's file has %d lines, want 2+", len(rows))
	}
	if _, err := os.Stat(eventsFile(dir, old)); !os.IsNotExist(err) {
		t.Errorf("old plain file still exists (err %v)", err)
	}
	if rows := readEventLines(t, eventsFile(dir, old)+".gz"); len(rows) != 1 || rows[0]["checked"] != 2.0 {
		t.Errorf("old gzip rows = %v", rows)
	}
}

func TestDeriveEventsWriterRetention(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{time.Date(2026, 11, 14, 23, 59, 0, 0, time.UTC)}
	w := newTestDeriveEventsWriter(t, dir, clock) // retainDays 14
	today := time.Date(2026, 11, 15, 0, 1, 0, 0, time.UTC)
	expired := eventsFile(dir, today.AddDate(0, 0, -15)) + ".gz"
	kept := eventsFile(dir, today.AddDate(0, 0, -14)) + ".gz"
	for _, p := range []string{expired, kept} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w.handle(deviatingEventCheck(clock.t))
	clock.t = today
	w.handle(deviatingEventCheck(clock.t)) // rollover
	_ = w.close()
	w.waitCompress()

	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Errorf("%s not deleted (err %v)", expired, err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("%s deleted: %v", kept, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "derive-events.salt")); err != nil {
		t.Errorf("salt file removed: %v", err)
	}
}

// deviatingEventCheck is agreeingEventCheck with a level deviation, so it is
// written as soon as it is handled.
func deviatingEventCheck(ts time.Time) deriveEventCheck {
	ev := agreeingEventCheck(ts)
	ev.Agree.Level = dimDisagree
	return ev
}

// onlyDeviation writes one deviating ev and returns its row.
func onlyDeviation(t *testing.T, ev deriveEventCheck) map[string]any {
	t.Helper()
	dir := t.TempDir()
	clock := &testClock{ev.Time}
	w := newTestDeriveEventsWriter(t, dir, clock)
	ev.Agree.Level = dimDisagree
	w.handle(ev)
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	devs := rowsOfKind(readEventLines(t, eventsFile(dir, ev.Time)), "deviation")
	if len(devs) != 1 {
		t.Fatalf("got %d deviations, want 1", len(devs))
	}
	return devs[0]
}

func TestDeriveEventsUTCOffsetUnknownLocation(t *testing.T) {
	ev := agreeingEventCheck(time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC))
	ev.CellID, ev.Lat, ev.Lon = 0, 0, 0
	d := onlyDeviation(t, ev)
	if d["utc_offset"] != 0.0 || d["cell"] != "" {
		t.Errorf("utc_offset/cell = %v/%q, want 0/\"\"", d["utc_offset"], d["cell"])
	}
}

func TestDeriveEventsCellFromServerCell(t *testing.T) {
	// A level-15 cell in Tokyo; lat/lon point at London.
	l15 := s2.CellIDFromLatLng(s2.LatLngFromDegrees(35.68, 139.76)).Parent(15)
	ev := agreeingEventCheck(time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC))
	ev.CellID = int64(l15)
	d := onlyDeviation(t, ev)
	if want := l15.Parent(deriveEventsCellLevel).ToToken(); d["cell"] != want {
		t.Errorf("cell = %v, want %s", d["cell"], want)
	}
	if d["utc_offset"] != 540.0 {
		t.Errorf("utc_offset = %v, want 540 (Asia/Tokyo)", d["utc_offset"])
	}
}

func TestDeriveEventsUTCOffsetLondonSummer(t *testing.T) {
	ev := agreeingEventCheck(time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC))
	ev.CellID, ev.Lat, ev.Lon = 0, 51.5, -0.12
	d := onlyDeviation(t, ev)
	if d["utc_offset"] != 60.0 {
		t.Errorf("utc_offset = %v, want 60", d["utc_offset"])
	}
	want := s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5, -0.12)).Parent(deriveEventsCellLevel).ToToken()
	if d["cell"] != want {
		t.Errorf("cell = %v, want %s", d["cell"], want)
	}
}

func TestDeriveEventsAccountHashStable(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	w1 := newTestDeriveEventsWriter(t, dir, clock)
	a, b := w1.accountHash("trainer"), w1.accountHash("trainer")
	if len(a) != 12 || a != b {
		t.Fatalf("hashes %q %q, want equal 12-char", a, b)
	}
	if w1.accountHash("") != "" {
		t.Errorf("empty username hashed")
	}
	if w1.accountHash("other") == a {
		t.Errorf("different usernames collide")
	}
	_ = w1.close()

	w2 := newTestDeriveEventsWriter(t, dir, clock)
	if got := w2.accountHash("trainer"); got != a {
		t.Errorf("second writer hash %q, want %q (salt not reused)", got, a)
	}
	_ = w2.close()
	other := newTestDeriveEventsWriter(t, t.TempDir(), clock)
	if other.accountHash("trainer") == a {
		t.Errorf("fresh salt produced the same hash")
	}
	_ = other.close()
}

func TestDeriveEventsWriterInitBadDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newDeriveEventsWriter(file, 15*time.Minute, 14, time.Now); err == nil {
		t.Fatal("want error for a dir that is a regular file")
	}
}

func TestDeriveEventsWriterClampsSettings(t *testing.T) {
	w, err := newDeriveEventsWriter(t.TempDir(), 30*time.Second, 0, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if w.bucket != 15*time.Minute || w.retainDays != 14 {
		t.Errorf("bucket/retain = %v/%d, want 15m/14", w.bucket, w.retainDays)
	}
	_ = w.close()
}

func TestDeriveEventsWriterRunDrainsChannel(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	w := newTestDeriveEventsWriter(t, dir, clock)
	done := make(chan struct{})
	go func() { w.run(); close(done) }()
	w.events <- agreeingEventCheck(clock.t)
	close(w.events)
	<-done // run closes the writer on channel close
	if rows := rowsOfKind(readEventLines(t, eventsFile(dir, clock.t)), "summary"); len(rows) != 1 {
		t.Errorf("got %d summaries after run, want 1", len(rows))
	}
}

func TestDeriveEventsWriterOpenFailureThrottled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "events")
	clock := &testClock{time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	w := newTestDeriveEventsWriter(t, dir, clock)

	// Replace the dir with a regular file: neither MkdirAll nor the open can
	// succeed until it is restored.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		w.handle(deviatingEventCheck(clock.t))
	}
	if w.openFailures != 1 || w.droppedLines != 50 {
		t.Fatalf("openFailures/droppedLines = %d/%d, want 1/50 (one attempt per window)",
			w.openFailures, w.droppedLines)
	}

	// Restored, but still inside the retry window: still dropping.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(30 * time.Second)
	w.handle(deviatingEventCheck(clock.t))
	if w.openFailures != 1 || w.droppedLines != 51 {
		t.Fatalf("retried inside the window: openFailures/droppedLines = %d/%d", w.openFailures, w.droppedLines)
	}

	// Past the window: the open is retried, recreates the dir and succeeds.
	clock.t = clock.t.Add(deriveEventsOpenRetry)
	w.handle(deviatingEventCheck(clock.t))
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	if w.openFailures != 0 || w.droppedLines != 0 {
		t.Errorf("outage not reset: %d/%d", w.openFailures, w.droppedLines)
	}
	if devs := rowsOfKind(readEventLines(t, eventsFile(dir, clock.t)), "deviation"); len(devs) != 1 {
		t.Errorf("got %d deviations after recovery, want 1", len(devs))
	}
}
