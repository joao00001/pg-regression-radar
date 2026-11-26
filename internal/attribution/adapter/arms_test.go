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

package adapter_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/adapter"
	"github.com/joao00001/pg-regression-radar/internal/pglog"
)

func spec() adapter.ArmSpec {
	return adapter.ArmSpec{
		Treatment: func(a string) bool { return strings.HasPrefix(a, "web-new-") },
		Control:   func(a string) bool { return strings.HasPrefix(a, "web-old-") },
	}
}

func bucket(app string, q int64, at time.Time, n int64, mean float64) pglog.Bucket {
	return pglog.Bucket{At: at, App: app, QueryID: q, N: n, W: float64(n), SumWMs: mean * float64(n)}
}

func TestWithArmsSplitsOldPodsBeforeAndUsesCanaryAfter(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	at := t0.Add(30 * time.Minute)
	units := []attribution.Unit{{Key: "q", QueryIDs: []int64{7}}, {Key: "other", QueryIDs: []int64{8}}}
	var b []pglog.Bucket
	for m := 1; m <= 60; m++ {
		ts := t0.Add(time.Duration(m) * time.Minute)
		if ts.Before(at) || ts.Equal(at) {
			for _, p := range []string{"web-old-aaaa", "web-old-bbbb", "web-old-cccc", "web-old-dddd"} {
				b = append(b, bucket(p, 7, ts, 3, 10))
			}
		} else {
			b = append(b, bucket("web-new-xxxx", 7, ts, 3, 20)) // canary is twice as slow
			b = append(b, bucket("web-old-aaaa", 7, ts, 3, 10), bucket("web-old-bbbb", 7, ts, 3, 10))
		}
	}
	out := adapter.WithArms(units, b, spec(), at)
	u := out[0]
	if len(u.Treatment) == 0 || len(u.Control) == 0 {
		t.Fatalf("no arms: %d / %d", len(u.Treatment), len(u.Control))
	}
	var pre, post []float64
	ti := map[time.Time]float64{}
	for _, o := range u.Treatment {
		ti[o.At] = o.MeanMs
	}
	for _, c := range u.Control {
		tm, ok := ti[c.At]
		if !ok {
			continue
		}
		if c.At.After(at) {
			post = append(post, tm/c.MeanMs)
		} else {
			pre = append(pre, tm/c.MeanMs)
		}
	}
	for _, r := range pre {
		if r != 1 {
			t.Fatalf("pre-deploy ratio %v, want 1: both halves ran the same version", r)
		}
	}
	if len(post) == 0 || post[0] != 2 {
		t.Fatalf("post-deploy ratio %v, want 2", post)
	}
	if len(out[1].Control) != 0 || len(out[1].Treatment) != 0 {
		t.Fatal("a unit with no log samples must be left alone")
	}
}

func TestWithArmsNeedsBothArms(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	units := []attribution.Unit{{Key: "q", QueryIDs: []int64{7}}}
	// only the canary reports: no control
	b := []pglog.Bucket{bucket("web-new-x", 7, t0.Add(time.Hour), 5, 20)}
	out := adapter.WithArms(units, b, spec(), t0)
	if len(out[0].Control) != 0 || len(out[0].Treatment) != 0 {
		t.Fatalf("got arms from one side: %+v", out[0])
	}
}

func TestWithArmsIgnoresAppsOfNeitherArm(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	units := []attribution.Unit{{Key: "q", QueryIDs: []int64{7}}}
	b := []pglog.Bucket{bucket("psql", 7, t0.Add(time.Hour), 5, 99), bucket("web-new-x", 7, t0.Add(time.Hour), 5, 20), bucket("web-old-a", 7, t0.Add(time.Hour), 5, 10)}
	out := adapter.WithArms(units, b, spec(), t0)
	if len(out[0].Treatment) != 1 || out[0].Treatment[0].MeanMs != 20 || out[0].Control[0].MeanMs != 10 {
		t.Fatalf("%+v", out[0])
	}
}

func TestWithArmsNeverPutsAllOldPodsOnOneSide(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	at := t0.Add(10 * time.Minute)
	units := []attribution.Unit{{Key: "q", QueryIDs: []int64{7}}}
	for n := 2; n <= 12; n++ {
		var b []pglog.Bucket
		for p := 0; p < n; p++ {
			for m := 1; m <= 9; m++ {
				b = append(b, bucket(fmt.Sprintf("web-old-%d-%d", n, p), 7, t0.Add(time.Duration(m)*time.Minute), 2, 10))
			}
		}
		b = append(b, bucket("web-new-x", 7, at.Add(time.Minute), 2, 10), bucket("web-old-0", 7, at.Add(time.Minute), 2, 10))
		out := adapter.WithArms(units, b, spec(), at)
		if len(out[0].Treatment) == 0 || len(out[0].Control) == 0 {
			t.Fatalf("%d old pods: pre-deploy baseline missing one side", n)
		}
		var preT, preC int
		for _, o := range out[0].Treatment {
			if !o.At.After(at) {
				preT++
			}
		}
		for _, o := range out[0].Control {
			if !o.At.After(at) {
				preC++
			}
		}
		if preT == 0 || preC == 0 {
			t.Fatalf("%d old pods: pre-deploy treatment %d control %d", n, preT, preC)
		}
	}
}
