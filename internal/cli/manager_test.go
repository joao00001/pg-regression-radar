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

func TestRunManager_Version(t *testing.T) {
	// Not t.Parallel(): this test (and its sibling below) manipulate
	// process-wide environment variables that ctrl.GetConfig() reads, via
	// t.Setenv, which itself forbids use alongside t.Parallel().
	if got := cli.RunManager([]string{"--version"}); got != 0 {
		t.Errorf("RunManager(--version) = %d, want 0", got)
	}
}

// TestRunManager_NoKubernetesConfigResolves exercises the one branch of
// RunManager that needs no real Kubernetes cluster to reach deterministically:
// ctrl.GetConfig() failing because neither an in-cluster service account nor
// a kubeconfig can be found. This is deliberately forced via environment
// variables (rather than relying on the ambient CI/dev environment having
// neither) so the test is reproducible regardless of what happens to be
// configured on whatever machine runs it.
func TestRunManager_NoKubernetesConfigResolves(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig-does-not-exist")

	got := cli.RunManager([]string{"--dry-run"})
	if got != 1 {
		t.Errorf("RunManager(--dry-run) with no resolvable Kubernetes config = %d, want 1", got)
	}
}

func TestRunManager_UnknownFlag(t *testing.T) {
	if got := cli.RunManager([]string{"--this-flag-does-not-exist"}); got != 2 {
		t.Errorf("RunManager(--this-flag-does-not-exist) = %d, want 2", got)
	}
}

func TestRunManager_Help(t *testing.T) {
	if got := cli.RunManager([]string{"--help"}); got != 0 {
		t.Errorf("RunManager(--help) = %d, want 0", got)
	}
}

func TestRunManager_InvalidSecurityProfile(t *testing.T) {
	if got := cli.RunManager([]string{"--security-profile", "unknown"}); got != 1 {
		t.Errorf("RunManager(--security-profile=unknown) = %d, want 1", got)
	}
}

func TestRunManager_ValidSecurityProfileControlled(t *testing.T) {
	// controlled is valid; GetConfig() will fail in test env but that's exit 1 for a
	// different reason — we just need no exit 2 from flag parsing.
	got := cli.RunManager([]string{"--security-profile", "controlled", "--dry-run"})
	// Without a kubeconfig this exits 1 (GetConfig fails), not 2; flag was accepted.
	if got == 2 {
		t.Errorf("RunManager(--security-profile=controlled) rejected the flag (exit 2)")
	}
}

func TestRunManager_ValidSecurityProfileHardened(t *testing.T) {
	got := cli.RunManager([]string{"--security-profile", "hardened", "--dry-run"})
	if got == 2 {
		t.Errorf("RunManager(--security-profile=hardened) rejected the flag (exit 2)")
	}
}
