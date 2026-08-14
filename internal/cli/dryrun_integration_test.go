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

//go:build integration

// operator_test.go/collector_test.go's --dry-run tests all use a
// deliberately unreachable DSN, so they can only ever prove the *failure*
// branches (bad flag, ping refused, and so on) — none of them proves a
// --dry-run actually succeeds end to end against a real, reachable
// Postgres. This file closes that gap the same way
// internal/collector/collector_integration_test.go does: gated behind the
// `integration` build tag and PGRR_TEST_DSN, so `go test ./...` (no tag)
// stays safe with no database available, and CI's `integration-postgres`
// matrix (postgres:16/17/18 — see .github/workflows/ci.yml) exercises it
// for real on every PR.
//
// Run it explicitly the same way as the Collector integration tests:
//
//	docker run --rm -d --name pgrr-cli-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/cli/...
package cli_test

import (
	"os"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/cli"
)

func TestRunOperator_DryRun_SucceedsAgainstRealPostgres(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping operator --dry-run integration test")
	}

	if got := cli.RunOperator([]string{"--dsn", dsn, "--dry-run"}); got != 0 {
		t.Errorf("RunOperator(--dry-run) against a real, reachable Postgres = %d, want 0", got)
	}
}

// TestRunOperator_DryRun_PostgresStateBackend_SucceedsAgainstRealPostgres
// additionally exercises --state-backend=postgres's own dry-run
// connectivity check (internal/storage/postgres.Open), the one --dry-run
// branch that can only ever be reached after a *successful* collector ping
// — see operator.go's --dry-run block ordering — so no unreachable-DSN unit
// test can cover it.
func TestRunOperator_DryRun_PostgresStateBackend_SucceedsAgainstRealPostgres(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping operator --dry-run integration test")
	}

	got := cli.RunOperator([]string{
		"--dsn", dsn,
		"--dry-run",
		"--state-backend", "postgres",
	})
	if got != 0 {
		t.Errorf("RunOperator(--dry-run, --state-backend=postgres) against a real, reachable Postgres = %d, want 0", got)
	}
}

func TestRunCollector_DryRun_SucceedsAgainstRealPostgres(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping collector --dry-run integration test")
	}

	if got := cli.RunCollector([]string{"--dsn", dsn, "--dry-run"}); got != 0 {
		t.Errorf("RunCollector(--dry-run) against a real, reachable Postgres = %d, want 0", got)
	}
}
