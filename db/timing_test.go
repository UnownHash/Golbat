package db

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestTimedPassesErrorThroughUnchanged(t *testing.T) {
	old := slowQueryLogThreshold.Load()
	defer slowQueryLogThreshold.Store(old)

	SetSlowQueryLogThreshold(time.Hour)
	err := Timed("test", func() error { return sql.ErrNoRows })
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Timed altered the error: %v", err)
	}
	if err := Timed("test", func() error { return nil }); err != nil {
		t.Errorf("Timed returned unexpected error: %v", err)
	}
}

func TestTimedDisabledSkipsTiming(t *testing.T) {
	old := slowQueryLogThreshold.Load()
	defer slowQueryLogThreshold.Store(old)

	SetSlowQueryLogThreshold(-1)
	ran := false
	if err := Timed("test", func() error { ran = true; return nil }); err != nil || !ran {
		t.Errorf("disabled Timed must still run fn (ran=%t, err=%v)", ran, err)
	}
}
