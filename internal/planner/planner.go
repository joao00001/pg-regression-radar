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

// Package planner captures a PostgreSQL query's execution plan and diffs two
// captures against each other, so a detected latency regression (see
// internal/correlation) can be reported alongside "here's what the plan
// looked like before and after" instead of just "it got slower".
//
// There are two, deliberately non-equivalent, ways to get a plan for a
// queryid, in order of preference:
//
//  1. pg_store_plans (see storeplans.go) — a contrib-style extension
//     (https://github.com/ossc-db/pg_store_plans) that records the REAL
//     plan of each execution, the same way pg_stat_statements records real
//     execution statistics. This is what actually ran.
//  2. EXPLAIN (FORMAT JSON, GENERIC_PLAN) (see genericplan.go), added in
//     PostgreSQL 16 (https://www.postgresql.org/docs/16/sql-explain.html),
//     which asks the planner to estimate a plan for a parameterized query
//     *right now*, without ever executing it. This is a best-effort
//     approximation: table statistics, index availability, and even the
//     query's own selectivity-relevant literal values (GENERIC_PLAN plans
//     without them) may have changed since the original slow execution
//     actually ran, so the plan it returns is not guaranteed to be the plan
//     that caused the regression.
//
// CapturePlan tries source 1 first and falls back to source 2; see its doc
// comment and docs/detection-algorithm.md for the full rationale, including
// the queryid-compatibility research behind why source 1 is sometimes
// skipped even when the extension is installed.
package planner

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Source identifies which of the two capture paths produced a PlanSnapshot.
// It is carried on the snapshot itself (not just logged at capture time)
// because it affects how much an alert consuming the resulting Diff should
// trust the plan it's showing: SourceStorePlans reflects a real execution,
// SourceGenericPlan is an estimate. See the package doc comment.
type Source string

const (
	// SourceStorePlans marks a PlanSnapshot captured from pg_store_plans's
	// real, previously-executed plan for a queryid.
	SourceStorePlans Source = "pg_store_plans"

	// SourceGenericPlan marks a PlanSnapshot captured via
	// EXPLAIN (FORMAT JSON, GENERIC_PLAN), i.e. an estimate produced at
	// analysis time rather than recorded at execution time.
	SourceGenericPlan Source = "generic_plan"
)

// PlanSnapshot is one captured execution plan for a queryid, at a point in
// time. A regression alert typically holds two of these — one from before
// the suspected deploy, one from after — and passes them to Diff.
type PlanSnapshot struct {
	// QueryID is the pg_stat_statements-compatible query identifier this
	// plan was captured for. See storeplans.go's package-level doc comment
	// for why this is "pg_stat_statements-compatible" and not simply
	// "pg_store_plans' queryid" — the two are not always the same thing.
	QueryID int64

	// RecordedAt is when this snapshot was captured (i.e. when this
	// package ran the query/read the view row), not necessarily when the
	// underlying plan was originally executed — pg_store_plans rows
	// persist across many executions between first_call and last_call, so
	// for Source == SourceStorePlans the real plan may be older than
	// RecordedAt by as much as the analysis window.
	RecordedAt time.Time

	// RootNodeType is the "Node Type" of the plan's root node (e.g.
	// "Seq Scan", "Index Scan", "Hash Join"), extracted from PlanJSON. A
	// change here across two snapshots is usually the single most
	// human-readable signal of "the plan changed shape".
	RootNodeType string

	// TotalCost is the planner's estimated total cost for the root node,
	// extracted from PlanJSON. For SourceStorePlans this is the estimate
	// that was current at capture time, not a measured runtime cost —
	// pg_store_plans does not expose actual per-plan cost, only actual
	// timing/row/buffer counters (see storeplans.go).
	TotalCost float64

	// PlanJSON is the full plan, as JSON text, exactly as returned by
	// whichever Source produced it. Kept verbatim (not just the two fields
	// above) so a caller building a richer alert has the whole tree
	// available, not just the root node summary.
	PlanJSON string

	// Source records which capture path produced this snapshot. See the
	// Source type's doc comment.
	Source Source
}

// Sentinel errors returned by the Capture* functions in this package.
// Callers should compare against these with errors.Is rather than on
// string content, since the wrapping messages carry additional
// (unstable) diagnostic detail.
var (
	// ErrUnsupportedVersion is returned by CaptureGenericPlan when the
	// target server predates PostgreSQL 16, i.e. it does not support
	// EXPLAIN's GENERIC_PLAN option at all.
	ErrUnsupportedVersion = errors.New("planner: EXPLAIN (FORMAT JSON, GENERIC_PLAN) requires PostgreSQL 16 or newer")

	// ErrExtensionNotInstalled is returned by CapturePlanFromStorePlans
	// when pg_store_plans has not been installed (CREATE EXTENSION
	// pg_store_plans) in the target database.
	ErrExtensionNotInstalled = errors.New("planner: pg_store_plans extension is not installed")

	// ErrQueryIDUnreliable is returned by CapturePlanFromStorePlans when
	// pg_store_plans IS installed, but this package cannot trust that its
	// queryid values correlate with pg_stat_statements' queryid for the
	// combination of extension version, server version, and
	// compute_query_id setting it detected. See storeplans.go's doc
	// comment on detectStorePlans, and docs/detection-algorithm.md, for
	// the compatibility research this is based on.
	ErrQueryIDUnreliable = errors.New("planner: pg_store_plans queryid cannot be trusted to correlate with pg_stat_statements queryid")

	// ErrNoPlanRecorded is returned by CapturePlanFromStorePlans when the
	// extension is installed and its queryid is trusted, but no row exists
	// for the requested queryid — e.g. the query has not executed since
	// the last pg_store_plans_reset(), or its entry was evicted because
	// more distinct plans than pg_store_plans.max were observed.
	ErrNoPlanRecorded = errors.New("planner: no pg_store_plans entry found for queryid")
)

// Diff renders a human-readable summary of what changed between two
// PlanSnapshots of the same queryid, for inclusion in a regression alert.
// Either argument may be nil (e.g. no plan could be captured on one side),
// in which case Diff says so rather than panicking.
func Diff(before, after *PlanSnapshot) string {
	if before == nil && after == nil {
		return "plan diff: no plan captured on either side of the regression."
	}
	if before == nil {
		return fmt.Sprintf("plan diff: no plan captured before the regression; after (%s source): %s, total cost %.2f.",
			after.Source, after.RootNodeType, after.TotalCost)
	}
	if after == nil {
		return fmt.Sprintf("plan diff: no plan captured after the regression; before (%s source): %s, total cost %.2f.",
			before.Source, before.RootNodeType, before.TotalCost)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "plan diff for queryid %d:\n", after.QueryID)

	if before.RootNodeType != after.RootNodeType {
		fmt.Fprintf(&b, "  root node type changed: %q -> %q\n", before.RootNodeType, after.RootNodeType)
	} else {
		fmt.Fprintf(&b, "  root node type unchanged: %q\n", after.RootNodeType)
	}

	switch {
	case before.TotalCost > 0:
		delta := (after.TotalCost - before.TotalCost) / before.TotalCost * 100
		fmt.Fprintf(&b, "  total cost: %.2f -> %.2f (%+.1f%%)\n", before.TotalCost, after.TotalCost, delta)
	default:
		fmt.Fprintf(&b, "  total cost: %.2f -> %.2f\n", before.TotalCost, after.TotalCost)
	}

	fmt.Fprintf(&b, "  source: %s -> %s\n", before.Source, after.Source)

	if before.Source == SourceGenericPlan || after.Source == SourceGenericPlan {
		b.WriteString("  caution: at least one snapshot is an EXPLAIN (GENERIC_PLAN) estimate, not a plan pg_store_plans recorded from a real execution — planner statistics may have changed since the original slow run, so this diff may not reflect the actual root cause.\n")
	}

	return b.String()
}

// parsePlanJSON extracts the root node's "Node Type" and "Total Cost" from a
// plan JSON document, tolerating the two shapes this package's two sources
// can produce:
//
//   - EXPLAIN (FORMAT JSON, ...) always wraps its output in a one-element
//     array of objects, each shaped like {"Plan": {...}} — see the
//     PostgreSQL documentation for EXPLAIN's JSON output format
//     (https://www.postgresql.org/docs/current/sql-explain.html, "The
//     output format is FORMAT JSON").
//   - pg_store_plans' pg_store_plans.plan_format = 'json' renders the
//     single stored plan for a row directly; based on the module's own
//     rendering functions (pg_store_plans_jsonplan(), documented at
//     https://ossc-db.github.io/pg_store_plans/pg_store_plans.html) this is
//     not documented to be wrapped in the same one-element array, so this
//     parser also accepts a bare {"Plan": {...}} object, and — as a last
//     resort, since the exact unwrapped shape is not guaranteed by
//     upstream's documentation — a bare plan-node object with no "Plan"
//     wrapper key at all.
func parsePlanJSON(raw string) (rootNodeType string, totalCost float64, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, errors.New("empty plan JSON")
	}

	var planNode map[string]any

	var wrapped []map[string]any
	if jerr := json.Unmarshal([]byte(raw), &wrapped); jerr == nil && len(wrapped) > 0 {
		planNode = wrapped[0]
	} else {
		var bare map[string]any
		if jerr := json.Unmarshal([]byte(raw), &bare); jerr != nil {
			return "", 0, fmt.Errorf("unrecognized plan JSON shape: %w", jerr)
		}
		planNode = bare
	}

	plan, ok := planNode["Plan"].(map[string]any)
	if !ok {
		// No "Plan" wrapper key present; treat the top-level object itself
		// as the root plan node (see doc comment above).
		plan = planNode
	}

	nt, _ := plan["Node Type"].(string)
	if nt == "" {
		return "", 0, errors.New(`plan JSON missing "Node Type"`)
	}
	tc, _ := plan["Total Cost"].(float64)

	return nt, tc, nil
}
