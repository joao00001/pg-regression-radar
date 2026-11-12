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
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type identityTestRow struct {
	dbID, userID, queryID int64
	topLevel              bool
	calls                 int64
	total                 float64
	since                 time.Time
}

// identityResponder models the columns actually selected by the scrape SQL.
// On the old queryid-only SQL it returns the old five-column shape, so the
// regression below exposes mixed counters rather than a mock scan failure.
func identityResponder(t *testing.T, version int64, rows *[]identityTestRow) fakeResponder {
	t.Helper()
	return func(query string) ([]string, [][]driver.Value, error) {
		if strings.Contains(query, "server_version_num") {
			return []string{"server_version_num"}, [][]driver.Value{{version}}, nil
		}
		if !strings.Contains(query, "FROM pg_stat_statements") {
			t.Fatalf("unexpected query: %s", query)
		}
		identitySelected := strings.Contains(query, "userid")
		columns := []string{"queryid", "query", "calls", "total_exec_time", "mean_exec_time"}
		if identitySelected {
			columns = append([]string{"dbid", "userid", "toplevel"}, columns...)
		}
		if version >= 170000 {
			columns = append(columns, "stats_since")
		}
		values := make([][]driver.Value, 0, len(*rows))
		for _, row := range *rows {
			mean := float64(0)
			if row.calls > 0 {
				mean = row.total / float64(row.calls)
			}
			v := []driver.Value{row.queryID, "SELECT * FROM items WHERE id = $1", row.calls, row.total, mean}
			if identitySelected {
				v = append([]driver.Value{row.dbID, row.userID, row.topLevel}, v...)
			}
			if version >= 170000 {
				v = append(v, row.since)
			}
			values = append(values, v)
		}
		return columns, values, nil
	}
}

func TestScrape_RowIdentitySeparatesRoleCounters(t *testing.T) {
	col := newTestCollector(t, Config{})
	rows := []identityTestRow{
		{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 1000, total: 2000},
		{dbID: 1, userID: 20, queryID: 42, topLevel: true, calls: 50, total: 500},
	}
	col.db = newFakeDB(t, identityResponder(t, 160000, &rows))
	if err := col.scrape(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows[0].calls, rows[0].total = 1100, 2200
	rows[1].calls, rows[1].total = 55, 550
	// The result must not depend on which role PostgreSQL returns first.
	rows[0], rows[1] = rows[1], rows[0]
	if err := col.scrape(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := col.SamplesInRange(42, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if len(got) != 2 {
		t.Fatalf("got %d samples, want one per queryid in each of two scrapes: %+v", len(got), got)
	}
	if got[0].IntervalKnown || got[0].ResetDetected {
		t.Fatalf("first scrape cannot infer an interval or reset: %+v", got[0])
	}
	last := got[1]
	if !last.IntervalKnown || last.ResetDetected || last.IntervalCalls != 105 || !approxEqual(last.IntervalTotalMs, 250) || !approxEqual(last.IntervalMeanMs, 250.0/105) {
		t.Errorf("mixed independent counters: got %+v; want 105 calls / 250 ms without reset", last)
	}
}

func TestScrape_RowIdentitySeparatesDatabaseAndTopLevel(t *testing.T) {
	for _, dimension := range []string{"database", "toplevel"} {
		t.Run(dimension, func(t *testing.T) {
			col := newTestCollector(t, Config{})
			rows := []identityTestRow{
				{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 1000, total: 2000},
				{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 50, total: 500},
			}
			if dimension == "database" {
				rows[1].dbID = 2
			} else {
				rows[1].topLevel = false
			}
			col.db = newFakeDB(t, identityResponder(t, 160000, &rows))
			if err := col.scrape(context.Background()); err != nil {
				t.Fatal(err)
			}
			rows[0].calls, rows[0].total = 1100, 2200
			rows[1].calls, rows[1].total = 55, 550
			if err := col.scrape(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := col.samples[42][1]
			if !got.IntervalKnown || got.ResetDetected || got.IntervalCalls != 105 || !approxEqual(got.IntervalTotalMs, 250) {
				t.Fatalf("independent %s counters mixed: %+v", dimension, got)
			}
		})
	}
}

func TestScrape_RowIdentityDetectsOneRowResetBeforeAggregation(t *testing.T) {
	since := time.Now().UTC().Add(-time.Hour)
	for _, changedSince := range []bool{false, true} {
		t.Run(map[bool]string{false: "counter decrease", true: "stats_since changes above old counters"}[changedSince], func(t *testing.T) {
			col := newTestCollector(t, Config{})
			rows := []identityTestRow{
				{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 1000, total: 2000, since: since},
				{dbID: 1, userID: 20, queryID: 42, topLevel: true, calls: 50, total: 500, since: since},
			}
			col.db = newFakeDB(t, identityResponder(t, 170000, &rows))
			if err := col.scrape(context.Background()); err != nil {
				t.Fatal(err)
			}
			// A grows enough to hide B's reset in the aggregate counters.
			rows[0].calls, rows[0].total = 2000, 4000
			rows[1].calls, rows[1].total = 5, 50
			if changedSince {
				rows[1].calls, rows[1].total = 60, 600
				rows[1].since = since.Add(time.Minute)
			}
			if err := col.scrape(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := col.samples[42][1]
			if !got.IntervalKnown || !got.ResetDetected || got.IntervalCalls != 1000+rows[1].calls || !approxEqual(got.IntervalTotalMs, 2000+rows[1].total) {
				t.Fatalf("per-row reset lost in aggregate: %+v", got)
			}
			if !got.StatsSince.IsZero() {
				t.Fatalf("aggregate must not invent a shared reset epoch: %v", got.StatsSince)
			}
		})
	}
}

func TestScrape_RowIdentityMembershipChangesNeedNewBaseline(t *testing.T) {
	col := newTestCollector(t, Config{})
	a := identityTestRow{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 1000, total: 2000}
	b := identityTestRow{dbID: 1, userID: 20, queryID: 42, topLevel: true, calls: 50, total: 500}
	rows := []identityTestRow{a, b}
	col.db = newFakeDB(t, identityResponder(t, 160000, &rows))
	for step, wantKnown := range []bool{false, false, true, false, true} {
		a.calls += 100
		a.total += 200
		b.calls += 5
		b.total += 50
		rows = []identityTestRow{a, b}
		if step == 1 || step == 2 {
			rows = rows[:1]
		}
		if err := col.scrape(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := col.samples[42][step]
		if !got.IntervalComputed || got.IntervalKnown != wantKnown || got.ResetDetected {
			t.Fatalf("step %d: got %+v; want computed, known=%v, no reset", step, got, wantKnown)
		}
		if !wantKnown && (got.IntervalCalls != 0 || got.IntervalTotalMs != 0 || got.IntervalMeanMs != 0) {
			t.Errorf("step %d: incomplete interval must not expose partial totals: %+v", step, got)
		}
		if wantKnown && got.IntervalCalls != int64(100+5*(len(rows)-1)) {
			t.Errorf("step %d: included calls from an unobserved interval: %+v", step, got)
		}
		if len(col.previousRows) != len(rows) {
			t.Errorf("step %d: retained %d predecessors for %d current rows", step, len(col.previousRows), len(rows))
		}
	}
}

func TestScrape_RowIdentityFailureDoesNotAdvanceBaseline(t *testing.T) {
	col := newTestCollector(t, Config{})
	rows := []identityTestRow{
		{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 1000, total: 2000},
		{dbID: 1, userID: 20, queryID: 42, topLevel: true, calls: 50, total: 500},
	}
	responder := identityResponder(t, 160000, &rows)
	badSecondRow := false
	col.db = newFakeDB(t, func(query string) ([]string, [][]driver.Value, error) {
		columns, values, err := responder(query)
		if badSecondRow && strings.Contains(query, "FROM pg_stat_statements") {
			values[1][0] = "not a database OID"
		}
		return columns, values, err
	})
	if err := col.scrape(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstTime := col.LastScrapeTime()
	rows[0].calls, rows[0].total = 1100, 2200
	rows[1].calls, rows[1].total = 55, 550
	badSecondRow = true
	if err := col.scrape(context.Background()); err == nil {
		t.Fatal("want scan error on second row")
	}
	if len(col.samples[42]) != 1 || !col.LastScrapeTime().Equal(firstTime) {
		t.Fatal("failed partial scrape advanced samples or successful-scrape timestamp")
	}
	badSecondRow = false
	if err := col.scrape(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := col.samples[42][1]
	if !got.IntervalKnown || got.IntervalCalls != 105 || !approxEqual(got.IntervalTotalMs, 250) {
		t.Fatalf("failed scrape advanced row predecessor: %+v", got)
	}
}

func TestScrape_RowIdentityRejectsExpiredPredecessor(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Minute})
	rows := []identityTestRow{{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 1000, total: 2000}}
	col.db = newFakeDB(t, identityResponder(t, 160000, &rows))
	if err := col.scrape(context.Background()); err != nil {
		t.Fatal(err)
	}
	for identity, previous := range col.previousRows {
		previous.RecordedAt = time.Now().Add(-time.Hour)
		col.previousRows[identity] = previous
	}
	rows[0].calls, rows[0].total = 1100, 2200
	if err := col.scrape(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := col.samples[42][1]; got.IntervalKnown || !got.IntervalComputed {
		t.Fatalf("expired baseline used to derive interval: %+v", got)
	}
	col.pruneAndUpdateMetrics(time.Now().Add(2 * time.Minute))
	if len(col.previousRows) != 0 {
		t.Fatal("retention pruning left stale row predecessors")
	}
}

func TestScrape_RowIdentityBackfillPreservesComputedIntervals(t *testing.T) {
	col := newTestCollector(t, Config{})
	base := time.Now().UTC().Add(-time.Minute)
	stored := []QuerySample{
		{QueryID: 42, QueryText: "SELECT 1", RecordedAt: base, Calls: 1050, TotalExecTimeMs: 2500, IntervalComputed: true},
		{QueryID: 42, QueryText: "SELECT 1", RecordedAt: base.Add(time.Second), Calls: 2005, TotalExecTimeMs: 4050, IntervalComputed: true, IntervalKnown: true, IntervalCalls: 1005, IntervalTotalMs: 2050, IntervalMeanMs: 2050.0 / 1005, ResetDetected: true},
		{QueryID: 42, QueryText: "SELECT 1", RecordedAt: base.Add(2 * time.Second), Calls: 2100, TotalExecTimeMs: 4200},
		{QueryID: 42, QueryText: "SELECT 1", RecordedAt: base.Add(3 * time.Second), Calls: 2200, TotalExecTimeMs: 4400},
	}
	col.Backfill(stored)
	got := col.samples[42]
	for i := 0; i < 2; i++ {
		stored[i].Fingerprint = FingerprintQuery(stored[i].QueryText)
		if !reflect.DeepEqual(got[i], stored[i]) {
			t.Fatalf("computed sample %d changed during backfill: %+v", i, got[i])
		}
	}
	if got[2].IntervalKnown || !got[3].IntervalKnown || got[3].IntervalCalls != 100 {
		t.Fatalf("backfill crossed computed/legacy boundary or broke legacy sequence: %+v", got[2:])
	}
	rows := []identityTestRow{{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 3000, total: 6000}}
	col.db = newFakeDB(t, identityResponder(t, 160000, &rows))
	if err := col.scrape(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sample := col.samples[42][4]; !sample.IntervalComputed || sample.IntervalKnown {
		t.Fatalf("aggregate backfill used as a live row predecessor: %+v", sample)
	}
}

func TestScrape_RowIdentityVersionColumnsAndRetry(t *testing.T) {
	for _, version := range []int64{120000, 130000, 140000, 160000, 170000} {
		col := newTestCollector(t, Config{})
		rows := []identityTestRow{{dbID: 1, userID: 10, queryID: 42, topLevel: true, calls: 10, total: 20}}
		responder := identityResponder(t, version, &rows)
		var selected string
		lookupFails := true
		col.db = newFakeDB(t, func(query string) ([]string, [][]driver.Value, error) {
			if lookupFails && strings.Contains(query, "server_version_num") {
				return nil, nil, errors.New("temporary version lookup failure")
			}
			if strings.Contains(query, "FROM pg_stat_statements") {
				selected = query
			}
			return responder(query)
		})
		if err := col.scrape(context.Background()); err == nil || selected != "" {
			t.Fatalf("version %d: failed lookup must not guess row identity", version)
		}
		lookupFails = false
		if err := col.scrape(context.Background()); err != nil {
			t.Fatalf("version %d: retry failed: %v", version, err)
		}
		wantTop := "true                        AS toplevel"
		if version >= 140000 {
			wantTop = "toplevel                        AS toplevel"
		}
		if !strings.Contains(selected, wantTop) || !strings.Contains(selected, "dbid,") || !strings.Contains(selected, "userid,") || !strings.Contains(selected, "LIMIT 500") {
			t.Errorf("version %d: unexpected identity/row-cap SQL: %s", version, selected)
		}
	}
}

func TestBackfill_PreservesKnownIntervalsWithoutNewMarker(t *testing.T) {
	col := newTestCollector(t, Config{})
	base := time.Now().UTC().Add(-time.Minute)
	sample := QuerySample{QueryID: 42, QueryText: "SELECT 1", RecordedAt: base,
		Calls: 2005, TotalExecTimeMs: 4050, IntervalKnown: true,
		IntervalCalls: 1005, IntervalTotalMs: 2050, IntervalMeanMs: 2050.0 / 1005, ResetDetected: true}
	col.Backfill([]QuerySample{sample})
	sample.Fingerprint = FingerprintQuery(sample.QueryText)
	if got := col.samples[42][0]; !reflect.DeepEqual(got, sample) {
		t.Fatalf("known interval without new marker was discarded: %+v", got)
	}
}
