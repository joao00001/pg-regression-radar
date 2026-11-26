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

package pglog

import (
	"context"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodLogs opens a follow stream of one pod's log from a time. It is the part of
// the Kubernetes API that FollowPods uses, so a test can stand in for it.
type PodLogs interface {
	Stream(ctx context.Context, namespace, pod, container string, since time.Time) (io.ReadCloser, error)
}

// KubePodLogs reads pods/log through a clientset.
type KubePodLogs struct{ Client kubernetes.Interface }

// Stream implements PodLogs.
func (k KubePodLogs) Stream(ctx context.Context, namespace, pod, container string, since time.Time) (io.ReadCloser, error) {
	opt := &corev1.PodLogOptions{Container: container, Follow: true}
	if !since.IsZero() {
		t := metav1.NewTime(since)
		opt.SinceTime = &t
	}
	return k.Client.CoreV1().Pods(namespace).GetLogs(pod, opt).Stream(ctx)
}

// PodLister lists the names of the pods to follow.
type PodLister func(ctx context.Context) ([]string, error)

// KubePodLister lists the running pods of a namespace that match a label
// selector (for CloudNativePG: cnpg.io/cluster=<name>).
func KubePodLister(c kubernetes.Interface, namespace, selector string) PodLister {
	return func(ctx context.Context) ([]string, error) {
		list, err := c.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		var out []string
		for _, p := range list.Items {
			if p.Status.Phase == corev1.PodRunning {
				out = append(out, p.Name)
			}
		}
		return out, nil
	}
}

// FollowPods follows the log of every pod the lister returns, starting each from
// now, and keeps following as pods come and go. A stream that ends or fails is
// reopened on the next round from where its last line was, so a short outage
// loses nothing and a restart replays nothing. It returns when ctx is done.
//
// logf is told about failures; it may be nil.
func FollowPods(ctx context.Context, namespace, container string, list PodLister, logs PodLogs, every time.Duration, sink func(Record), logf func(format string, args ...any)) {
	if every <= 0 {
		every = 15 * time.Second
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var mu sync.Mutex
	active := map[string]bool{}
	resume := map[string]time.Time{} // last record time per pod
	var wg sync.WaitGroup

	follow := func(pod string) {
		defer wg.Done()
		defer func() { mu.Lock(); delete(active, pod); mu.Unlock() }()
		mu.Lock()
		since := resume[pod]
		mu.Unlock()
		if since.IsZero() {
			since = time.Now()
		}
		rc, err := logs.Stream(ctx, namespace, pod, container, since)
		if err != nil {
			logf("pglog: open log of %s/%s: %v", namespace, pod, err)
			return
		}
		defer func() { _ = rc.Close() }()
		_, err = Consume(ctx, rc, func(r Record) {
			mu.Lock()
			if r.At.After(resume[pod]) {
				resume[pod] = r.At
			}
			mu.Unlock()
			sink(r)
		})
		if err != nil && ctx.Err() == nil {
			logf("pglog: log of %s/%s ended: %v", namespace, pod, err)
		}
	}

	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		pods, err := list(ctx)
		if err != nil {
			logf("pglog: list pods in %s: %v", namespace, err)
		}
		for _, p := range pods {
			mu.Lock()
			if !active[p] {
				active[p] = true
				wg.Add(1)
				go follow(p)
			}
			mu.Unlock()
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-tick.C:
		}
	}
}
