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
	"fmt"
	"strings"
	"time"
)

// pgMajorVersion reads server_version_num (documented at
// https://www.postgresql.org/docs/current/runtime-config-preset.html,
// formatted MMmmpp for PostgreSQL 10+, e.g. 160003 = 16.3) and returns just
// the major version number. This mirrors the exact detection pattern
// internal/collector.Collector.resolveColumns uses for the same GUC, so the
// two packages agree on how "which major version is this" is derived.
func pgMajorVersion(ctx context.Context, db *sql.DB) (int, error) {
	var versionNum int
	err := db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&versionNum)
	if err != nil {
		return 0, fmt.Errorf("planner: check server_version_num: %w", err)
	}
	return versionNum / 10000, nil
}

// CaptureGenericPlan asks the planner to estimate a plan for queryText right
// now, via EXPLAIN (FORMAT JSON, GENERIC_PLAN), and returns it as a
// PlanSnapshot with Source set to SourceGenericPlan.
//
// GENERIC_PLAN was added in PostgreSQL 16
// (https://www.postgresql.org/docs/16/sql-explain.html: "GENERIC_PLAN ...
// Allow SQL statements that contain parameter placeholders to be explained
// without actually supplying parameter values"); on an older server this
// returns ErrUnsupportedVersion without attempting the EXPLAIN.
//
// The returned plan is only ever an estimate: it reflects the planner's
// current view of table statistics and available indexes, which may well
// have changed since whatever earlier, actually-slow execution this
// snapshot is meant to help explain. Prefer CapturePlanFromStorePlans (via
// the CapturePlan facade, which does this automatically) whenever
// pg_store_plans is installed and trustworthy — see package doc comment.
func CaptureGenericPlan(ctx context.Context, db *sql.DB, queryID int64, queryText NormalizedQueryText) (*PlanSnapshot, error) {
	if strings.TrimSpace(queryText.String()) == "" {
		return nil, fmt.Errorf("planner: CaptureGenericPlan: empty query text for queryid %d", queryID)
	}

	major, err := pgMajorVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	if major < 16 {
		return nil, ErrUnsupportedVersion
	}

	// EXPLAIN does not accept its target statement as a bind parameter; the
	// normalized query text from pg_stat_statements is interpolated
	// directly. This mirrors how any EXPLAIN-based tool must operate (there
	// is no parameterized form of the EXPLAIN command itself) and is safe
	// here specifically because queryText is expected to be the statement
	// pg_stat_statements already normalized and recorded — not raw,
	// unvalidated user input — the same trust boundary
	// internal/collector already relies on when it reads query text back
	// out of pg_stat_statements.
	stmt := fmt.Sprintf("EXPLAIN (FORMAT JSON, GENERIC_PLAN) %s", queryText.String())

	var planText string
	if err := db.QueryRowContext(ctx, stmt).Scan(&planText); err != nil {
		return nil, fmt.Errorf("planner: EXPLAIN (FORMAT JSON, GENERIC_PLAN) for queryid %d: %w", queryID, err)
	}

	rootType, totalCost, err := parsePlanJSON(planText)
	if err != nil {
		return nil, fmt.Errorf("planner: parse GENERIC_PLAN JSON for queryid %d: %w", queryID, err)
	}

	return &PlanSnapshot{
		QueryID:      queryID,
		RecordedAt:   time.Now().UTC(),
		RootNodeType: rootType,
		TotalCost:    totalCost,
		PlanJSON:     planText,
		Source:       SourceGenericPlan,
	}, nil
}
