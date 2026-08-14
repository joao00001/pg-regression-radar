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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

func newManagerStatusTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	s := runtime.NewScheme()
	if err := radarv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	return fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.PostgresWatch{}, &radarv1alpha1.DeploySource{}).
		WithObjects(objs...).
		Build()
}

func TestReportManagerStartupFailureStatus_UpdatesExistingResources(t *testing.T) {
	watch := &radarv1alpha1.PostgresWatch{
		ObjectMeta: metav1.ObjectMeta{Name: "podip-watch", Namespace: "default", Generation: 3},
	}
	source := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "webhook-source", Namespace: "default", Generation: 7},
	}
	c := newManagerStatusTestClient(t, watch, source)
	cause := errors.New(`failed to get server groups: Get "https://10.96.0.1:443/api": dial tcp 10.96.0.1:443: i/o timeout`)

	if err := reportManagerStartupFailureStatus(context.Background(), c, cause); err != nil {
		t.Fatalf("reportManagerStartupFailureStatus: %v", err)
	}

	var gotWatch radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), types.NamespacedName{Name: "podip-watch", Namespace: "default"}, &gotWatch); err != nil {
		t.Fatalf("get postgreswatch: %v", err)
	}
	if gotWatch.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
		t.Fatalf("expected PostgresWatch phase Failed, got %q", gotWatch.Status.Phase)
	}
	if gotWatch.Status.ObservedGeneration != watch.Generation {
		t.Fatalf("expected PostgresWatch observedGeneration %d, got %d", watch.Generation, gotWatch.Status.ObservedGeneration)
	}
	if !strings.Contains(gotWatch.Status.Message, cause.Error()) {
		t.Fatalf("expected PostgresWatch message to contain %q, got %q", cause.Error(), gotWatch.Status.Message)
	}
	if len(gotWatch.Status.Conditions) != 1 {
		t.Fatalf("expected one PostgresWatch condition, got %d", len(gotWatch.Status.Conditions))
	}
	if cond := gotWatch.Status.Conditions[0]; cond.Type != "Ready" || cond.Status != metav1.ConditionFalse || cond.Reason != managerStartupFailureReason {
		t.Fatalf("unexpected PostgresWatch Ready condition: %+v", cond)
	}

	var gotSource radarv1alpha1.DeploySource
	if err := c.Get(context.Background(), types.NamespacedName{Name: "webhook-source", Namespace: "default"}, &gotSource); err != nil {
		t.Fatalf("get deploysource: %v", err)
	}
	if gotSource.Status.Phase != radarv1alpha1.DeploySourcePhaseFailed {
		t.Fatalf("expected DeploySource phase Failed, got %q", gotSource.Status.Phase)
	}
	if gotSource.Status.ObservedGeneration != source.Generation {
		t.Fatalf("expected DeploySource observedGeneration %d, got %d", source.Generation, gotSource.Status.ObservedGeneration)
	}
	if gotSource.Status.WebhookPath != "" {
		t.Fatalf("expected DeploySource webhookPath to be cleared, got %q", gotSource.Status.WebhookPath)
	}
	if !strings.Contains(gotSource.Status.Message, cause.Error()) {
		t.Fatalf("expected DeploySource message to contain %q, got %q", cause.Error(), gotSource.Status.Message)
	}
	if len(gotSource.Status.Conditions) != 1 {
		t.Fatalf("expected one DeploySource condition, got %d", len(gotSource.Status.Conditions))
	}
	if cond := gotSource.Status.Conditions[0]; cond.Type != "Ready" || cond.Status != metav1.ConditionFalse || cond.Reason != managerStartupFailureReason {
		t.Fatalf("unexpected DeploySource Ready condition: %+v", cond)
	}
}

func TestReportManagerStartupFailureStatus_NoResourcesIsNoop(t *testing.T) {
	c := newManagerStatusTestClient(t)

	if err := reportManagerStartupFailureStatus(context.Background(), c, errors.New("boom")); err != nil {
		t.Fatalf("reportManagerStartupFailureStatus on empty cluster: %v", err)
	}
}
