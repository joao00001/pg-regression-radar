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

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidatePostgresDSN_AcceptsSupportedForms(t *testing.T) {
	t.Parallel()

	for _, dsn := range []string{
		"postgres://127.0.0.2:5432/postgres?sslmode=disable",
		"127.0.0.2:5432/postgres?sslmode=disable",
		"host=127.0.0.2 port=5432 dbname=postgres sslmode=disable",
	} {
		if err := validatePostgresDSN(dsn); err != nil {
			t.Fatalf("validatePostgresDSN(%q) error = %v, want nil", dsn, err)
		}
	}
}

func TestValidatePostgresDSN_RejectsMalformedURIForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dsn     string
		wantErr string
	}{
		{
			name:    "opaque postgres URI",
			dsn:     "postgres:127.0.0.2:5432/postgres?sslmode=disable",
			wantErr: "opaque URI form is not supported",
		},
		{
			name:    "userinfo without scheme",
			dsn:     "user:pass@127.0.0.2:5432/postgres?sslmode=disable",
			wantErr: "userinfo requires an explicit postgres:// or postgresql:// URI",
		},
		{
			name:    "unsupported scheme",
			dsn:     "foo://127.0.0.2:5432/postgres?sslmode=disable",
			wantErr: `invalid scheme "foo"`,
		},
		{
			name:    "empty username",
			dsn:     "postgres://:pass@127.0.0.2:5432/postgres?sslmode=disable",
			wantErr: "empty username in userinfo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePostgresDSN(tt.dsn)
			if err == nil {
				t.Fatalf("validatePostgresDSN(%q) error = nil, want %q", tt.dsn, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validatePostgresDSN(%q) error = %q, want substring %q", tt.dsn, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRunOperator_RejectsMalformedDSNBeforePing(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	got := runOperator([]string{
		"--dsn", "postgres:127.0.0.2:5432/postgres?sslmode=disable",
		"--dry-run",
	}, &output)
	if got != 1 {
		t.Fatalf("RunOperator(--dry-run, malformed --dsn) = %d, want 1", got)
	}

	if !strings.Contains(output.String(), `"msg":"invalid --dsn"`) {
		t.Fatalf("RunOperator output = %q, want invalid --dsn log", output.String())
	}
	if !strings.Contains(output.String(), "opaque URI form is not supported") {
		t.Fatalf("RunOperator output = %q, want parse error detail", output.String())
	}
	if strings.Contains(output.String(), "collector ping failed") {
		t.Fatalf("RunOperator output = %q, want startup validation before any ping attempt", output.String())
	}
}
