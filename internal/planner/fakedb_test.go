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

package planner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
)

// This file implements a minimal database/sql/driver.Driver so tests can
// exercise this package's *sql.DB-shaped functions (CapturePlanFromStorePlans,
// CaptureGenericPlan, CapturePlan) against scripted responses, without a
// real PostgreSQL server and without adding a third-party mocking
// dependency (e.g. go-sqlmock) to go.mod for what is, dispatch-wise, a
// handful of single-row SELECTs and one SET LOCAL. Every test in this
// package matches on the SQL text via fakeResponder rather than parsing SQL
// for real.

// fakeResponder answers one query/exec call. It receives the exact SQL text
// this package issued (so tests match on the substrings that identify each
// statement — "pg_extension", "server_version_num", "compute_query_id",
// "FROM pg_store_plans", "GENERIC_PLAN", etc.) and the bound argument
// values, and returns either a result set (cols/rows) or an error.
type fakeResponder func(query string, args []driver.Value) (cols []string, rows [][]driver.Value, err error)

var fakeDriverSeq atomic.Int64

// newFakeDB registers a throwaway driver backed by responder under a unique
// name (sql.Register's registry is global and rejects re-registration under
// the same name, so tests can't just reuse one fixed name across the whole
// package) and returns a *sql.DB using it. The underlying driver and DB are
// closed/discarded automatically via t.Cleanup.
func newFakeDB(t *testing.T, responder fakeResponder) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("planner-fake-%d", fakeDriverSeq.Add(1))
	sql.Register(name, &fakeDriver{responder: responder})
	db, err := sql.Open(name, "fake")
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
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

// Begin satisfies driver.Conn. database/sql prefers BeginTx (below) when
// available, but the interface still requires Begin to exist.
func (c *fakeConn) Begin() (driver.Tx, error) { return &fakeTx{}, nil }

// BeginTx implements driver.ConnBeginTx so db.BeginTx (used by
// CapturePlanFromStorePlans to scope SET LOCAL) works against this fake.
func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &fakeTx{}, nil
}

// QueryContext implements driver.QueryerContext.
func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	cols, rows, err := c.responder(query, namedToValues(args))
	if err != nil {
		return nil, err
	}
	return &fakeRows{cols: cols, rows: rows}, nil
}

// ExecContext implements driver.ExecerContext.
func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, _, err := c.responder(query, namedToValues(args))
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

func namedToValues(args []driver.NamedValue) []driver.Value {
	vs := make([]driver.Value, len(args))
	for i, a := range args {
		vs[i] = a.Value
	}
	return vs
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

// fakeStmt exists only to satisfy driver.Conn.Prepare; every call path this
// package actually exercises goes through QueryContext/ExecContext above,
// which database/sql prefers over Prepare when the connection implements
// them. It's kept minimal on purpose.
type fakeStmt struct {
	conn  *fakeConn
	query string
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	_, _, err := s.conn.responder(s.query, args)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	cols, rows, err := s.conn.responder(s.query, args)
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
