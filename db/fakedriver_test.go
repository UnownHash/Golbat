package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"

	"github.com/jmoiron/sqlx"

	"golbat/stats_collector"
)

// fakeCapture records every statement the fake driver executes, so tests can
// assert on the SQL text and the bound arguments the db package produced.
type fakeCapture struct {
	mu      sync.Mutex
	queries []string
	args    [][]any
}

func (c *fakeCapture) record(query string, args []driver.NamedValue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	vals := make([]any, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	c.queries = append(c.queries, query)
	c.args = append(c.args, vals)
}

func (c *fakeCapture) last() (string, []any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queries) == 0 {
		return "", nil
	}
	return c.queries[len(c.queries)-1], c.args[len(c.args)-1]
}

// fakeConnector serves every query from a fixed result set. When the rows are
// exhausted Next returns nextErr (or io.EOF when nil), which is how a mid-stream
// failure surfaces through database/sql's rows.Err().
type fakeConnector struct {
	cols    []string
	rows    [][]driver.Value
	nextErr error
	cap     *fakeCapture
}

func (c *fakeConnector) Connect(context.Context) (driver.Conn, error) { return &fakeConn{c: c}, nil }
func (c *fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use OpenDB") }

type fakeConn struct{ c *fakeConnector }

func (fc *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not supported")
}
func (fc *fakeConn) Close() error              { return nil }
func (fc *fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("tx not supported") }

// QueryContext implements driver.QueryerContext so database/sql skips Prepare.
func (fc *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	fc.c.cap.record(query, args)
	return &fakeRows{c: fc.c}, nil
}

type fakeRows struct {
	c *fakeConnector
	i int
}

func (r *fakeRows) Columns() []string { return r.c.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.c.rows) {
		if r.c.nextErr != nil {
			return r.c.nextErr
		}
		return io.EOF
	}
	copy(dest, r.c.rows[r.i])
	r.i++
	return nil
}

// newFakeDb returns a sqlx handle backed by the fake driver, plus the capture
// that records what was executed.
func newFakeDb(cols []string, rows [][]driver.Value, nextErr error) (*sqlx.DB, *fakeCapture) {
	cap := &fakeCapture{}
	conn := &fakeConnector{cols: cols, rows: rows, nextErr: nextErr, cap: cap}
	return sqlx.NewDb(sql.OpenDB(conn), "mysql"), cap
}

// captureStats records the error passed to IncDbQuery per query label.
type captureStats struct {
	stats_collector.StatsCollector
	mu    sync.Mutex
	calls map[string][]error
}

func newCaptureStats() *captureStats {
	return &captureStats{StatsCollector: stats_collector.NewNoopStatsCollector(), calls: map[string][]error{}}
}

func (c *captureStats) IncDbQuery(query string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[query] = append(c.calls[query], err)
}

// installCaptureStats swaps the package collector for a capturing one for the
// duration of the test.
func installCaptureStats(t interface{ Cleanup(func()) }) *captureStats {
	prev := statsCollector
	cs := newCaptureStats()
	SetStatsCollector(cs)
	t.Cleanup(func() { SetStatsCollector(prev) })
	return cs
}
