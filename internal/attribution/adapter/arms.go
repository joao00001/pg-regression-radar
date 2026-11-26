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

package adapter

import (
	"hash/fnv"
	"sort"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/pglog"
)

// ArmSpec says which application_names ran the new version of a deploy and which
// ran the version it replaced.
//
// A canary has no arm of its own before the deploy: until then every pod runs
// the old version. To still have a pre-deploy baseline for "treatment divided by
// control", the old version's pods are split in two, one half standing in for the
// future canary (see splitPods). After the deploy the halves
// are no longer used: the canary pods are the treatment and the old version's
// pods the control. A real canary effect then shows as a step in the
// treatment/control ratio on top of a baseline that was flat by construction,
// which is the evidence a concurrent control provides.
type ArmSpec struct {
	// Treatment reports whether an application_name belongs to the pods running
	// the new version.
	Treatment func(app string) bool
	// Control reports whether an application_name belongs to the pods still
	// running the old version.
	Control func(app string) bool
}

// WithArms fills Treatment and Control on the units that the log has samples
// for, and leaves the rest as they were. at is the deploy time. A unit is the
// set of query ids it was built from, so a log line (which carries the query id,
// never the text) joins to it through them.
//
// The units' Obs are untouched: they stay what pg_stat_statements reports, which
// is what the grade B and C paths read.
func WithArms(units []attribution.Unit, buckets []pglog.Bucket, spec ArmSpec, at time.Time) []attribution.Unit {
	if spec.Treatment == nil || spec.Control == nil || len(buckets) == 0 {
		return units
	}
	halves := splitPods(buckets, spec, at)
	owner := map[int64]int{}
	for i, u := range units {
		for _, q := range u.QueryIDs {
			owner[q] = i
		}
	}
	type acc struct {
		n    int64
		w, s float64
	}
	treat := map[int]map[time.Time]*acc{}
	ctl := map[int]map[time.Time]*acc{}
	add := func(m map[int]map[time.Time]*acc, i int, b pglog.Bucket) {
		if m[i] == nil {
			m[i] = map[time.Time]*acc{}
		}
		a := m[i][b.At]
		if a == nil {
			a = &acc{}
			m[i][b.At] = a
		}
		a.n += b.N
		a.w += b.W
		a.s += b.SumWMs
	}
	for _, b := range buckets {
		i, ok := owner[b.QueryID]
		if !ok || b.W <= 0 {
			continue
		}
		switch {
		case b.At.After(at):
			switch {
			case spec.Treatment(b.App):
				add(treat, i, b)
			case spec.Control(b.App):
				add(ctl, i, b)
			}
		case spec.Control(b.App):
			// Before the deploy every pod runs the old version: half of them
			// stand in for the canary.
			if halves[b.App] == 0 {
				add(treat, i, b)
			} else {
				add(ctl, i, b)
			}
		}
	}
	obs := func(m map[time.Time]*acc) []attribution.Obs {
		out := make([]attribution.Obs, 0, len(m))
		for t, a := range m {
			out = append(out, attribution.Obs{At: t, MeanMs: a.s / a.w, Calls: a.n})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
		return out
	}
	out := append([]attribution.Unit(nil), units...)
	for i := range out {
		t, c := obs(treat[i]), obs(ctl[i])
		if len(t) == 0 || len(c) == 0 {
			continue
		}
		out[i].Treatment, out[i].Control = t, c
	}
	return out
}

// splitPods assigns each pod of the old version seen before the deploy to one of
// two halves. Pods are put in a fixed order by a hash of their name and dealt out
// alternately, so that any two or more pods end up on both sides (a plain hash
// parity would put four pods on one side one time in eight).
func splitPods(buckets []pglog.Bucket, spec ArmSpec, at time.Time) map[string]int {
	seen := map[string]bool{}
	for _, b := range buckets {
		if !b.At.After(at) && spec.Control(b.App) {
			seen[b.App] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		hi, hj := hashOf(names[i]), hashOf(names[j])
		if hi != hj {
			return hi < hj
		}
		return names[i] < names[j]
	})
	out := make(map[string]int, len(names))
	for i, n := range names {
		out[n] = i % 2
	}
	return out
}

func hashOf(app string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(app))
	return h.Sum32()
}
