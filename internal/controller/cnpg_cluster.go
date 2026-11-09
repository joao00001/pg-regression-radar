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

package controller

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

// cnpgReadyConditionType is the condition type CloudNativePG reports on a
// Cluster's status once it considers the cluster fully operational — see
// https://cloudnative-pg.io/documentation/current/cloudnative-pg.v1/#postgresql-cnpg-io-v1-ClusterCondition.
const cnpgReadyConditionType = "Ready"

// validateCNPGCluster checks that the CloudNativePG Cluster named by
// watch.Spec.CNPGClusterRef exists and is healthy, reading it through cl
// (the same client — hub or remote — that dsnSecretRef itself was just
// resolved through) in namespace (the same namespace dsnSecretRef was read
// from). A no-op (nil error, empty phase) when CNPGClusterRef is unset —
// callers should skip calling this entirely in that case, but it is also
// safe to call unconditionally.
//
// "Healthy" is decided in two steps:
//
//  1. If status.conditions contains a Ready condition (every CloudNativePG
//     release in current support windows sets one), its Status is
//     authoritative: True means healthy, anything else means not.
//  2. Otherwise (an older CloudNativePG release that predates the Ready
//     condition), fall back to a best-effort check of status.phase: empty
//     means the Cluster hasn't been reconciled far enough yet to report
//     anything, and a phase containing "Failed" (case-insensitive; CloudNativePG's
//     own phase constants include exactly one such string, "Cluster in
//     failed state") means unhealthy. Any other non-empty phase string
//     (e.g. "Cluster in healthy state", or one of the several transient
//     "setting up.../switching.../waiting for..." phases CloudNativePG
//     reports while converging) is treated as healthy — this is
//     deliberately permissive in the fallback case, since the whole point
//     of this check is catching a genuinely broken/missing target, not
//     gating on every transient reconciliation phase of a cluster that is
//     simply still starting up.
//
// The returned error, when non-nil, is written verbatim into
// PostgresWatch.status.message by markFailed — see resolveDSN's caller in
// Reconcile — so it names the Cluster, its namespace, and exactly what was
// wrong.
func (r *PostgresWatchReconciler) validateCNPGCluster(ctx context.Context, cl client.Client, namespace string, watch *radarv1alpha1.PostgresWatch) error {
	if watch.Spec.CNPGClusterRef == "" {
		return nil
	}

	var cluster cnpgCluster
	key := types.NamespacedName{Namespace: namespace, Name: watch.Spec.CNPGClusterRef}
	if err := cl.Get(ctx, key, &cluster); err != nil {
		return fmt.Errorf(
			"fetch cnpg cluster %s (postgresql.cnpg.io/v1, Kind=Cluster) referenced by spec.cnpgClusterRef: %w — "+
				"ensure the CloudNativePG CRDs are installed on the target cluster and the credential used to "+
				"reach it can read Cluster resources in group postgresql.cnpg.io; see "+
				"docs/multi-cluster.md#remote-cloudnativepg-cluster-validation",
			key, err,
		)
	}

	for _, cond := range cluster.Status.Conditions {
		if cond.Type != cnpgReadyConditionType {
			continue
		}
		if cond.Status == metav1.ConditionTrue {
			return nil
		}
		return fmt.Errorf(
			"cnpg cluster %s is not ready (condition %s=%s, reason=%q, message=%q)",
			key, cnpgReadyConditionType, cond.Status, cond.Reason, cond.Message,
		)
	}

	// No Ready condition reported at all — fall back to status.phase.
	phase := cluster.Status.Phase
	if phase == "" {
		return fmt.Errorf(
			"cnpg cluster %s has not reported status.phase or a %s condition yet — it may still be initialising",
			key, cnpgReadyConditionType,
		)
	}
	if strings.Contains(strings.ToLower(phase), "fail") {
		return fmt.Errorf("cnpg cluster %s is reporting phase %q", key, phase)
	}
	return nil
}
