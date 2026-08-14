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

package cli_test

import (
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/cli"
)

// unreachableDSN is syntactically valid enough for the postgres driver to
// accept at sql.Open (which never dials — see collector.go's own comment on
// this), but connecting to it fails immediately: nothing listens on
// 127.0.0.1:1, so the OS refuses the connection right away rather than
// timing out. This is the same fast-fail DSN pattern
// internal/controller's tests already use for the equivalent reason.
const unreachableDSN = "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable"

func TestRunOperator_Version(t *testing.T) {
	t.Parallel()
	if got := cli.RunOperator([]string{"--version"}); got != 0 {
		t.Errorf("RunOperator(--version) = %d, want 0", got)
	}
}

func TestRunOperator_MissingDSN(t *testing.T) {
	t.Parallel()
	if got := cli.RunOperator([]string{}); got != 1 {
		t.Errorf("RunOperator() with no --dsn = %d, want 1", got)
	}
}

func TestRunOperator_UnknownFlag(t *testing.T) {
	t.Parallel()
	// flag.ContinueOnError means an unrecognised flag must be reported via
	// the returned exit code, not by killing the test process the way
	// flag.ExitOnError's os.Exit(2) would.
	if got := cli.RunOperator([]string{"--this-flag-does-not-exist"}); got != 2 {
		t.Errorf("RunOperator(--this-flag-does-not-exist) = %d, want 2", got)
	}
}

func TestRunOperator_Help(t *testing.T) {
	t.Parallel()
	// -h/--help makes flag.Parse return flag.ErrHelp; that's a clean,
	// successful exit (0), not a usage error (2).
	if got := cli.RunOperator([]string{"--help"}); got != 0 {
		t.Errorf("RunOperator(--help) = %d, want 0", got)
	}
}

func TestRunOperator_DryRun_BadSourceType(t *testing.T) {
	t.Parallel()
	got := cli.RunOperator([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
		"--source-type", "not-a-real-source-type",
	})
	if got != 1 {
		t.Errorf("RunOperator(--dry-run, bad --source-type) = %d, want 1", got)
	}
}

func TestRunOperator_DryRun_BadAlertFormat(t *testing.T) {
	t.Parallel()
	// Checked before any network I/O (source-type, then alerting config),
	// so this must fail deterministically regardless of --dsn reachability.
	got := cli.RunOperator([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
		"--alert-format", "not-a-real-format",
	})
	if got != 1 {
		t.Errorf("RunOperator(--dry-run, bad --alert-format) = %d, want 1", got)
	}
}

func TestRunOperator_DryRun_PagerDutyMissingRoutingKey(t *testing.T) {
	t.Parallel()
	got := cli.RunOperator([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
		"--alert-format", "pagerduty",
		// --pagerduty-routing-key deliberately omitted.
	})
	if got != 1 {
		t.Errorf("RunOperator(--dry-run, --alert-format=pagerduty, no routing key) = %d, want 1", got)
	}
}

func TestRunOperator_DryRun_PeriodicWindowMustBePositive(t *testing.T) {
	t.Parallel()
	// Also validated before any network I/O, so no real Postgres is needed
	// to exercise this branch.
	got := cli.RunOperator([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
		"--periodic-detection",
		"--periodic-window-minutes", "0",
	})
	if got != 1 {
		t.Errorf("RunOperator(--dry-run, --periodic-window-minutes=0) = %d, want 1", got)
	}
}

func TestRunOperator_DryRun_PeriodicIntervalMustBePositive(t *testing.T) {
	t.Parallel()
	got := cli.RunOperator([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
		"--periodic-detection",
		"--periodic-interval-minutes", "-5",
	})
	if got != 1 {
		t.Errorf("RunOperator(--dry-run, --periodic-interval-minutes=-5) = %d, want 1", got)
	}
}

// TestRunOperator_DryRun_PingFails is the one branch in this file that
// actually dials a socket: --dry-run's connectivity check
// (collector.Collector.Ping) against a DSN nothing listens on. 127.0.0.1:1
// refuses the connection immediately (no listener), so this stays fast —
// no real Postgres server, no timeout wait.
func TestRunOperator_DryRun_PingFails(t *testing.T) {
	t.Parallel()
	got := cli.RunOperator([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
	})
	if got != 1 {
		t.Errorf("RunOperator(--dry-run) against an unreachable DSN = %d, want 1", got)
	}
}

func TestRunOperator_DryRun_UnreadableAlertTemplateFile(t *testing.T) {
	t.Parallel()
	got := cli.RunOperator([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
		"--alert-format", "custom",
		"--alert-template-file", "/nonexistent/path/does-not-exist.tmpl",
	})
	if got != 1 {
		t.Errorf("RunOperator(--alert-template-file pointing nowhere) = %d, want 1", got)
	}
}
