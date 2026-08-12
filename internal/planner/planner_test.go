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

package planner_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/planner"

	_ "github.com/lib/pq"
)

// ----- fake driver: simulates a fixed server_version_num without a real DB -----
//
// CapturePlan's version-gating branch (major < 16 -> ErrUnsupportedVersion)
// is exercised here against a hand-written database/sql/driver.Driver that
// answers the exact `SELECT current_setting('server_version_num')::int`
// query CapturePlan issues, rather than against a real old-major-version
// PostgreSQL server (impractical to stand up alongside the PG16 harness this
// repo's integration tests already use). This proves the sentinel-error
// contract deterministically and without any external dependency.

type fakeVersionDriver struct{ versionNum int64 }

func (d fakeVersionDriver) Open(name string) (driver.Conn, error) {
	return fakeVersionConn(d), nil
}

type fakeVersionConn struct{ versionNum int64 }

func (c fakeVersionConn) Prepare(query string) (driver.Stmt, error) {
	return fakeVersionStmt(c), nil
}
func (c fakeVersionConn) Close() error              { return nil }
func (c fakeVersionConn) Begin() (driver.Tx, error) { return nil, errors.New("not supported") }

type fakeVersionStmt struct{ versionNum int64 }

func (s fakeVersionStmt) Close() error  { return nil }
func (s fakeVersionStmt) NumInput() int { return -1 }
func (s fakeVersionStmt) Exec(args []driver.Value) (driver.Result, error) {
	return nil, errors.New("not supported")
}
func (s fakeVersionStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &fakeVersionRows{versionNum: s.versionNum}, nil
}

// fakeVersionRows yields exactly one row with one int64 column, regardless
// of which query text was sent — sufficient for CapturePlan, which only ever
// issues the single version-detection query before it would go on to issue
// the real EXPLAIN (this test never lets it get that far).
type fakeVersionRows struct {
	versionNum int64
	done       bool
}

func (r *fakeVersionRows) Columns() []string { return []string{"server_version_num"} }
func (r *fakeVersionRows) Close() error      { return nil }
func (r *fakeVersionRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.versionNum
	return nil
}

func init() {
	sql.Register("pgrr_fake_pg14", fakeVersionDriver{versionNum: 140005})
}

// ----- Diff -----

func TestDiff_NilSnapshotsReturnEmpty(t *testing.T) {
	t.Parallel()

	snap := &planner.PlanSnapshot{RootNodeType: "Seq Scan", TotalCost: 10}

	cases := []struct {
		name          string
		before, after *planner.PlanSnapshot
	}{
		{"both nil", nil, nil},
		{"before nil", nil, snap},
		{"after nil", snap, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := planner.Diff(tc.before, tc.after); got != "" {
				t.Errorf("expected empty diff when a snapshot is nil, got %q", got)
			}
		})
	}
}

func TestDiff_RootNodeTypeChanged(t *testing.T) {
	t.Parallel()

	before := &planner.PlanSnapshot{RootNodeType: "Index Scan", TotalCost: 10}
	after := &planner.PlanSnapshot{RootNodeType: "Seq Scan", TotalCost: 10}

	got := planner.Diff(before, after)
	want := "root plan node changed from Index Scan to Seq Scan"
	if !strings.Contains(got, want) {
		t.Errorf("expected diff to mention %q, got %q", want, got)
	}
}

func TestDiff_CostIncreaseReported(t *testing.T) {
	t.Parallel()

	before := &planner.PlanSnapshot{RootNodeType: "Index Scan", TotalCost: 10}
	after := &planner.PlanSnapshot{RootNodeType: "Index Scan", TotalCost: 42}

	got := planner.Diff(before, after)
	if !strings.Contains(got, "cost increased 4.2x") {
		t.Errorf("expected diff to mention a 4.2x cost increase, got %q", got)
	}
	// Root node type didn't change, so it must not be mentioned.
	if strings.Contains(got, "root plan node changed") {
		t.Errorf("did not expect a root-node-changed message when the node type is unchanged, got %q", got)
	}
}

func TestDiff_CostDecreaseReported(t *testing.T) {
	t.Parallel()

	before := &planner.PlanSnapshot{RootNodeType: "Seq Scan", TotalCost: 100}
	after := &planner.PlanSnapshot{RootNodeType: "Seq Scan", TotalCost: 20}

	got := planner.Diff(before, after)
	if !strings.Contains(got, "cost decreased 5.0x") {
		t.Errorf("expected diff to mention a 5.0x cost decrease, got %q", got)
	}
}

func TestDiff_MinorCostFluctuationNotReported(t *testing.T) {
	t.Parallel()

	// A ~5% change is within the noise band CapturePlan/Diff tolerate (real
	// autovacuum-driven statistics drift between two GENERIC_PLAN calls of an
	// otherwise-unchanged plan), so it should not be called out as a cost
	// change.
	before := &planner.PlanSnapshot{RootNodeType: "Seq Scan", TotalCost: 100}
	after := &planner.PlanSnapshot{RootNodeType: "Seq Scan", TotalCost: 104}

	got := planner.Diff(before, after)
	if strings.Contains(got, "cost increased") || strings.Contains(got, "cost decreased") {
		t.Errorf("did not expect a cost-change callout for a minor fluctuation, got %q", got)
	}
}

func TestDiff_NoChangeReturnsReadableMessage(t *testing.T) {
	t.Parallel()

	before := &planner.PlanSnapshot{RootNodeType: "Index Scan", TotalCost: 50}
	after := &planner.PlanSnapshot{RootNodeType: "Index Scan", TotalCost: 50}

	got := planner.Diff(before, after)
	if got == "" {
		t.Fatal("expected a non-empty, readable message when both snapshots are present but nothing changed")
	}
	if strings.Contains(got, "root plan node changed") || strings.Contains(got, "cost increased") || strings.Contains(got, "cost decreased") {
		t.Errorf("expected an unchanged-plan message, got a change-callout: %q", got)
	}
}

func TestDiff_ZeroBeforeCostToPositiveAfterCost(t *testing.T) {
	t.Parallel()

	before := &planner.PlanSnapshot{RootNodeType: "Result", TotalCost: 0}
	after := &planner.PlanSnapshot{RootNodeType: "Seq Scan", TotalCost: 12.5}

	got := planner.Diff(before, after)
	if !strings.Contains(got, "estimated cost went from 0 to 12.5") {
		t.Errorf("expected a 0->12.5 cost callout, got %q", got)
	}
}

// ----- CapturePlan version gating -----

// TestCapturePlan_UnsupportedVersion_PG14 proves CapturePlan's <PG16
// rejection path (major < 16 -> ErrUnsupportedVersion) deterministically,
// using the fake driver above to report server_version_num=140005 (PostgreSQL
// 14.5) without needing a real old-major-version Postgres server. The real
// ">=16 actually captures a plan" path is proven against a live PostgreSQL 16
// by TestIntegration_CapturePlan_RealPostgres16 (build-tag gated; see
// planner_integration_test.go).
func TestCapturePlan_UnsupportedVersion_PG14(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("pgrr_fake_pg14", "fake")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = planner.CapturePlan(context.Background(), db, 1, "SELECT 1")
	if !errors.Is(err, planner.ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion for a PostgreSQL 14 server, got: %v", err)
	}
}

// TestCapturePlan_UnreachableServer_ReturnsConnectionError_NotVersionError
// confirms CapturePlan surfaces the underlying connection error (NOT
// ErrUnsupportedVersion) when it can't even determine the server version —
// i.e. version-gating only fires once a version number was actually read,
// so a plain connectivity failure isn't misreported as "unsupported version".
func TestCapturePlan_UnreachableServer_ReturnsConnectionError_NotVersionError(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("postgres", "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = planner.CapturePlan(ctx, db, 1, "SELECT 1")
	if err == nil {
		t.Fatal("expected an error against an unreachable server, got nil")
	}
	if errors.Is(err, planner.ErrUnsupportedVersion) {
		t.Errorf("expected a connection error, not ErrUnsupportedVersion, since the version query itself never succeeded: %v", err)
	}
}
