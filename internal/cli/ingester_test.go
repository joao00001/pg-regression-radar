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

func TestRunIngester_Version(t *testing.T) {
	t.Parallel()
	if got := cli.RunIngester([]string{"--version"}); got != 0 {
		t.Errorf("RunIngester(--version) = %d, want 0", got)
	}
}

func TestRunIngester_UnknownFlag(t *testing.T) {
	t.Parallel()
	if got := cli.RunIngester([]string{"--this-flag-does-not-exist"}); got != 2 {
		t.Errorf("RunIngester(--this-flag-does-not-exist) = %d, want 2", got)
	}
}

func TestRunIngester_Help(t *testing.T) {
	t.Parallel()
	if got := cli.RunIngester([]string{"--help"}); got != 0 {
		t.Errorf("RunIngester(--help) = %d, want 0", got)
	}
}

// RunIngester's --dry-run path needs no real network I/O at all (unlike
// operator/collector, which ping a real Postgres) — every check here is
// pure/local, so the full success and failure matrix is unit-testable.

func TestRunIngester_DryRun_OK(t *testing.T) {
	t.Parallel()
	got := cli.RunIngester([]string{
		"--dry-run",
		"--source-type", "argocd",
		"--listen", ":0",
	})
	if got != 0 {
		t.Errorf("RunIngester(--dry-run) with valid config = %d, want 0", got)
	}
}

func TestRunIngester_DryRun_BadSourceType(t *testing.T) {
	t.Parallel()
	got := cli.RunIngester([]string{
		"--dry-run",
		"--source-type", "not-a-real-source-type",
	})
	if got != 1 {
		t.Errorf("RunIngester(--dry-run, bad --source-type) = %d, want 1", got)
	}
}

func TestRunIngester_DryRun_BadListenAddress(t *testing.T) {
	t.Parallel()
	got := cli.RunIngester([]string{
		"--dry-run",
		"--listen", "not-a-valid-address",
	})
	if got != 1 {
		t.Errorf("RunIngester(--dry-run, bad --listen) = %d, want 1", got)
	}
}
