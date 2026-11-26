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

package pglog

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Querier is the part of *sql.DB that Validate needs.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Issue is one reason the server's log cannot serve as an arm-labelled source.
type Issue struct {
	Setting string
	Got     string
	Problem string
	Fix     string
	// Warning is true for a setting that works but costs more than it should; the
	// log is still used. Any other issue stops it from being used.
	Warning bool
}

func (i Issue) String() string {
	return fmt.Sprintf("%s = %s: %s (fix: %s)", i.Setting, i.Got, i.Problem, i.Fix)
}

// Check is what Validate found: the sampling the server really uses, and the
// settings that prevent or distort the arm-labelled data.
type Check struct {
	Sampling Sampling
	Issues   []Issue
}

// OK is true when nothing prevents the log from being used. Warnings do not.
func (c Check) OK() bool {
	for _, i := range c.Issues {
		if !i.Warning {
			return false
		}
	}
	return true
}

// Validate reads the server's settings and says whether its log can be used.
// It never changes a setting.
func Validate(ctx context.Context, db Querier) (Check, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, setting FROM pg_settings WHERE name IN (
		'server_version_num','compute_query_id','shared_preload_libraries','log_destination',
		'log_min_duration_statement','log_min_duration_sample','log_statement_sample_rate')`)
	if err != nil {
		return Check{}, fmt.Errorf("read pg_settings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	set := map[string]string{}
	for rows.Next() {
		var n, v string
		if err := rows.Scan(&n, &v); err != nil {
			return Check{}, err
		}
		set[n] = v
	}
	if err := rows.Err(); err != nil {
		return Check{}, err
	}
	return CheckSettings(set), nil
}

// CheckSettings applies the rules to a map of pg_settings values. It is separate
// from Validate so the rules can be tested without a server.
func CheckSettings(set map[string]string) Check {
	var c Check
	bad := func(name, problem, fix string) {
		c.Issues = append(c.Issues, Issue{Setting: name, Got: set[name], Problem: problem, Fix: fix})
	}
	warn := func(name, problem, fix string) {
		c.Issues = append(c.Issues, Issue{Setting: name, Got: set[name], Problem: problem, Fix: fix, Warning: true})
	}

	if v, _ := strconv.Atoi(set["server_version_num"]); v != 0 && v < 150000 {
		bad("server_version_num", "the JSON log needs PostgreSQL 15 or newer", "upgrade, or leave the arm-labelled source off")
	}

	switch qid := set["compute_query_id"]; qid {
	case "on":
	case "auto":
		if !strings.Contains(set["shared_preload_libraries"], "pg_stat_statements") {
			bad("compute_query_id", "auto computes no query id unless an extension asks for it, and pg_stat_statements is not preloaded", "set compute_query_id = on")
		}
	default:
		bad("compute_query_id", "the log carries no query_id, so its lines cannot be joined to the collected queries", "set compute_query_id = on")
	}

	if d := set["log_destination"]; d != "" && !strings.Contains(d, "jsonlog") && !strings.Contains(d, "csvlog") {
		bad("log_destination", "the log is not structured; application_name and query_id cannot be read from plain text", "add jsonlog: log_destination = 'jsonlog' (or 'stderr,jsonlog')")
	}

	c.Sampling = Sampling{Rate: 1, AlwaysAtOrAboveMs: -1}
	if v, err := strconv.ParseFloat(set["log_statement_sample_rate"], 64); err == nil {
		c.Sampling.Rate = v
		switch {
		case v <= 0:
			bad("log_statement_sample_rate", "no statement is sampled", "set a small rate, for example 0.01, and raise it on a quiet server")
		case v > 0.25:
			warn("log_statement_sample_rate", "this logs a large share of all statements, which costs the database and the log pipeline", "lower it (0.01 to 0.05 is plenty for a busy server)")
		}
	}
	if v, err := strconv.ParseFloat(set["log_min_duration_statement"], 64); err == nil {
		c.Sampling.AlwaysAtOrAboveMs = v
	}
	switch v, err := strconv.ParseFloat(set["log_min_duration_sample"], 64); {
	case err != nil:
	case v < 0:
		bad("log_min_duration_sample", "sampling is off", "set log_min_duration_sample = 0")
	case v > 0:
		bad("log_min_duration_sample", "only statements slower than this are sampled, so the fast ones that make most of the traffic are missing and a regression is seen only past the threshold", "set log_min_duration_sample = 0")
	}
	return c
}
