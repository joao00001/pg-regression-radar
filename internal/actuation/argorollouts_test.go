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

package actuation

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newRollout(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"status": map[string]interface{}{
			"abort": false,
		},
	}}
}

// TestArgoRolloutsAborter_Abort verifies the happy path: Abort patches
// status.abort to true on the named Rollout, the same field
// `kubectl argo rollouts abort` sets, without touching anything else.
func TestArgoRolloutsAborter_Abort(t *testing.T) {
	scheme := runtime.NewScheme()
	rollout := newRollout("default", "checkout")
	dyn := dynamicfake.NewSimpleDynamicClient(scheme, rollout)

	a := NewArgoRolloutsAborter(dyn)
	if err := a.Abort(context.Background(), "default", "checkout"); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	got, err := dyn.Resource(rolloutGVR).Namespace("default").Get(context.Background(), "checkout", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get rollout after abort: %v", err)
	}
	abort, found, err := unstructured.NestedBool(got.Object, "status", "abort")
	if err != nil || !found {
		t.Fatalf("status.abort not found or wrong type: found=%v err=%v", found, err)
	}
	if !abort {
		t.Fatal("expected status.abort=true after Abort, got false")
	}
}

// TestArgoRolloutsAborter_Abort_RolloutNotFound verifies that aborting a
// name with no matching Rollout returns an error rather than silently
// succeeding — the deploy event's app name might not correspond to any
// real Rollout (e.g. it came from a non-argo-rollouts source, or the
// Rollout was deleted between detection and the abort attempt), and a
// caller relying on this to actually stop a bad canary needs to know that
// didn't happen.
func TestArgoRolloutsAborter_Abort_RolloutNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme) // no Rollout objects at all

	a := NewArgoRolloutsAborter(dyn)
	err := a.Abort(context.Background(), "default", "does-not-exist")
	if err == nil {
		t.Fatal("expected an error aborting a nonexistent rollout, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("expected error to name the rollout, got: %v", err)
	}
}

// TestArgoRolloutsAborter_Abort_DoesNotTouchOtherRollouts verifies Abort's
// namespace/name scoping: aborting one Rollout must never affect another,
// even one with the same name in a different namespace.
func TestArgoRolloutsAborter_Abort_DoesNotTouchOtherRollouts(t *testing.T) {
	scheme := runtime.NewScheme()
	target := newRollout("prod", "checkout")
	other := newRollout("staging", "checkout")
	dyn := dynamicfake.NewSimpleDynamicClient(scheme, target, other)

	a := NewArgoRolloutsAborter(dyn)
	if err := a.Abort(context.Background(), "prod", "checkout"); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	gotOther, err := dyn.Resource(rolloutGVR).Namespace("staging").Get(context.Background(), "checkout", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get other rollout: %v", err)
	}
	abort, _, _ := unstructured.NestedBool(gotOther.Object, "status", "abort")
	if abort {
		t.Fatal("expected the staging namespace's rollout to be untouched, but status.abort is true")
	}
}
