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
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

const managerStartupFailureReason = "ManagerStartupFailed"

func reportManagerStartupFailureStatus(ctx context.Context, c client.Client, cause error) error {
	message := fmt.Sprintf("manager could not reach the Kubernetes API strongly enough to start reconcilers: %v", cause)

	var errs []error
	if err := reportPostgresWatchStartupFailureStatus(ctx, c, message); err != nil {
		errs = append(errs, err)
	}
	if err := reportDeploySourceStartupFailureStatus(ctx, c, message); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func reportPostgresWatchStartupFailureStatus(ctx context.Context, c client.Client, message string) error {
	var list radarv1alpha1.PostgresWatchList
	if err := c.List(ctx, &list); err != nil {
		return fmt.Errorf("list postgreswatches: %w", err)
	}

	var errs []error
	for i := range list.Items {
		watch := &list.Items[i]
		watch.Status.Phase = radarv1alpha1.PostgresWatchPhaseFailed
		watch.Status.ObservedGeneration = watch.Generation
		watch.Status.Message = message
		setCondition(&watch.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             managerStartupFailureReason,
			Message:            message,
			ObservedGeneration: watch.Generation,
		})
		if err := c.Status().Update(ctx, watch); err != nil {
			errs = append(errs, fmt.Errorf("update postgreswatch %s/%s status: %w", watch.Namespace, watch.Name, err))
		}
	}
	return errors.Join(errs...)
}

func reportDeploySourceStartupFailureStatus(ctx context.Context, c client.Client, message string) error {
	var list radarv1alpha1.DeploySourceList
	if err := c.List(ctx, &list); err != nil {
		return fmt.Errorf("list deploysources: %w", err)
	}

	var errs []error
	for i := range list.Items {
		src := &list.Items[i]
		src.Status.Phase = radarv1alpha1.DeploySourcePhasePending
		src.Status.ObservedGeneration = src.Generation
		src.Status.WebhookPath = ""
		src.Status.Message = message
		setCondition(&src.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             managerStartupFailureReason,
			Message:            message,
			ObservedGeneration: src.Generation,
		})
		if err := c.Status().Update(ctx, src); err != nil {
			errs = append(errs, fmt.Errorf("update deploysource %s/%s status: %w", src.Namespace, src.Name, err))
		}
	}
	return errors.Join(errs...)
}

func setCondition(conditions *[]metav1.Condition, cond metav1.Condition) {
	now := metav1.Now()
	for i := range *conditions {
		if (*conditions)[i].Type == cond.Type {
			if (*conditions)[i].Status != cond.Status {
				cond.LastTransitionTime = now
			} else {
				cond.LastTransitionTime = (*conditions)[i].LastTransitionTime
			}
			(*conditions)[i] = cond
			return
		}
	}
	if cond.LastTransitionTime.IsZero() {
		cond.LastTransitionTime = now
	}
	*conditions = append(*conditions, cond)
}
