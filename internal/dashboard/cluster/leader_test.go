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

package cluster

import (
	"context"
	"errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"strings"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

var t0 = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func lease(ns, holder string, renewed time.Time, dur int32) *coordinationv1.Lease {
	l := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: DefaultLease, Namespace: ns}}
	if holder != "" {
		l.Spec.HolderIdentity = &holder
	}
	rt := metav1.NewMicroTime(renewed)
	l.Spec.RenewTime = &rt
	l.Spec.LeaseDurationSeconds = &dur
	return l
}

func pod(ns, name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Status: corev1.PodStatus{Phase: phase}}
}

func TestLeaderFinder(t *testing.T) {
	deleting := pod("pgrr", "mgr-0", corev1.PodRunning)
	now := metav1.NewTime(t0)
	deleting.DeletionTimestamp = &now

	cases := []struct {
		name      string
		ns        string
		objs      []runtime.Object
		reactor   func(*fake.Clientset)
		wantPod   string
		wantNS    string
		wantError string
	}{
		{name: "leader found with namespace", ns: "pgrr",
			objs:    []runtime.Object{lease("pgrr", "mgr-0_5b1c-uuid", t0, 15), pod("pgrr", "mgr-0", corev1.PodRunning)},
			wantPod: "mgr-0", wantNS: "pgrr"},
		{name: "namespace discovered", ns: "",
			objs:    []runtime.Object{lease("ops", "mgr-1_x", t0, 15), pod("ops", "mgr-1", corev1.PodRunning)},
			wantPod: "mgr-1", wantNS: "ops"},
		{name: "pod name with underscores keeps all but the uuid", ns: "pgrr",
			objs:    []runtime.Object{lease("pgrr", "a_b_c_uuid", t0, 15), pod("pgrr", "a_b_c", corev1.PodRunning)},
			wantPod: "a_b_c", wantNS: "pgrr"},
		{name: "no lease in namespace", ns: "pgrr", wantError: "is the manager installed there"},
		{name: "no lease anywhere", ns: "", wantError: "no manager found"},
		{name: "lease in two namespaces", ns: "",
			objs:      []runtime.Object{lease("a", "m_u", t0, 15), lease("b", "m_u", t0, 15)},
			wantError: "several namespaces"},
		{name: "no holder", ns: "pgrr", objs: []runtime.Object{lease("pgrr", "", t0, 15)}, wantError: "no holder"},
		{name: "stale lease", ns: "pgrr",
			objs:      []runtime.Object{lease("pgrr", "mgr-0_u", t0.Add(-time.Hour), 15), pod("pgrr", "mgr-0", corev1.PodRunning)},
			wantError: "not running"},
		{name: "pod missing", ns: "pgrr", objs: []runtime.Object{lease("pgrr", "mgr-0_u", t0, 15)}, wantError: "no such pod"},
		{name: "pod pending", ns: "pgrr",
			objs:      []runtime.Object{lease("pgrr", "mgr-0_u", t0, 15), pod("pgrr", "mgr-0", corev1.PodPending)},
			wantError: "is pending"},
		{name: "pod terminating", ns: "pgrr",
			objs:      []runtime.Object{lease("pgrr", "mgr-0_u", t0, 15), deleting},
			wantError: "terminating"},
		{name: "forbidden lease", ns: "pgrr",
			reactor: func(c *fake.Clientset) {
				c.PrependReactor("get", "leases", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, forbidden("leases")
				})
			},
			wantError: "get on leases"},
		{name: "forbidden list across namespaces", ns: "",
			reactor: func(c *fake.Clientset) {
				c.PrependReactor("list", "leases", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, forbidden("leases")
				})
			},
			wantError: "pass --namespace"},
		{name: "forbidden pod", ns: "pgrr", objs: []runtime.Object{lease("pgrr", "mgr-0_u", t0, 15)},
			reactor: func(c *fake.Clientset) {
				c.PrependReactor("get", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, forbidden("pods")
				})
			},
			wantError: "pods/portforward"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewSimpleClientset(tc.objs...)
			if tc.reactor != nil {
				tc.reactor(c)
			}
			got, err := LeaderFinder(c, tc.ns, DefaultLease, 8083, func() time.Time { return t0 })(context.Background())
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want error containing %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Pod != tc.wantPod || got.Namespace != tc.wantNS || got.Port != 8083 {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func forbidden(resource string) error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "x", errors.New("no"))
}
