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
	"github.com/joao00001/pg-regression-radar/internal/pglog"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
)

// Source is the sample history the evaluator reads. *collector.Collector
// satisfies it.
type Source interface {
	AllQueryIDs() []int64
	SamplesInRange(queryID int64, from, to time.Time) []pgstat.QuerySample
}

// ArmSource is the log-derived executions labelled by application_name, from
// which a canary and the stable pods can be told apart. *pglog.Store satisfies
// it.
type ArmSource interface {
	Range(from, to time.Time) []pglog.Bucket
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

// Hooks lets an observer (the live dashboard) hear what the evaluator does. Each
// is called after the evaluator's lock is released, on the goroutine that did
// the work, and must return quickly and never block: the radar does not wait for
// whoever is watching it. Any of them may be nil.
type Hooks struct {
	OnRegister func(Deploy)
	OnEvaluate func(Deploy, Record)
	OnAction   func(ActionRecord)
}

// defaultMaxRecords bounds the evaluations kept in memory. One record holds the
// engine's whole finding (every unit), so the bound is on count, and the oldest
// go first.
const defaultMaxRecords = 500

// Evaluator judges deploys and remembers the outcome. Safe for concurrent use.
type Evaluator struct {
	Source      Source
	Fingerprint adapter.Fingerprint
	Config      attribution.Config
	Policy      *attribution.Policy

	// Arms and ArmsFor, when both are set, let the evaluator compare a canary
	// against the stable pods (grade A). ArmsFor says, for a deploy, which
	// application_names ran the new version and which the old; it returns false
	// when the deploy has no two arms to compare (it was promoted, or is not a
	// rollout), and the engine then works as it does without a log.
	Arms    ArmSource
	ArmsFor func(d Deploy) (adapter.ArmSpec, bool)

	// Hooks is set once, before the evaluator is used.
	Hooks Hooks

	// MaxRecords bounds the evaluations kept in memory; 500 if zero.
	MaxRecords int

	mu      sync.Mutex
	others  []attribution.Event
	records []Record
	deploys []Deploy
	actions []ActionRecord
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
	for _, x := range e.deploys {
		if x.ID == d.ID {
			e.mu.Unlock()
			return
		}
	}
	e.deploys = append(e.deploys, d)
	e.mu.Unlock()
	if e.Hooks.OnRegister != nil {
		e.Hooks.OnRegister(d)
	}
}

// Deploys returns every deploy registered so far, oldest first.
func (e *Evaluator) Deploys() []Deploy {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Deploy(nil), e.deploys...)
}

// DeployByID returns the registered deploy with this id.
func (e *Evaluator) DeployByID(id string) (Deploy, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, x := range e.deploys {
		if x.ID == id {
			return x, true
		}
	}
	return Deploy{}, false
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

// gap is a stretch of time (from, to] whose data a reading leaves out. The zero
// value leaves out nothing.
type gap struct{ from, to time.Time }

func (g gap) has(t time.Time) bool { return !g.to.IsZero() && t.After(g.from) && !t.After(g.to) }

// assess builds the engine's reading of deploy d at time now, from the samples
// in [d.At − PreWindow, now]. It returns false when there are no samples at all.
func (e *Evaluator) assess(now time.Time, d Deploy) (attribution.Assessment, bool) {
	a, _, ok := e.assessWithout(now, d, gap{}, e.Config)
	return a, ok
}

// assessWithout is assess with a stretch of time left out of the data, under cfg.
// It also returns how many samples the busiest query has after the gap, which is
// all there is to judge by when the gap is the part that is not wanted.
func (e *Evaluator) assessWithout(now time.Time, d Deploy, skip gap, cfg attribution.Config) (attribution.Assessment, int, bool) {
	units, after, ok := e.unitsWithout(now, d, skip)
	if !ok {
		return attribution.Assessment{}, 0, false
	}

	e.mu.Lock()
	var events []attribution.Event
	for _, o := range e.others {
		if o.ID != d.ID {
			events = append(events, o)
		}
	}
	e.mu.Unlock()

	return attribution.Assess(cfg, units, events, attribution.Deploy{
		ID: d.ID, At: d.At, Tables: d.Tables,
		Reversible: true, HasRollbackTarget: d.HasRollbackTarget,
	}), after, true
}

// units builds what the engine reads for deploy d at time now: every query's
// series over [d.At − PreWindow, now], with the canary and stable arms told apart
// when the log can. It returns false when there are no samples at all.
func (e *Evaluator) units(now time.Time, d Deploy) ([]attribution.Unit, bool) {
	u, _, ok := e.unitsWithout(now, d, gap{})
	return u, ok
}

// unitsWithout is units with a stretch of time left out, in both the
// pg_stat_statements samples and the log's buckets. after is the number of samples
// the busiest query has once the gap is over.
func (e *Evaluator) unitsWithout(now time.Time, d Deploy, skip gap) ([]attribution.Unit, int, bool) {
	pre := e.Config.PreWindow
	if pre <= 0 {
		pre = 90 * time.Minute
	}
	from := d.At.Add(-pre)
	var samples []pgstat.QuerySample
	after := 0
	for _, id := range e.Source.AllQueryIDs() {
		n := 0
		for _, sm := range dedupe(e.Source.SamplesInRange(id, from, now)) {
			if skip.has(sm.RecordedAt) {
				continue
			}
			if !skip.to.IsZero() && sm.RecordedAt.After(skip.to) {
				n++
			}
			samples = append(samples, sm)
		}
		if n > after {
			after = n
		}
	}
	if len(samples) == 0 {
		return nil, 0, false
	}
	units := adapter.Units(samples, e.Fingerprint, d.Tables)
	if e.Arms != nil && e.ArmsFor != nil {
		if spec, ok := e.ArmsFor(d); ok {
			var buckets []pglog.Bucket
			for _, b := range e.Arms.Range(from, now) {
				if !skip.has(b.At) {
					buckets = append(buckets, b)
				}
			}
			units = adapter.WithArms(units, buckets, spec, d.At)
		}
	}
	return units, after, true
}

// Unit returns the series the engine compared for one query of deploy d: what
// a chart of the evidence is drawn from. It is rebuilt from the same sources the
// evaluation read, so it shows what the engine would see now.
func (e *Evaluator) Unit(now time.Time, d Deploy, key string) (attribution.Unit, bool) {
	units, ok := e.units(now, d)
	if !ok {
		return attribution.Unit{}, false
	}
	for _, u := range units {
		if u.Key == key {
			return u, true
		}
	}
	return attribution.Unit{}, false
}

// Evaluate judges deploy d at time now. It returns false when there are no
// samples at all.
func (e *Evaluator) Evaluate(now time.Time, d Deploy) (Record, bool) {
	a, ok := e.assess(now, d)
	if !ok {
		return Record{}, false
	}
	dec := e.Policy.Decide(now, a)
	rec := Record{At: now, Assessment: a, Decision: dec}

	e.mu.Lock()
	e.records = append(e.records, rec)
	limit := e.MaxRecords
	if limit <= 0 {
		limit = defaultMaxRecords
	}
	if len(e.records) > limit {
		e.records = append([]Record(nil), e.records[len(e.records)-limit:]...)
	}
	e.mu.Unlock()
	if e.Hooks.OnEvaluate != nil {
		e.Hooks.OnEvaluate(d, rec)
	}
	return rec, true
}

// Verify re-reads deploy d at time now, after an action on it had time to act,
// and tells the policy whether the regression is still supported. It is what
// feeds the circuit breaker: after enough actions that did not help, the policy
// stops acting and only alerts. It records no decision.
//
// since is when the action took effect. What the deploy did until then is not
// what the action is judged by: a canary that ran slow for twenty minutes before
// it was stopped would otherwise count against the abort for as long as that
// stretch stays in the window, and two correct aborts would open the breaker. So
// the stretch from the deploy to since is left out, and the engine reads the
// time before the deploy and the time after the action. If the canary is still
// serving, its pods are still in the data after since and the regression is still
// found. A zero since reads the whole window.
//
// It returns false when there is too little to judge by: no samples at all, or,
// with a since, fewer than the engine needs after it. A check that cannot be made
// is not a failure and does not count towards the breaker.
func (e *Evaluator) Verify(now time.Time, d Deploy, since time.Time) (attribution.Verification, bool) {
	skip := gap{}
	cfg := e.Config
	if since.After(d.At) {
		skip = gap{from: d.At, to: since}
		// The engine reads PostWindow after the deploy, and the part that counts
		// now starts late. Let the window reach now, and keep the grace period
		// where it was: the first minutes after a deploy are still left out.
		post := cfg.PostWindow
		if post <= 0 {
			post = 30 * time.Minute
		}
		if cfg.Grace == 0 {
			cfg.Grace = post / 3
		}
		if span := now.Sub(d.At); span > post {
			cfg.PostWindow = span
		}
	}
	a, after, ok := e.assessWithout(now, d, skip, cfg)
	if !ok || (!skip.to.IsZero() && after < verifyMinSamples) {
		return attribution.Verification{}, false
	}
	return e.Policy.Verify(a), true
}

// verifyMinSamples is how many samples a query needs after an action for Verify to
// judge by them: the engine's own minimum for the time after a deploy.
const verifyMinSamples = 3

// ActionRecord is one thing the manager did, or declined to do, on the engine's
// decision, or the outcome of checking it afterwards.
type ActionRecord struct {
	At        time.Time `json:"at"`
	DeployID  string    `json:"deployId"`
	Namespace string    `json:"namespace"`
	App       string    `json:"app"`
	// Action is "pause" or "abort".
	Action string `json:"action"`
	// Result is "done", "failed" or "skipped" for the action itself, then
	// "recovered" or "persisted" for the check made after an abort.
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

// maxActions bounds the in-memory action ledger.
const maxActions = 200

// RecordAction appends to the action ledger, dropping the oldest entry once it
// holds maxActions.
func (e *Evaluator) RecordAction(a ActionRecord) {
	e.mu.Lock()
	e.actions = append(e.actions, a)
	if len(e.actions) > maxActions {
		e.actions = append([]ActionRecord(nil), e.actions[len(e.actions)-maxActions:]...)
	}
	e.mu.Unlock()
	if e.Hooks.OnAction != nil {
		e.Hooks.OnAction(a)
	}
}

// Actions returns the action ledger, oldest first.
func (e *Evaluator) Actions() []ActionRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ActionRecord(nil), e.actions...)
}

// BreakerOpen reports whether the circuit breaker has stopped automatic actions.
func (e *Evaluator) BreakerOpen() bool { return e.Policy.BreakerOpen() }

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
