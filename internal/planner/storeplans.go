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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// storePlansMinReliableVersion is the earliest pg_store_plans release whose
// "queryid" column is documented to be the same core-generated identifier
// pg_stat_statements uses, rather than an independently computed one.
//
// This is a researched fact, not an assumption: pg_store_plans 1.6's own
// release notes (https://github.com/ossc-db/pg_store_plans/releases/tag/1.6)
// say "Removed the column queryid_stat_statements and now the column
// queryid shows the core generated query id that is controlled by
// compute_query_id." Before 1.6, the module computed its own queryid "to
// identify the source query similarly to pg_stat_statements but in a
// different algorithm" (still the wording in the upstream HTML docs' prose
// today: https://ossc-db.github.io/pg_store_plans/pg_store_plans.html) and
// exposed the separate queryid_stat_statements column specifically because
// its own queryid was NOT safe to join against pg_stat_statements directly.
// That prose sentence looks like a leftover from before 1.6 — it directly
// contradicts the same page's Table 1, which describes the current queryid
// column as "Core-generated query ID ... usable as the join key with
// pg_stat_statements" — but out of caution this package treats pre-1.6
// installs as unreliable rather than trusting the (self-contradictory)
// documentation.
var storePlansMinReliableVersion = [2]int{1, 6}

// storePlansMinReliablePGMajor is the earliest PostgreSQL major version the
// upstream docs explicitly claim queryid-based joining works for: "For
// PostgreSQL 14 or later, you can find the corresponding query for a
// pg_store_plans entry in pg_stat_statements by joining using queryid."
// (same page as above). PostgreSQL 14 is also the first version with the
// core compute_query_id GUC that pg_store_plans >= 1.6 relies on for its
// queryid, so this threshold is consistent with storePlansMinReliableVersion
// rather than an independent guess.
const storePlansMinReliablePGMajor = 14

// storePlansStatus is the result of feature-detecting pg_store_plans: is it
// installed at all, and — if so — can this package trust its queryid to
// mean the same thing as pg_stat_statements' queryid for THIS server.
type storePlansStatus struct {
	installed bool
	// reliable is false when installed is true but queryid correlation is
	// not trusted; reason then explains why (see detectStorePlans).
	reliable bool
	reason   string
}

// extVersionAtLeast reports whether extversion (e.g. "1.8", "1.10", or an
// unexpected value like "1.6.1") is >= the given major.minor, comparing
// numerically rather than lexically so "1.10" correctly compares as newer
// than "1.6" (a plain string comparison would get this backwards). Any
// version string this package can't parse at all is treated as NOT meeting
// the threshold — i.e. fails closed into the "unreliable" path — since an
// unparseable version number is itself a reason for caution.
func extVersionAtLeast(extversion string, wantMajor, wantMinor int) bool {
	parts := strings.SplitN(extversion, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	if major != wantMajor {
		return major > wantMajor
	}
	return minor >= wantMinor
}

// detectStorePlans feature-detects pg_store_plans the same way the rest of
// this project detects optional server capabilities (see
// internal/collector.Collector.Ping's pg_extension check and
// resolveColumns's server_version_num check, which this function follows
// the same shape as), and additionally judges whether its queryid can be
// trusted to correlate with pg_stat_statements' queryid — see the package
// doc comment and docs/detection-algorithm.md for why that judgment is
// necessary at all rather than being a given.
//
// Three independent things all have to hold for queryid to be trustworthy:
//
//  1. The installed pg_store_plans version is >= storePlansMinReliableVersion
//     (1.6), the release that switched queryid to the core-generated ID.
//  2. The server is PostgreSQL >= storePlansMinReliablePGMajor (14), the
//     version upstream's docs explicitly claim this join works on.
//  3. compute_query_id is not "off". Per upstream docs: "pg_store_plans
//     requires the GUC variable compute_query_id to be 'on' or 'auto'. If
//     it is set to 'no' [sic — the GUC's actual off-value is 'off'],
//     pg_store_plans is silently disabled." A silently-disabled extension
//     can still be present in pg_extension and even still have stale rows
//     from before it was disabled, so this must be checked explicitly
//     rather than inferred from installation alone.
func detectStorePlans(ctx context.Context, db *sql.DB) (storePlansStatus, error) {
	var extversion string
	err := db.QueryRowContext(ctx,
		`SELECT extversion FROM pg_extension WHERE extname = 'pg_store_plans'`,
	).Scan(&extversion)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return storePlansStatus{installed: false}, nil
	case err != nil:
		return storePlansStatus{}, fmt.Errorf("planner: check pg_store_plans extension: %w", err)
	}

	if !extVersionAtLeast(extversion, storePlansMinReliableVersion[0], storePlansMinReliableVersion[1]) {
		return storePlansStatus{
			installed: true,
			reliable:  false,
			reason: fmt.Sprintf(
				"pg_store_plans %s predates %d.%d, which introduced a core-compatible queryid; its queryid may use pg_store_plans' own algorithm instead and cannot safely be joined against pg_stat_statements",
				extversion, storePlansMinReliableVersion[0], storePlansMinReliableVersion[1],
			),
		}, nil
	}

	major, err := pgMajorVersion(ctx, db)
	if err != nil {
		return storePlansStatus{}, err
	}
	if major < storePlansMinReliablePGMajor {
		return storePlansStatus{
			installed: true,
			reliable:  false,
			reason: fmt.Sprintf(
				"server is PostgreSQL %d, older than the PostgreSQL %d that pg_store_plans documents queryid-joining against pg_stat_statements as supported",
				major, storePlansMinReliablePGMajor,
			),
		}, nil
	}

	var computeQueryID string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('compute_query_id')`).Scan(&computeQueryID); err != nil {
		// current_setting errors if the GUC doesn't exist at all (e.g. a
		// pre-14 server, already excluded above, but defend in depth) or if
		// it's genuinely unreadable; either way, we can't confirm queryid
		// is trustworthy, so fail closed into "unreliable" rather than
		// erroring out CapturePlanFromStorePlans entirely — the caller can
		// still fall back to generic_plan.
		return storePlansStatus{
			installed: true,
			reliable:  false,
			reason:    fmt.Sprintf("could not read compute_query_id: %v", err),
		}, nil
	}
	if computeQueryID == "off" {
		return storePlansStatus{
			installed: true,
			reliable:  false,
			reason:    "compute_query_id is 'off'; pg_store_plans is silently disabled per upstream docs, and any existing rows may predate that setting and cannot be trusted",
		}, nil
	}

	return storePlansStatus{installed: true, reliable: true}, nil
}

// CapturePlanFromStorePlans reads the most recently captured, actually
// EXECUTED plan for queryID from the pg_store_plans view
// (https://github.com/ossc-db/pg_store_plans), returning it as a
// PlanSnapshot with Source set to SourceStorePlans.
//
// It returns:
//   - ErrExtensionNotInstalled if pg_store_plans is not installed.
//   - ErrQueryIDUnreliable (wrapped with a specific reason) if it IS
//     installed but this package cannot trust its queryid to mean the same
//     thing as pg_stat_statements' queryid — see detectStorePlans.
//   - ErrNoPlanRecorded if the extension is installed and trusted, but has
//     no row for queryID yet.
//   - a wrapped, non-sentinel error for any other database failure.
//
// Most callers should use CapturePlan instead, which falls back to
// CaptureGenericPlan automatically on any of the above.
func CapturePlanFromStorePlans(ctx context.Context, db *sql.DB, queryID int64) (*PlanSnapshot, error) {
	status, err := detectStorePlans(ctx, db)
	if err != nil {
		return nil, err
	}
	if !status.installed {
		return nil, ErrExtensionNotInstalled
	}
	if !status.reliable {
		return nil, fmt.Errorf("%w: %s", ErrQueryIDUnreliable, status.reason)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("planner: begin tx to read pg_store_plans: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// pg_store_plans stores each plan in an internal representation and
	// renders it into pg_store_plans.plan_format at read time (per upstream
	// docs, plan_format is what "controls the format of plans in
	// pg_store_plans", and 'raw' is documented as being fed to
	// pg_store_plans_*plan() render functions on demand — i.e. rendering
	// happens per read, not per capture). SET LOCAL scopes this to the
	// current transaction only, so we don't durably change a
	// cluster-wide/session-wide setting other callers rely on.
	if _, err := tx.ExecContext(ctx, `SET LOCAL pg_store_plans.plan_format = 'json'`); err != nil {
		return nil, fmt.Errorf("planner: set pg_store_plans.plan_format: %w", err)
	}

	const q = `
SELECT plan
FROM pg_store_plans
WHERE queryid = $1
ORDER BY last_call DESC
LIMIT 1
`
	var planText string
	err = tx.QueryRowContext(ctx, q, queryID).Scan(&planText)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("%w: queryid %d", ErrNoPlanRecorded, queryID)
	case err != nil:
		return nil, fmt.Errorf("planner: query pg_store_plans for queryid %d: %w", queryID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("planner: commit pg_store_plans read: %w", err)
	}

	rootType, totalCost, err := parsePlanJSON(planText)
	if err != nil {
		return nil, fmt.Errorf("planner: parse pg_store_plans plan JSON for queryid %d: %w", queryID, err)
	}

	return &PlanSnapshot{
		QueryID:      queryID,
		RecordedAt:   time.Now().UTC(),
		RootNodeType: rootType,
		TotalCost:    totalCost,
		PlanJSON:     planText,
		Source:       SourceStorePlans,
	}, nil
}

// CapturePlan is the facade most callers should use: it captures a plan for
// queryID, preferring the REAL, previously-executed plan from
// pg_store_plans (CapturePlanFromStorePlans) over the ESTIMATED plan from
// EXPLAIN GENERIC_PLAN (CaptureGenericPlan, using queryText — normalized
// query text as read from pg_stat_statements).
//
// It falls back to CaptureGenericPlan on ANY failure from
// CapturePlanFromStorePlans — not installed, installed but untrusted
// (ErrQueryIDUnreliable), no row yet (ErrNoPlanRecorded), or an unexpected
// database error. This is a deliberate choice to favor availability over
// strictness: a transient pg_store_plans read failure (say, a lock or a
// momentarily unreadable GUC) should not prevent showing at least an
// estimated plan in an alert. Callers that need to distinguish "used a real
// plan" from "had to estimate" should inspect the returned snapshot's
// Source field rather than branching on CapturePlan's error.
//
// An error is returned only if BOTH sources fail; it wraps the
// CaptureGenericPlan error (via %w, so errors.Is(err, ErrUnsupportedVersion)
// still works) and includes the pg_store_plans failure as context.
func CapturePlan(ctx context.Context, db *sql.DB, queryID int64, queryText string) (*PlanSnapshot, error) {
	snap, storeErr := CapturePlanFromStorePlans(ctx, db, queryID)
	if storeErr == nil {
		return snap, nil
	}

	generic, genErr := CaptureGenericPlan(ctx, db, queryID, queryText)
	if genErr != nil {
		return nil, fmt.Errorf("planner: no plan available for queryid %d (pg_store_plans: %v): %w", queryID, storeErr, genErr)
	}
	return generic, nil
}
