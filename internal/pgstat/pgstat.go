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

// Package pgstat holds the plain data types and pure functions shared by the
// pg_stat_statements collector and the correlation engine. It deliberately
// imports only the standard library so that it can be compiled into small
// binaries, such as the WebAssembly build behind the docs playground.
package pgstat

import "time"

// QuerySample is one observation of a query's runtime statistics from
// pg_stat_statements.
type QuerySample struct {
	QueryID         int64
	QueryText       string
	Calls           int64
	TotalExecTimeMs float64
	MeanExecTimeMs  float64
	RecordedAt      time.Time

	// StatsSince is the pg_stat_statements.stats_since value of the entry
	// (PostgreSQL 17+; zero on older servers). It changes whenever the entry
	// is reset or evicted and recreated, which lets the Collector detect a
	// reset even when the counters have already climbed past their old values
	// by the next scrape.
	StatsSince time.Time

	// IntervalKnown reports whether the Interval* fields below are
	// meaningful. It is false for the first observation of a queryid (there
	// is no earlier snapshot to subtract) and for samples that did not come
	// from this Collector (for example the OTel/Prometheus source).
	IntervalKnown bool
	// IntervalCalls and IntervalTotalMs are the executions and total
	// execution time (ms) that accrued since the previous snapshot of this
	// queryid, that is, the delta of the cumulative counters.
	IntervalCalls   int64
	IntervalTotalMs float64
	// IntervalMeanMs is IntervalTotalMs / IntervalCalls: the mean latency of
	// only the calls made during this scrape interval. Unlike MeanExecTimeMs
	// it is not diluted by the query's entire history, so a regression shows
	// up in the very next sample. Zero when IntervalCalls is zero.
	IntervalMeanMs float64
	// ResetDetected is true when the counters were found to have restarted
	// since the previous snapshot (pg_stat_statements_reset(), an eviction,
	// or a crash). The Interval* fields then describe the activity since the
	// reset rather than a difference against the previous snapshot.
	ResetDetected bool

	// Fingerprint is the normalized-text hash produced by FingerprintQuery
	// for QueryText. It is additive metadata: existing consumers that only
	// read the original fields are unaffected. The Collector uses it
	// internally (see SamplesInRange) to reconnect a query across a queryid
	// change; it is exposed here too so future consumers can group samples
	// by query shape without recomputing the hash.
	Fingerprint string
}

// DeriveInterval fills cur's Interval* fields from the cumulative counters of
// the previous snapshot prev of the same queryid, and returns cur.
//
// pg_stat_statements' calls and total_exec_time only ever grow until the entry
// is reset (pg_stat_statements_reset, a crash, or eviction once
// pg_stat_statements.max is exceeded), and mean_exec_time is their ratio, so
// the mean is an average over the entire history since that reset. The change
// in that average from one scrape to the next shrinks as calls accumulate, and
// a regression that doubles a query's latency barely moves a mean built from
// millions of earlier calls. The latency of the calls made *between* two
// snapshots is the delta of the totals over the delta of the counts:
//
//	interval mean = (total_now - total_prev) / (calls_now - calls_prev)
//
// A reset is recognised from any of these signals:
//   - calls or total_exec_time went down;
//   - stats_since (PostgreSQL 17+) changed, which also catches a reset after
//     which the counters have already climbed past their old values.
//
// After a reset the counters restart from zero, so cur's own cumulative values
// are exactly the activity since the reset and are used as the interval.
func DeriveInterval(prev, cur QuerySample) QuerySample {
	reset := cur.Calls < prev.Calls || cur.TotalExecTimeMs < prev.TotalExecTimeMs
	if !cur.StatsSince.IsZero() && !prev.StatsSince.IsZero() && !cur.StatsSince.Equal(prev.StatsSince) {
		reset = true
	}

	dCalls, dTotal := cur.Calls-prev.Calls, cur.TotalExecTimeMs-prev.TotalExecTimeMs
	if reset {
		dCalls, dTotal = cur.Calls, cur.TotalExecTimeMs
	}

	cur.IntervalKnown = true
	cur.ResetDetected = reset
	cur.IntervalCalls = dCalls
	cur.IntervalTotalMs = dTotal
	cur.IntervalMeanMs = 0
	if dCalls > 0 {
		cur.IntervalMeanMs = dTotal / float64(dCalls)
	}
	return cur
}
