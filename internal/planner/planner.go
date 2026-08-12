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

// Package planner captures periodic query-plan snapshots via PostgreSQL 16's
// EXPLAIN (GENERIC_PLAN) and diffs them, so a detected latency regression can
// come with a short human-readable hint about *why* the plan may have
// changed — see docs/detection-algorithm.md's "Plan-diff correlation"
// section for the full design and its documented scope boundary.
package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PlanSnapshot is a point-in-time summary of a query's execution plan, as
// captured by CapturePlan. PlanJSON retains the full EXPLAIN output for
// callers that want more than the root-node summary (e.g. future,
// out-of-scope UI work); RootNodeType/TotalCost are the only fields Diff
// currently reads.
type PlanSnapshot struct {
	QueryID      int64
	RecordedAt   time.Time
	RootNodeType string
	TotalCost    float64
	PlanJSON     string
}

// ErrUnsupportedVersion is returned by CapturePlan when the target server is
// older than PostgreSQL 16, the first version to support
// "EXPLAIN (GENERIC_PLAN)" (see CapturePlan's doc comment for why this
// package relies specifically on that feature). Callers should check for
// this with errors.Is and treat it as "plan capture unavailable on this
// server" — log once at Info level and otherwise continue exactly as before,
// not treat it as a per-scrape failure. CloudNativePG supports PostgreSQL
// 14+, so this is an expected, non-exceptional outcome on some clusters.
var ErrUnsupportedVersion = errors.New("planner: EXPLAIN (GENERIC_PLAN) requires PostgreSQL 16 or newer")

// explainRow mirrors the top-level shape of `EXPLAIN (FORMAT JSON) ...`
// output: a JSON array with one element (barring EXPLAIN ANALYZE's
// additional per-statement rows, which GENERIC_PLAN never produces since it
// cannot execute the query), wrapping the plan tree under "Plan".
type explainRow struct {
	Plan explainNode `json:"Plan"`
}

// explainNode mirrors one node of the plan tree. Only the fields this
// package actually reads are declared; encoding/json silently ignores the
// many others (e.g. "Startup Cost", "Plan Rows", "Relation Name") that a
// future enhancement could read without needing to change this shape.
type explainNode struct {
	NodeType  string        `json:"Node Type"`
	TotalCost float64       `json:"Total Cost"`
	Plans     []explainNode `json:"Plans,omitempty"`
}

// CapturePlan runs `EXPLAIN (FORMAT JSON, GENERIC_PLAN) <queryText>` against
// db and returns a PlanSnapshot summarising the resulting plan's root node.
//
// Why GENERIC_PLAN, and not a plain EXPLAIN: pg_stat_statements normalises
// query text, replacing every literal value with a "$1"/"$2"/... placeholder
// (see internal/collector/fingerprint.go's doc comment for the full citation
// trail on that normalization). A plain EXPLAIN cannot run against that text
// without real bound parameter values — which pg_stat_statements never
// records — because the planner has no way to resolve "$1" to a concrete
// value or even infer its type from context alone; attempting it fails with
// "there is no parameter $1". PostgreSQL 16 added EXPLAIN's GENERIC_PLAN
// option specifically to solve this: it plans the query using the planner's
// normal type inference and default (parameter-independent) selectivity
// estimates for each unbound parameter, without requiring any real values
// (see pganalyze's writeup, https://pganalyze.com/blog/5mins-postgres-explain-generic-plan,
// and Cybertec's, https://www.cybertec-postgresql.com/en/explain-generic-plan-postgresql-16/,
// both describing this as the feature's intended use case). Below PostgreSQL
// 16 there is no supported way to EXPLAIN captured pg_stat_statements text at
// all without either a real parameter-value capture pipeline (auto_explain's
// log output, or the pg_store_plans extension) or a fragile literal-
// substitution hack this package deliberately avoids (fragile because a
// substituted literal can mismatch the parameter's real type, and because
// pg_stat_statements never records what the original literal even was) — see
// ErrUnsupportedVersion.
//
// queryText is expected to come directly from pg_stat_statements (i.e.
// already containing "$1"-style placeholders) — that is the whole point of
// GENERIC_PLAN. A query GENERIC_PLAN genuinely cannot handle — per its own
// documented limitations, e.g. a parameter used in a structural position
// (LIMIT via a prepared value in some contexts) or an ambiguous function
// overload GENERIC_PLAN can't resolve without a concrete type — surfaces
// here as a plain error; callers should log it at Debug and move on to the
// next query, never treat one query's plan-capture failure as fatal to the
// scrape loop.
func CapturePlan(ctx context.Context, db *sql.DB, queryID int64, queryText string) (*PlanSnapshot, error) {
	major, err := serverMajorVersion(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("planner: determine server version: %w", err)
	}
	if major < 16 {
		return nil, fmt.Errorf("%w (server major version %d)", ErrUnsupportedVersion, major)
	}

	const explainPrefix = `EXPLAIN (FORMAT JSON, GENERIC_PLAN) `
	var raw string
	if err := db.QueryRowContext(ctx, explainPrefix+queryText).Scan(&raw); err != nil {
		return nil, fmt.Errorf("planner: explain (generic_plan) query_id=%d: %w", queryID, err)
	}

	var rows []explainRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("planner: parse explain json for query_id=%d: %w", queryID, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("planner: explain returned no plan rows for query_id=%d", queryID)
	}

	root := rows[0].Plan
	return &PlanSnapshot{
		QueryID:      queryID,
		RecordedAt:   time.Now().UTC(),
		RootNodeType: root.NodeType,
		TotalCost:    root.TotalCost,
		PlanJSON:     raw,
	}, nil
}

// serverMajorVersion mirrors the exact server_version_num detection pattern
// already used by internal/collector.Collector.resolveColumns:
// server_version_num is formatted MMmmpp for PostgreSQL 10+ (e.g. 160003 =
// 16.3), so dividing by 10000 yields the major version.
func serverMajorVersion(ctx context.Context, db *sql.DB) (int, error) {
	var versionNum int
	if err := db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&versionNum); err != nil {
		return 0, err
	}
	return versionNum / 10000, nil
}

// costChangeRatioThreshold bounds how far TotalCost must move, relatively,
// before Diff calls it out. Postgres's cost estimates are never bit-for-bit
// stable between two GENERIC_PLAN calls of a genuinely unchanged plan (row
// estimates drift slightly as table statistics get re-sampled by autovacuum,
// for instance), so a fixed 10% band separates "the planner picked a
// meaningfully different strategy" from that background noise.
const costChangeRatioThreshold = 1.10

// Diff returns a short, human-readable summary of what changed between two
// plan snapshots of the same query, suitable for direct inclusion in a Slack
// alert (see internal/alerting). Returns "" if either snapshot is nil —
// there is nothing meaningful to say without both sides, and this lets
// callers treat "" as "no plan-diff available" without a separate check.
//
// When both snapshots are present but nothing meaningfully changed, this
// still returns a short, explicit statement to that effect rather than "",
// because "" would otherwise be ambiguous with "no snapshots were
// available at all" from the caller's point of view.
func Diff(before, after *PlanSnapshot) string {
	if before == nil || after == nil {
		return ""
	}

	var parts []string
	if before.RootNodeType != after.RootNodeType {
		parts = append(parts, fmt.Sprintf("root plan node changed from %s to %s", before.RootNodeType, after.RootNodeType))
	}

	switch {
	case before.TotalCost > 0 && after.TotalCost > 0:
		ratio := after.TotalCost / before.TotalCost
		switch {
		case ratio >= costChangeRatioThreshold:
			parts = append(parts, fmt.Sprintf("estimated cost increased %.1fx", ratio))
		case ratio <= 1/costChangeRatioThreshold:
			parts = append(parts, fmt.Sprintf("estimated cost decreased %.1fx", 1/ratio))
		}
	case before.TotalCost == 0 && after.TotalCost > 0:
		parts = append(parts, fmt.Sprintf("estimated cost went from 0 to %.1f", after.TotalCost))
	}

	if len(parts) == 0 {
		return "plan shape unchanged; cost roughly stable"
	}
	return strings.Join(parts, "; ")
}
