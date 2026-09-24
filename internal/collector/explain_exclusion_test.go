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
	"database/sql/driver"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// Covers the fix for the EXPLAIN self-feedback loop described on
// isExplainStatement's doc comment: when pg_stat_statements.track=all (or
// any setup that tracks utility statements), this collector's own
// "EXPLAIN (FORMAT JSON, GENERIC_PLAN) <query>" calls get recorded as new
// pg_stat_statements rows in their own right. These tests assert those ghost
// rows are excluded from BOTH ingestSample (so they can never become a
// false-positive "regression" of their own) and plan capture (so they never
// produce the nested "EXPLAIN (...) EXPLAIN (...)" syntax error).

func TestIsExplainStatement(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"plain explain", "EXPLAIN SELECT 1", true},
		{"explain analyze", "EXPLAIN ANALYZE SELECT 1", true},
		{"generic plan capture's own shape", `EXPLAIN (FORMAT JSON, GENERIC_PLAN) SELECT * FROM widgets WHERE sku = $1`, true},
		{"lowercase", "explain select 1", true},
		{"mixed case", "ExPlAiN select 1", true},
		{"leading whitespace", "   EXPLAIN SELECT 1", true},
		{"leading newline/tab", "\n\tEXPLAIN SELECT 1", true},
		{"ordinary select", "SELECT * FROM widgets WHERE sku = $1", false},
		{"select mentioning explain later", "SELECT 1 -- explain this", false},
		{"empty string", "", false},
		{"whitespace only", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExplainStatement(tc.text); got != tc.want {
				t.Errorf("isExplainStatement(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// statStatementsRow is one row this test's fake pg_stat_statements query
// returns; scriptStatStatements below turns a list of these into the
// column/row shape queryStatStatements' SQL expects.
type statStatementsRow struct {
	queryID int64
	query   string
	calls   int64
	total   float64
	mean    float64
}

func scriptStatStatements(rows []statStatementsRow) rule {
	driverRows := make([][]driver.Value, len(rows))
	for i, r := range rows {
		driverRows[i] = []driver.Value{r.queryID, r.query, r.calls, r.total, r.mean}
	}
	return rule{
		substr: "FROM pg_stat_statements",
		cols:   []string{"queryid", "query", "calls", "total_exec_time", "mean_exec_time"},
		rows:   driverRows,
	}
}

func explainCounterValue(t *testing.T, col *Collector) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := col.explainSkipped.Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// ghost EXPLAIN entries a real EXPLAIN-based plan-capture cycle produces
// under pg_stat_statements.track=all — see this file's package doc comment.
const ghostQueryID int64 = 999001
const ghostQueryText = `EXPLAIN (FORMAT JSON, GENERIC_PLAN) SELECT * FROM widgets WHERE sku = $1`
const legitQueryID int64 = 1
const legitQueryText = `SELECT * FROM widgets WHERE sku = $1`

func TestScrape_ExplainRows_ExcludedFromSamples(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour, CapturePlans: false})
	col.db = newFakeDB(t, scriptedResponder(t, []rule{
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(160000)}}},
		scriptStatStatements([]statStatementsRow{
			{legitQueryID, legitQueryText, 10, 100, 10},
			{ghostQueryID, ghostQueryText, 5, 1, 0.2},
		}),
	}))

	if err := col.Scrape(context.Background()); err != nil {
		t.Fatalf("Scrape: %v", err)
	}

	ids := col.AllQueryIDs()
	foundLegit, foundGhost := false, false
	for _, id := range ids {
		if id == legitQueryID {
			foundLegit = true
		}
		if id == ghostQueryID {
			foundGhost = true
		}
	}
	if !foundLegit {
		t.Errorf("AllQueryIDs() = %v, want it to include the legit queryid %d", ids, legitQueryID)
	}
	if foundGhost {
		t.Errorf("AllQueryIDs() = %v, want it to EXCLUDE the ghost EXPLAIN queryid %d (ingestSample should never see it)", ids, ghostQueryID)
	}
	if got := explainCounterValue(t, col); got != 1 {
		t.Errorf("explainSkipped counter = %v, want 1 (one ghost row skipped)", got)
	}
}

func TestScrape_ExplainRows_ExcludedFromPlanCapture(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour, CapturePlans: true})
	col.db = newFakeDB(t, scriptedResponder(t, []rule{
		// resolveColumns and planner.CaptureGenericPlan's own version check
		// both query current_setting('server_version_num'); PG16 so
		// GENERIC_PLAN is available.
		{substr: "server_version_num", cols: []string{"server_version_num"}, rows: [][]driver.Value{{int64(160000)}}},
		// pg_store_plans not installed — CapturePlan falls back to
		// GENERIC_PLAN for the legit query, exactly as in this project's
		// live test cluster (see the conversation this fix came out of).
		{substr: "pg_extension", cols: []string{"extversion"}, rows: nil},
		{substr: "GENERIC_PLAN", cols: []string{"QUERY PLAN"}, rows: [][]driver.Value{{`[{"Plan": {"Node Type": "Index Scan", "Total Cost": 5.5}}]`}}},
		scriptStatStatements([]statStatementsRow{
			{legitQueryID, legitQueryText, 10, 100, 10},
			{ghostQueryID, ghostQueryText, 5, 1, 0.2},
		}),
	}))

	if err := col.Scrape(context.Background()); err != nil {
		t.Fatalf("Scrape: %v", err)
	}

	now := time.Now().UTC()

	if _, after := col.PlansAround(legitQueryID, now); after == nil {
		t.Errorf("PlansAround(legit queryid) after = nil, want a captured GENERIC_PLAN snapshot")
	}
	if before, after := col.PlansAround(ghostQueryID, now); before != nil || after != nil {
		t.Errorf("PlansAround(ghost queryid) = (%v, %v), want (nil, nil) — capturePlans must never even be asked to EXPLAIN a ghost EXPLAIN entry, which would produce a nested \"EXPLAIN (...) EXPLAIN (...)\" the real server rejects with a syntax error", before, after)
	}
	if got := explainCounterValue(t, col); got != 1 {
		t.Errorf("explainSkipped counter = %v, want 1 (one ghost row skipped)", got)
	}
}
