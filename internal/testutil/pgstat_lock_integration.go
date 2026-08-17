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

package testutil

import (
	"context"
	"database/sql"
	"testing"
)

const pgStatStatementsLockKey int64 = 8255423672001

// AcquirePGStatStatementsTestLock serializes integration tests that
// reset/read pg_stat_statements so package-parallel test execution does not
// race on shared server-global stats state.
func AcquirePGStatStatementsTestLock(t *testing.T, ctx context.Context, db *sql.DB) func() {
	t.Helper()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("open dedicated lock connection: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pgStatStatementsLockKey); err != nil {
		_ = conn.Close()
		t.Fatalf("pg_advisory_lock(%d): %v", pgStatStatementsLockKey, err)
	}
	return func() {
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pgStatStatementsLockKey); err != nil {
			t.Errorf("pg_advisory_unlock(%d): %v", pgStatStatementsLockKey, err)
		}
		if err := conn.Close(); err != nil {
			t.Errorf("close lock connection: %v", err)
		}
	}
}
