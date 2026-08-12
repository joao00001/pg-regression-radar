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
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// rule is one (substring, handler) pair tried in order by scriptedResponder.
type rule struct {
	substr string
	cols   []string
	rows   [][]driver.Value
	err    error
}

// scriptedResponder builds a fakeResponder from an ordered list of rules,
// returning the first rule whose substr appears in the query text. This
// lets each test read as a short table of "when the SQL looks like X,
// answer with Y" instead of hand-rolling driver plumbing per test.
func scriptedResponder(t *testing.T, rules []rule) fakeResponder {
	t.Helper()
	return func(query string, _ []driver.Value) ([]string, [][]driver.Value, error) {
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

const genericPlanJSON = `[{"Plan": {"Node Type": "Index Scan", "Total Cost": 5.5}}]`

func TestCapturePlanFromStorePlans_ReliableExtension(t *testing.T) {
	db := newFakeDB(t, scriptedResponder(t, []rule{
		{substr: "pg_extension", cols: []string{"extversion"}, rows: [][]driver.Value{{"1.8"}}},
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(160003)}}},
		{substr: "compute_query_id", cols: []string{"current_setting"}, rows: [][]driver.Value{{"on"}}},
		{substr: "plan_format"},
		{substr: "FROM pg_store_plans", cols: []string{"plan"}, rows: [][]driver.Value{
			{`{"Plan": {"Node Type": "Seq Scan", "Total Cost": 12.34}}`},
		}},
	}))

	snap, err := CapturePlanFromStorePlans(context.Background(), db, 42)
	if err != nil {
		t.Fatalf("CapturePlanFromStorePlans: unexpected error: %v", err)
	}
	if snap.Source != SourceStorePlans {
		t.Errorf("Source = %q, want %q", snap.Source, SourceStorePlans)
	}
	if snap.QueryID != 42 {
		t.Errorf("QueryID = %d, want 42", snap.QueryID)
	}
	if snap.RootNodeType != "Seq Scan" {
		t.Errorf("RootNodeType = %q, want %q", snap.RootNodeType, "Seq Scan")
	}
	if snap.TotalCost != 12.34 {
		t.Errorf("TotalCost = %v, want 12.34", snap.TotalCost)
	}
	if snap.RecordedAt.IsZero() {
		t.Error("RecordedAt should be set")
	}
}

func TestCapturePlan_ExtensionNotInstalled_FallsBackToGenericPlan(t *testing.T) {
	db := newFakeDB(t, scriptedResponder(t, []rule{
		{substr: "pg_extension", cols: []string{"extversion"}, rows: nil}, // no rows => sql.ErrNoRows
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(170000)}}},
		{substr: "GENERIC_PLAN", cols: []string{"QUERY PLAN"}, rows: [][]driver.Value{{genericPlanJSON}}},
	}))

	// Direct call should report the extension is missing.
	if _, err := CapturePlanFromStorePlans(context.Background(), db, 7); !errors.Is(err, ErrExtensionNotInstalled) {
		t.Fatalf("CapturePlanFromStorePlans error = %v, want ErrExtensionNotInstalled", err)
	}

	// The facade should transparently fall back to generic_plan.
	snap, err := CapturePlan(context.Background(), db, 7, "SELECT 1")
	if err != nil {
		t.Fatalf("CapturePlan: unexpected error: %v", err)
	}
	if snap.Source != SourceGenericPlan {
		t.Errorf("Source = %q, want %q", snap.Source, SourceGenericPlan)
	}
	if snap.RootNodeType != "Index Scan" {
		t.Errorf("RootNodeType = %q, want %q", snap.RootNodeType, "Index Scan")
	}
}

func TestCapturePlan_ComputeQueryIDOff_FallsBackToGenericPlan(t *testing.T) {
	db := newFakeDB(t, scriptedResponder(t, []rule{
		{substr: "pg_extension", cols: []string{"extversion"}, rows: [][]driver.Value{{"1.8"}}},
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(160000)}}},
		{substr: "compute_query_id", cols: []string{"current_setting"}, rows: [][]driver.Value{{"off"}}},
		{substr: "GENERIC_PLAN", cols: []string{"QUERY PLAN"}, rows: [][]driver.Value{{genericPlanJSON}}},
	}))

	_, err := CapturePlanFromStorePlans(context.Background(), db, 9)
	if !errors.Is(err, ErrQueryIDUnreliable) {
		t.Fatalf("CapturePlanFromStorePlans error = %v, want ErrQueryIDUnreliable", err)
	}

	snap, err := CapturePlan(context.Background(), db, 9, "SELECT 1")
	if err != nil {
		t.Fatalf("CapturePlan: unexpected error: %v", err)
	}
	if snap.Source != SourceGenericPlan {
		t.Errorf("Source = %q, want %q", snap.Source, SourceGenericPlan)
	}
}

func TestCapturePlan_OldExtensionVersion_FallsBackToGenericPlan(t *testing.T) {
	db := newFakeDB(t, scriptedResponder(t, []rule{
		{substr: "pg_extension", cols: []string{"extversion"}, rows: [][]driver.Value{{"1.5"}}},
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(170000)}}},
		{substr: "GENERIC_PLAN", cols: []string{"QUERY PLAN"}, rows: [][]driver.Value{{genericPlanJSON}}},
	}))

	_, err := CapturePlanFromStorePlans(context.Background(), db, 3)
	if !errors.Is(err, ErrQueryIDUnreliable) {
		t.Fatalf("CapturePlanFromStorePlans error = %v, want ErrQueryIDUnreliable", err)
	}
	if !strings.Contains(err.Error(), "predates") {
		t.Errorf("error message should explain the version mismatch, got: %v", err)
	}

	snap, err := CapturePlan(context.Background(), db, 3, "SELECT 1")
	if err != nil {
		t.Fatalf("CapturePlan: unexpected error: %v", err)
	}
	if snap.Source != SourceGenericPlan {
		t.Errorf("Source = %q, want %q", snap.Source, SourceGenericPlan)
	}
}

func TestCapturePlan_NothingAvailable_UnsupportedVersionPropagates(t *testing.T) {
	db := newFakeDB(t, scriptedResponder(t, []rule{
		{substr: "pg_extension", cols: []string{"extversion"}, rows: nil},                                             // not installed
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(130000)}}}, // PG13, < 16
	}))

	// CaptureGenericPlan alone, in isolation.
	if _, err := CaptureGenericPlan(context.Background(), db, 1, "SELECT 1"); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("CaptureGenericPlan error = %v, want ErrUnsupportedVersion", err)
	}

	// The facade: neither source available, error should still let a
	// caller identify ErrUnsupportedVersion via errors.Is.
	_, err := CapturePlan(context.Background(), db, 1, "SELECT 1")
	if err == nil {
		t.Fatal("CapturePlan: expected an error when neither source is available")
	}
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("CapturePlan error = %v, want it to wrap ErrUnsupportedVersion", err)
	}
}

func TestCapturePlanFromStorePlans_NoRowsForQueryID(t *testing.T) {
	db := newFakeDB(t, scriptedResponder(t, []rule{
		{substr: "pg_extension", cols: []string{"extversion"}, rows: [][]driver.Value{{"1.9"}}},
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(170000)}}},
		{substr: "compute_query_id", cols: []string{"current_setting"}, rows: [][]driver.Value{{"auto"}}},
		{substr: "plan_format"},
		{substr: "FROM pg_store_plans", cols: []string{"plan"}, rows: nil},
	}))

	_, err := CapturePlanFromStorePlans(context.Background(), db, 99)
	if !errors.Is(err, ErrNoPlanRecorded) {
		t.Fatalf("CapturePlanFromStorePlans error = %v, want ErrNoPlanRecorded", err)
	}
}

func TestExtVersionAtLeast(t *testing.T) {
	cases := []struct {
		version string
		major   int
		minor   int
		want    bool
	}{
		{"1.8", 1, 6, true},
		{"1.6", 1, 6, true},
		{"1.5", 1, 6, false},
		{"1.10", 1, 6, true}, // numeric, not lexical, comparison
		{"2.0", 1, 6, true},
		{"0.9", 1, 6, false},
		{"garbage", 1, 6, false},
		{"1", 1, 6, false},
	}
	for _, c := range cases {
		got := extVersionAtLeast(c.version, c.major, c.minor)
		if got != c.want {
			t.Errorf("extVersionAtLeast(%q, %d, %d) = %v, want %v", c.version, c.major, c.minor, got, c.want)
		}
	}
}

func TestParsePlanJSON(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantNode string
		wantCost float64
		wantErr  bool
	}{
		{
			name:     "explain-style wrapped array",
			raw:      `[{"Plan": {"Node Type": "Hash Join", "Total Cost": 100.5}}]`,
			wantNode: "Hash Join",
			wantCost: 100.5,
		},
		{
			name:     "bare object with Plan key",
			raw:      `{"Plan": {"Node Type": "Seq Scan", "Total Cost": 12.34}}`,
			wantNode: "Seq Scan",
			wantCost: 12.34,
		},
		{
			name:     "bare node without Plan wrapper",
			raw:      `{"Node Type": "Index Scan", "Total Cost": 3.2}`,
			wantNode: "Index Scan",
			wantCost: 3.2,
		},
		{
			name:    "empty string",
			raw:     "",
			wantErr: true,
		},
		{
			name:    "invalid json",
			raw:     "{not json",
			wantErr: true,
		},
		{
			name:    "missing node type",
			raw:     `{"Plan": {"Total Cost": 1.0}}`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			node, cost, err := parsePlanJSON(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parsePlanJSON(%q): expected error, got nil", c.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePlanJSON(%q): unexpected error: %v", c.raw, err)
			}
			if node != c.wantNode {
				t.Errorf("node = %q, want %q", node, c.wantNode)
			}
			if cost != c.wantCost {
				t.Errorf("cost = %v, want %v", cost, c.wantCost)
			}
		})
	}
}

// TestCapturePlan_UnreachableServer_ReturnsConnectionError_NotVersionError
// confirms CapturePlan surfaces the underlying connection error (NOT
// ErrUnsupportedVersion) when it can't even reach the server to check
// pg_store_plans or the server version. Both CapturePlanFromStorePlans and
// CaptureGenericPlan issue their first query against the same unreachable
// server here, so this proves version-gating only fires once a version
// number was actually read — a plain connectivity failure must not be
// misreported as "unsupported version", since a caller (see
// internal/collector.Collector.capturePlans) treats ErrUnsupportedVersion as
// a permanent, log-once-and-stop signal that would be wrong to raise for a
// transient network problem.
func TestCapturePlan_UnreachableServer_ReturnsConnectionError_NotVersionError(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("postgres", "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = CapturePlan(ctx, db, 1, "SELECT 1")
	if err == nil {
		t.Fatal("expected an error against an unreachable server, got nil")
	}
	if errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("expected a connection error, not ErrUnsupportedVersion, since neither source's version/extension query ever succeeded: %v", err)
	}
}

func TestDiff(t *testing.T) {
	before := &PlanSnapshot{
		QueryID:      1,
		RecordedAt:   time.Now().Add(-time.Hour),
		RootNodeType: "Index Scan",
		TotalCost:    10.0,
		Source:       SourceStorePlans,
	}
	after := &PlanSnapshot{
		QueryID:      1,
		RecordedAt:   time.Now(),
		RootNodeType: "Seq Scan",
		TotalCost:    50.0,
		Source:       SourceStorePlans,
	}

	out := Diff(before, after)
	for _, want := range []string{"Index Scan", "Seq Scan", "10.00", "50.00", "+400.0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("Diff output missing %q, got: %s", want, out)
		}
	}
	if strings.Contains(out, "caution") {
		t.Errorf("Diff should not warn about estimation when both sources are pg_store_plans, got: %s", out)
	}

	afterEstimated := &PlanSnapshot{QueryID: 1, RootNodeType: "Seq Scan", TotalCost: 50.0, Source: SourceGenericPlan}
	outCaution := Diff(before, afterEstimated)
	if !strings.Contains(outCaution, "caution") {
		t.Errorf("Diff should warn when one snapshot is generic_plan, got: %s", outCaution)
	}

	if !strings.Contains(Diff(nil, after), "no plan captured before") {
		t.Errorf("Diff(nil, after) should say no plan before, got: %s", Diff(nil, after))
	}
	if !strings.Contains(Diff(before, nil), "no plan captured after") {
		t.Errorf("Diff(before, nil) should say no plan after, got: %s", Diff(before, nil))
	}
	if !strings.Contains(Diff(nil, nil), "no plan captured on either side") {
		t.Errorf("Diff(nil, nil) should say no plan on either side, got: %s", Diff(nil, nil))
	}
}
