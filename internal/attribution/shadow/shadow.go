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

// Package shadow runs the attribution engine next to the existing detector
// without ever acting: it judges each deploy, asks the policy what it would do,
// and records the answer. It is how a new engine earns trust before it is
// allowed to roll anything back.
package shadow

import (
	"sync"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/adapter"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
)

// Source is the sample history the evaluator reads. *collector.Collector
// satisfies it.
type Source interface {
	AllQueryIDs() []int64
	SamplesInRange(queryID int64, from, to time.Time) []pgstat.QuerySample
}

// Deploy is the deploy under judgement and what is known about it.
type Deploy struct {
	ID string
	// Namespace, App and Revision say which workload and release this is, so a
	// controller can ask for the verdict of the deploy it is rolling out.
	Namespace, App, Revision string
	At                       time.Time
	// Tables the deploy declares it changes, if known.
	Tables []string
	// HasRollbackTarget is false for the first deploy of an application.
	HasRollbackTarget bool
}

// Record is one shadow evaluation.
type Record struct {
	At         time.Time
	Assessment attribution.Assessment
	Decision   attribution.Decision
}

// Evaluator judges deploys and remembers the outcome. Safe for concurrent use.
type Evaluator struct {
	Source      Source
	Fingerprint adapter.Fingerprint
	Config      attribution.Config
	Policy      *attribution.Policy

	mu      sync.Mutex
	others  []attribution.Event
	records []Record
	deploys []Deploy
}

// New returns an evaluator in shadow mode (the policy never enforces).
func New(src Source, fp adapter.Fingerprint, cfg attribution.Config) *Evaluator {
	return NewWithPolicy(src, fp, cfg, attribution.PolicyConfig{Enforce: false})
}

// NewWithPolicy returns an evaluator with an explicit policy configuration.
// With Enforce set, its decisions carry verdicts a controller acts on.
func NewWithPolicy(src Source, fp adapter.Fingerprint, cfg attribution.Config, pc attribution.PolicyConfig) *Evaluator {
	return &Evaluator{Source: src, Fingerprint: fp, Config: cfg, Policy: attribution.NewPolicy(pc)}
}

// Register remembers a deploy so its latest evaluation can be looked up by
// workload, and registers it as something that can move latency.
func (e *Evaluator) Register(d Deploy) {
	e.NoteDeploy(d.ID, d.At)
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, x := range e.deploys {
		if x.ID == d.ID {
			return
		}
	}
	e.deploys = append(e.deploys, d)
}

// HasPrior reports whether an earlier deploy of app in namespace is known, that
// is, whether there is something to roll back to. After a manager restart the
// evaluator knows nothing, so the first deploy it sees counts as having no
// earlier one, which is the safe direction: it can pause a rollout but not
// abort one.
func (e *Evaluator) HasPrior(namespace, app string, before time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, x := range e.deploys {
		if x.Namespace == namespace && x.App == app && x.At.Before(before) {
			return true
		}
	}
	return false
}

// Latest returns the most recent evaluation of the newest deploy of app in
// namespace (of the given revision, when revision is not empty). found is false
// when no such deploy is known; ok is false when it is known but has not been
// evaluated yet.
func (e *Evaluator) Latest(namespace, app, revision string) (d Deploy, rec Record, found, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, x := range e.deploys {
		if x.Namespace != namespace || x.App != app || (revision != "" && x.Revision != revision) {
			continue
		}
		if !found || x.At.After(d.At) {
			d, found = x, true
		}
	}
	if !found {
		return
	}
	for i := len(e.records) - 1; i >= 0; i-- {
		if e.records[i].Assessment.Deploy.ID == d.ID {
			return d, e.records[i], true, true
		}
	}
	return d, Record{}, true, false
}

// NoteDeploy registers a deploy as something that can move latency, so that
// evaluations of other deploys can account for it.
func (e *Evaluator) NoteDeploy(id string, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, o := range e.others {
		if o.ID == id {
			return
		}
	}
	e.others = append(e.others, attribution.Event{ID: id, Kind: attribution.EventDeploy, At: at})
}

// Evaluate judges deploy d at time now, from the samples in
// [d.At − PreWindow, now]. It returns false when there are no samples at all.
func (e *Evaluator) Evaluate(now time.Time, d Deploy) (Record, bool) {
	cfg := e.Config
	pre := cfg.PreWindow
	if pre <= 0 {
		pre = 90 * time.Minute
	}
	from := d.At.Add(-pre)
	var samples []pgstat.QuerySample
	for _, id := range e.Source.AllQueryIDs() {
		samples = append(samples, dedupe(e.Source.SamplesInRange(id, from, now))...)
	}
	if len(samples) == 0 {
		return Record{}, false
	}
	units := adapter.Units(samples, e.Fingerprint, d.Tables)

	e.mu.Lock()
	var events []attribution.Event
	for _, o := range e.others {
		if o.ID != d.ID {
			events = append(events, o)
		}
	}
	e.mu.Unlock()

	a := attribution.Assess(cfg, units, events, attribution.Deploy{
		ID: d.ID, At: d.At, Tables: d.Tables,
		Reversible: true, HasRollbackTarget: d.HasRollbackTarget,
	})
	dec := e.Policy.Decide(now, a)
	rec := Record{At: now, Assessment: a, Decision: dec}

	e.mu.Lock()
	e.records = append(e.records, rec)
	e.mu.Unlock()
	return rec, true
}

// Records returns the evaluations so far, oldest first.
func (e *Evaluator) Records() []Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Record(nil), e.records...)
}

// dedupe drops repeated (queryid, time) samples, which the collector's
// fingerprint fallback can return under more than one queryid.
func dedupe(in []pgstat.QuerySample) []pgstat.QuerySample {
	type k struct {
		id int64
		at time.Time
	}
	seen := make(map[k]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		key := k{s.QueryID, s.RecordedAt}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}
