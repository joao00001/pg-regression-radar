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

// Package actuation closes the loop between detecting a query performance
// regression and doing something about it, instead of only alerting.
// ArgoRolloutsAborter is the first (and, deliberately, only) actuator: it
// aborts an Argo Rollouts canary the same way `kubectl argo rollouts abort`
// does. See docs/auto-abort.md for the full safety model — this is an
// opt-in, per-PostgresWatch, high-confidence-only capability, not a default
// behavior.
package actuation

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// rolloutGVR identifies Argo Rollouts' Rollout custom resource. Deliberately
// not vendoring github.com/argoproj/argo-rollouts's Go types for this: this
// project only ever needs to set one status field, so treating Rollout as
// unstructured avoids pulling in a large dependency (and its own
// client-go/controller-runtime version pin) for a single field write.
var rolloutGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "rollouts",
}

// ArgoRolloutsAborter aborts Argo Rollouts canaries via the Kubernetes API.
// It talks to the API server exactly the way `kubectl argo rollouts abort`
// does: a merge patch setting status.abort to true, using the same
// GroupVersionResource Argo Rollouts itself registers. Nothing here is
// Argo-Rollouts-specific beyond that GVR and the one field name — there is
// no dependency on Argo Rollouts' own Go module.
type ArgoRolloutsAborter struct {
	dyn dynamic.Interface
}

// NewArgoRolloutsAborter creates an ArgoRolloutsAborter backed by dyn. dyn
// is typically built once, in cmd/manager's setup, via
// dynamic.NewForConfig(mgr.GetConfig()) — see internal/cli.RunManager.
func NewArgoRolloutsAborter(dyn dynamic.Interface) *ArgoRolloutsAborter {
	return &ArgoRolloutsAborter{dyn: dyn}
}

// Abort sets status.abort=true on the Rollout named name in namespace,
// causing Argo Rollouts' own controller to stop the canary in place — the
// same effect `kubectl argo rollouts abort <name>` has. It does not delete,
// scale, or otherwise mutate anything about the Rollout beyond that one
// status field, and it does not roll back to a previous revision itself;
// that decision (and the corresponding `kubectl argo rollouts undo`, if
// wanted) is left to whoever operates the Rollout.
//
// Returns an error if the named Rollout doesn't exist (e.g. this deploy
// event's app name doesn't match any real Rollout in this namespace — see
// docs/auto-abort.md's note on why this is scoped to argo-rollouts-sourced
// deploy events only) or the patch is rejected, most plausibly because the
// caller's ServiceAccount lacks the argoproj.io/rollouts/status patch RBAC
// documented in docs/auto-abort.md.
func (a *ArgoRolloutsAborter) Abort(ctx context.Context, namespace, name string) error {
	patch := []byte(`{"status":{"abort":true}}`)
	_, err := a.dyn.Resource(rolloutGVR).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}, "status",
	)
	if err != nil {
		return fmt.Errorf("abort rollout %s/%s: %w", namespace, name, err)
	}
	return nil
}
