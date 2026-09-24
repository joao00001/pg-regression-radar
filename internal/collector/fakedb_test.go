// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package collector

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

// This file implements a minimal database/sql/driver.Driver so scrape()-level
// tests can exercise the Collector's real scrape/resolveColumns/capturePlans
// path — including the isExplainStatement filter this file's sibling test
// covers — without a real PostgreSQL server. It mirrors
// internal/planner/fakedb_test.go's approach (same rationale: a handful of
// scripted SELECTs don't justify a third-party mocking dependency), rather
// than importing it, since fakedb_test.go's identifiers are unexported to
// package planner.

// fakeResponder answers one query call, given the exact SQL text this
// package issued.
type fakeResponder func(query string) (cols []string, rows [][]driver.Value, err error)

var fakeDriverSeq atomic.Int64

// newFakeDB registers a throwaway driver backed by responder under a unique
// name (sql.Register's registry is global and rejects re-registration under
// the same name) and returns a *sql.DB using it. The underlying driver and DB
// are closed/discarded automatically via t.Cleanup.
func newFakeDB(t *testing.T, responder fakeResponder) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("collector-fake-%d", fakeDriverSeq.Add(1))
	sql.Register(name, &fakeDriver{responder: responder})
	db, err := sql.Open(name, "fake")
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rule is one (substring, canned response) pair tried in order by
// scriptedResponder.
type rule struct {
	substr string
	cols   []string
	rows   [][]driver.Value
	err    error
}

// scriptedResponder builds a fakeResponder from an ordered list of rules,
// returning the first rule whose substr appears in the query text, so each
// test reads as a short table of "when the SQL looks like X, answer with Y".
func scriptedResponder(t *testing.T, rules []rule) fakeResponder {
	t.Helper()
	return func(query string) ([]string, [][]driver.Value, error) {
		for _, r := range rules {
			if strings.Contains(query, r.substr) {
				if r.err != nil {
					return nil, nil, r.err
				}
				return r.cols, r.rows, nil
			}
		}
		t.Fatalf("scriptedResponder: no rule matched query: %s", query)
		return nil, nil, nil
	}
}

type fakeDriver struct {
	responder fakeResponder
}

func (d *fakeDriver) Open(string) (driver.Conn, error) {
	return &fakeConn{responder: d.responder}, nil
}

type fakeConn struct {
	responder fakeResponder
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{conn: c, query: query}, nil
}

func (c *fakeConn) Close() error { return nil }

func (c *fakeConn) Begin() (driver.Tx, error) { return &fakeTx{}, nil }

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &fakeTx{}, nil
}

// QueryContext implements driver.QueryerContext.
func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	cols, rows, err := c.responder(query)
	if err != nil {
		return nil, err
	}
	return &fakeRows{cols: cols, rows: rows}, nil
}

// ExecContext implements driver.ExecerContext.
func (c *fakeConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	_, _, err := c.responder(query)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

// fakeStmt exists only to satisfy driver.Conn.Prepare; every call path this
// package's tests actually exercise goes through QueryContext/ExecContext
// above, which database/sql prefers over Prepare when the connection
// implements them.
type fakeStmt struct {
	conn  *fakeConn
	query string
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	_, _, err := s.conn.responder(s.query)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	cols, rows, err := s.conn.responder(s.query)
	if err != nil {
		return nil, err
	}
	return &fakeRows{cols: cols, rows: rows}, nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}
