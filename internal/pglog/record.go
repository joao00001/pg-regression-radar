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

// Package pglog reads the statement durations a PostgreSQL server writes to its
// JSON log, labelled with the application_name of the session that ran them.
//
// It exists to tell two groups of clients apart (the canary pods and the stable
// pods of a rollout) on a database that offers no such split in
// pg_stat_statements. It needs nothing installed on the server: only
// compute_query_id and a sampled duration log, which are core settings.
//
// What it keeps is deliberately small: when a statement ran, which
// application_name ran it, its query_id (the same number pg_stat_statements
// uses, so a log line joins to a collected query) and how long it took. The
// statement text and the bind parameters, which can hold personal data, are
// never read into memory beyond the line that carried them.
package pglog

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Record is one statement execution seen in the log.
type Record struct {
	At         time.Time
	App        string // application_name of the session
	QueryID    int64  // pg_stat_statements' queryid
	DurationMs float64
}

// wire is the part of a jsonlog line this package looks at. CloudNativePG
// wraps the server's own record in {"logger":"postgres","record":{...}}.
type wire struct {
	Timestamp string          `json:"timestamp"`
	Message   string          `json:"message"`
	App       string          `json:"application_name"`
	QueryID   int64           `json:"query_id"`
	Record    json.RawMessage `json:"record"`
}

var durationPrefix = []byte("duration: ")

// ParseLine reads one line of the server's jsonlog. It reports false for
// anything that is not the duration of a statement that ran with a query_id:
// other messages, the parse and bind steps of the extended protocol, plans, and
// utility statements, which carry no query_id.
//
// A line whose timestamp cannot be trusted (a time zone abbreviation Go cannot
// resolve) is stamped with arrival, which is what a followed log is anyway.
func ParseLine(line []byte, arrival time.Time) (Record, bool) {
	// Cheap exit before decoding: nearly every line is not a duration.
	if !bytes.Contains(line, durationPrefix) {
		return Record{}, false
	}
	var w wire
	if err := json.Unmarshal(line, &w); err != nil {
		return Record{}, false
	}
	if len(w.Record) > 0 {
		// CloudNativePG: unwrap the server's record.
		var inner wire
		if err := json.Unmarshal(w.Record, &inner); err != nil {
			return Record{}, false
		}
		w = inner
	}
	if w.QueryID == 0 {
		return Record{}, false
	}
	ms, ok := statementDuration(w.Message)
	if !ok {
		return Record{}, false
	}
	return Record{At: parseTimestamp(w.Timestamp, arrival), App: w.App, QueryID: w.QueryID, DurationMs: ms}, true
}

// statementDuration extracts X from "duration: X ms  statement: ..." and
// "duration: X ms  execute <name>: ...". The parse and bind steps of the
// extended protocol, and "plan:" lines from auto_explain, are not executions.
func statementDuration(msg string) (float64, bool) {
	if !strings.HasPrefix(msg, "duration: ") {
		return 0, false
	}
	rest := msg[len("duration: "):]
	i := strings.Index(rest, " ms")
	if i <= 0 {
		return 0, false
	}
	ms, err := strconv.ParseFloat(rest[:i], 64)
	if err != nil || ms < 0 {
		return 0, false
	}
	what := strings.TrimLeft(rest[i+len(" ms"):], " ")
	if strings.HasPrefix(what, "statement:") || strings.HasPrefix(what, "execute ") {
		return ms, true
	}
	return 0, false
}

// parseTimestamp reads the server's "2026-11-25 21:35:46.681 -03" (or "... UTC").
// A zone it cannot resolve to an offset is not guessed at.
func parseTimestamp(s string, arrival time.Time) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05.000 -07", "2006-01-02 15:04:05 -07", "2006-01-02 15:04:05.000 -0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	if strings.HasSuffix(s, " UTC") || strings.HasSuffix(s, " GMT") {
		base := s[:len(s)-4]
		for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
			if t, err := time.ParseInLocation(layout, base, time.UTC); err == nil {
				return t
			}
		}
	}
	return arrival
}
