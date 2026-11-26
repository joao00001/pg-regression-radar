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

package observe

import (
	"context"
	"sync"
	"time"
)

// Range is the time window a client asked for. A zero bound is open.
type Range struct{ From, To time.Time }

// Contains reports whether t falls in the range.
func (r Range) Contains(t time.Time) bool {
	return (r.From.IsZero() || !t.Before(r.From)) && (r.To.IsZero() || !t.After(r.To))
}

// Source is everything the API reads. The manager implements it over the live
// evaluators and, later, a history store; tests implement it over recorded
// data. It is the seam that lets the dashboard have more than one origin.
type Source interface {
	Overview(ctx context.Context) (Overview, error)
	// Deploys returns the deploys whose time falls in r, newest first.
	Deploys(ctx context.Context, r Range) ([]DeploySummary, error)
	// Case returns one deploy in full; ok is false when it is unknown.
	Case(ctx context.Context, id string) (c Case, ok bool, err error)
	// Series returns the chart data of one query of one deploy.
	Series(ctx context.Context, id, unit string) (s Series, ok bool, err error)
	// Actions returns the ledger entries in r, newest first.
	Actions(ctx context.Context, r Range) ([]Action, error)
	// Subscribe returns a channel of events and a function that ends the
	// subscription. A slow subscriber is dropped events, never the radar slowed.
	Subscribe() (events <-chan Event, cancel func())
}

// Scoped restricts a Source to the namespaces of scope. It is the one place the
// allowlist is enforced: every method goes through it, and what is out of scope
// is indistinguishable from what does not exist, so the answer reveals nothing.
func Scoped(src Source, scope Scope) Source { return &scoped{src: src, scope: scope} }

type scoped struct {
	src   Source
	scope Scope
}

func (s *scoped) Overview(ctx context.Context) (Overview, error) {
	o, err := s.src.Overview(ctx)
	if err != nil {
		return Overview{}, err
	}
	o.Scope = s.scope
	if o.Scope.Namespaces != nil {
		o.Scope.Namespaces = append([]string(nil), o.Scope.Namespaces...)
	}
	// The scope is about the namespaces of the workloads being deployed, not about
	// where a PostgresWatch lives: the watches are the radar's own configuration,
	// and the status strip must always be able to say what it is running.
	return o, nil
}

func (s *scoped) Deploys(ctx context.Context, r Range) ([]DeploySummary, error) {
	in, err := s.src.Deploys(ctx, r)
	if err != nil {
		return nil, err
	}
	out := make([]DeploySummary, 0, len(in))
	for _, d := range in {
		if s.scope.Allows(d.Namespace) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *scoped) Case(ctx context.Context, id string) (Case, bool, error) {
	c, ok, err := s.src.Case(ctx, id)
	if err != nil || !ok || !s.scope.Allows(c.Namespace) {
		return Case{}, false, err
	}
	return c, true, nil
}

func (s *scoped) Series(ctx context.Context, id, unit string) (Series, bool, error) {
	x, ok, err := s.src.Series(ctx, id, unit)
	if err != nil || !ok || !s.scope.Allows(x.Namespace) {
		return Series{}, false, err
	}
	return x, true, nil
}

func (s *scoped) Actions(ctx context.Context, r Range) ([]Action, error) {
	in, err := s.src.Actions(ctx, r)
	if err != nil {
		return nil, err
	}
	out := make([]Action, 0, len(in))
	for _, a := range in {
		if s.scope.Allows(a.Namespace) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *scoped) Subscribe() (<-chan Event, func()) {
	in, cancel := s.src.Subscribe()
	return FilterEvents(in, cancel, s.scope.Allows)
}

// FilterEvents passes on only the events allow accepts for their namespace. An
// event with no namespace is about the radar, not a deploy, and always passes.
// A consumer too slow to keep up is dropped events, never the source blocked.
// The returned cancel is idempotent and also ends the upstream subscription.
func FilterEvents(in <-chan Event, cancelIn func(), allow func(ns string) bool) (<-chan Event, func()) {
	out := make(chan Event, cap(in)+1)
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case e, ok := <-in:
				if !ok {
					return
				}
				if e.Namespace != "" && !allow(e.Namespace) {
					continue
				}
				select {
				case out <- e:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return out, func() { once.Do(func() { close(done); cancelIn() }) }
}
