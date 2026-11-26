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

// Package cluster reaches the manager of a real cluster for the dashboard: it
// finds the leader, forwards a local port to it with the user's own kubeconfig,
// keeps that forward alive across failures and leader changes, and proxies the
// dashboard's /observe requests through it.
//
// Nothing here is exposed beyond the machine: the forward listens on 127.0.0.1,
// the manager's API is bound to loopback inside its pod, and the only way in is
// the pods/portforward permission the user already has or does not have.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DefaultLease is the leader-election Lease the manager holds (see
// internal/cli/manager.go: LeaderElectionID).
const DefaultLease = "pg-regression-radar-manager.radar.pgregressionradar.io"

// Target is the pod to forward to.
type Target struct {
	Namespace string
	Pod       string
	// Port is the manager's observe port inside the pod.
	Port int
}

func (t Target) String() string { return t.Namespace + "/" + t.Pod }

// Finder says which pod to talk to right now. It is called again after a failure
// and from time to time, because the leader can change.
type Finder func(ctx context.Context) (Target, error)

// LeaderFinder resolves the pod that holds the manager's Lease. Only the leader
// runs the evaluators, so only it can answer. An empty namespace searches every
// namespace for the Lease, which needs permission to list Leases; without it the
// error says to pass --namespace.
func LeaderFinder(kube kubernetes.Interface, namespace, lease string, port int, now func() time.Time) Finder {
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context) (Target, error) {
		ns := namespace
		if ns == "" {
			found, err := leaseNamespace(ctx, kube, lease)
			if err != nil {
				return Target{}, err
			}
			ns = found
		}
		l, err := kube.CoordinationV1().Leases(ns).Get(ctx, lease, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return Target{}, fmt.Errorf("no leader lease %q in namespace %q: is the manager installed there? (--namespace names the namespace it runs in)", lease, ns)
		}
		if apierrors.IsForbidden(err) {
			return Target{}, fmt.Errorf("not allowed to read the leader lease in namespace %q: the dashboard needs get on leases.coordination.k8s.io there", ns)
		}
		if err != nil {
			return Target{}, fmt.Errorf("reading the leader lease: %w", err)
		}
		pod, err := holderPod(l, now())
		if err != nil {
			return Target{}, err
		}
		p, err := kube.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return Target{}, fmt.Errorf("the lease names %q as leader, but there is no such pod in %q (a manager started with a custom hostname?)", pod, ns)
		}
		if apierrors.IsForbidden(err) {
			return Target{}, fmt.Errorf("not allowed to get pods in namespace %q: the dashboard needs get on pods and create on pods/portforward there", ns)
		}
		if err != nil {
			return Target{}, fmt.Errorf("reading the leader pod: %w", err)
		}
		if p.DeletionTimestamp != nil || p.Status.Phase != corev1.PodRunning {
			return Target{}, fmt.Errorf("the leader pod %s is %s; waiting for the next leader", pod, podState(p))
		}
		return Target{Namespace: ns, Pod: pod, Port: port}, nil
	}
}

func podState(p *corev1.Pod) string {
	if p.DeletionTimestamp != nil {
		return "terminating"
	}
	return strings.ToLower(string(p.Status.Phase))
}

// leaseNamespace finds the one namespace that has the Lease.
func leaseNamespace(ctx context.Context, kube kubernetes.Interface, lease string) (string, error) {
	list, err := kube.CoordinationV1().Leases(metav1.NamespaceAll).List(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + lease})
	if apierrors.IsForbidden(err) {
		return "", errors.New("cannot look for the manager across namespaces (no permission to list leases); pass --namespace with the namespace it runs in")
	}
	if err != nil {
		return "", fmt.Errorf("looking for the manager's lease: %w", err)
	}
	switch len(list.Items) {
	case 0:
		return "", fmt.Errorf("no manager found (no lease %q in any namespace); install it, or pass --namespace if it uses another lease name with --lease-name", lease)
	case 1:
		return list.Items[0].Namespace, nil
	}
	var names []string
	for _, it := range list.Items {
		names = append(names, it.Namespace)
	}
	return "", fmt.Errorf("the manager's lease exists in several namespaces (%s); pass --namespace to pick one", strings.Join(names, ", "))
}

// holderPod is the pod name inside the lease's holder identity ("<pod>_<uuid>",
// as controller-runtime writes it), refusing a lease nobody is renewing.
func holderPod(l *coordinationv1.Lease, now time.Time) (string, error) {
	if l.Spec.HolderIdentity == nil || *l.Spec.HolderIdentity == "" {
		return "", errors.New("the leader lease has no holder: no manager is leading right now")
	}
	if l.Spec.RenewTime != nil && l.Spec.LeaseDurationSeconds != nil {
		expires := l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second)
		if now.After(expires.Add(time.Minute)) {
			return "", fmt.Errorf("the leader lease was last renewed %s ago: the manager is not running", now.Sub(l.Spec.RenewTime.Time).Round(time.Second))
		}
	}
	id := *l.Spec.HolderIdentity
	if i := strings.LastIndex(id, "_"); i > 0 {
		id = id[:i]
	}
	return id, nil
}
