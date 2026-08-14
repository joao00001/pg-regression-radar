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

func TestRunCollector_Version(t *testing.T) {
	t.Parallel()
	if got := cli.RunCollector([]string{"--version"}); got != 0 {
		t.Errorf("RunCollector(--version) = %d, want 0", got)
	}
}

func TestRunCollector_MissingDSN(t *testing.T) {
	t.Parallel()
	if got := cli.RunCollector([]string{}); got != 1 {
		t.Errorf("RunCollector() with no --dsn = %d, want 1", got)
	}
}

func TestRunCollector_UnknownFlag(t *testing.T) {
	t.Parallel()
	if got := cli.RunCollector([]string{"--this-flag-does-not-exist"}); got != 2 {
		t.Errorf("RunCollector(--this-flag-does-not-exist) = %d, want 2", got)
	}
}

func TestRunCollector_Help(t *testing.T) {
	t.Parallel()
	if got := cli.RunCollector([]string{"--help"}); got != 0 {
		t.Errorf("RunCollector(--help) = %d, want 0", got)
	}
}

func TestRunCollector_MaxQueryTextLenMustBePositive(t *testing.T) {
	t.Parallel()
	got := cli.RunCollector([]string{
		"--dsn", unreachableDSN,
		"--max-query-text-len", "0",
	})
	if got != 1 {
		t.Errorf("RunCollector(--max-query-text-len=0) = %d, want 1", got)
	}
}

// TestRunCollector_DryRun_PingFails mirrors
// TestRunOperator_DryRun_PingFails: 127.0.0.1:1 refuses the connection
// immediately (nothing listens there), so this stays fast with no real
// Postgres server needed.
func TestRunCollector_DryRun_PingFails(t *testing.T) {
	t.Parallel()
	got := cli.RunCollector([]string{
		"--dsn", unreachableDSN,
		"--dry-run",
	})
	if got != 1 {
		t.Errorf("RunCollector(--dry-run) against an unreachable DSN = %d, want 1", got)
	}
}
