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
	"sort"
	"sync"
	"time"
)

// StoreInfo says what a HistoryStore holds and for how long.
type StoreInfo struct {
	// Kind is "memory" or "postgres".
	Kind string
	// Since is the oldest deploy the store can answer for; nil when it is empty.
	Since *time.Time
	// Retention is how long a deploy is kept; 0 means bounded by size only.
	Retention time.Duration
}

// HistoryStore keeps what the radar concluded after the evaluator's own bounded
// memory has let it go, so a time range reaches further back than the process.
//
// Writing is best effort and never on the path of a decision: the caller records
// a failed Save and goes on. Reading answers with what exists; Info says how far
// back that is, so the dashboard never shows a short list as a complete one.
type HistoryStore interface {
	// Save inserts or replaces a deploy's case (its evaluations and ledger).
	Save(ctx context.Context, c Case) error
	// SaveSeries inserts or replaces the chart data of one query of one deploy.
	SaveSeries(ctx context.Context, s Series) error
	Deploys(ctx context.Context, r Range) ([]DeploySummary, error)
	Case(ctx context.Context, id string) (Case, bool, error)
	Series(ctx context.Context, id, unit string) (Series, bool, error)
	Actions(ctx context.Context, r Range) ([]Action, error)
	Info(ctx context.Context) (StoreInfo, error)
}

// MemoryHistory is the default HistoryStore: a bounded ring in this process.
// It is lost on restart, and Info says so through its kind.
type MemoryHistory struct {
	// MaxDeploys bounds the deploys kept; 500 if zero. The oldest go first.
	MaxDeploys int
	// Retention drops deploys older than this; 0 keeps them until the size bound.
	Retention time.Duration
	// Now is the clock; time.Now if nil.
	Now func() time.Time

	mu     sync.Mutex
	cases  map[string]Case
	series map[string]Series // id + "\x00" + unit
}

func (m *MemoryHistory) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Save implements HistoryStore.
func (m *MemoryHistory) Save(_ context.Context, c Case) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cases == nil {
		m.cases, m.series = map[string]Case{}, map[string]Series{}
	}
	m.cases[c.ID] = c
	m.trim()
	return nil
}

// SaveSeries implements HistoryStore.
func (m *MemoryHistory) SaveSeries(_ context.Context, s Series) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// As in Postgres, a series belongs to a saved deploy; one for a deploy the
	// ring already let go would never be read and would never be freed.
	if _, ok := m.cases[s.DeployID]; !ok {
		return nil
	}
	m.series[s.DeployID+"\x00"+s.Key] = s
	return nil
}

// trim applies retention and the size bound; the caller holds the lock.
func (m *MemoryHistory) trim() {
	max := m.MaxDeploys
	if max <= 0 {
		max = 500
	}
	drop := func(id string) {
		delete(m.cases, id)
		for k, s := range m.series {
			if s.DeployID == id {
				delete(m.series, k)
			}
		}
	}
	if m.Retention > 0 {
		cut := m.now().Add(-m.Retention)
		for id, c := range m.cases {
			if c.At.Before(cut) {
				drop(id)
			}
		}
	}
	if len(m.cases) <= max {
		return
	}
	ids := make([]string, 0, len(m.cases))
	for id := range m.cases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return m.cases[ids[i]].At.Before(m.cases[ids[j]].At) })
	for _, id := range ids[:len(ids)-max] {
		drop(id)
	}
}

// Deploys implements HistoryStore.
func (m *MemoryHistory) Deploys(_ context.Context, r Range) ([]DeploySummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DeploySummary
	for _, c := range m.cases {
		if r.Contains(c.At) {
			out = append(out, c.DeploySummary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// Case implements HistoryStore.
func (m *MemoryHistory) Case(_ context.Context, id string) (Case, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cases[id]
	return c, ok, nil
}

// Series implements HistoryStore.
func (m *MemoryHistory) Series(_ context.Context, id, unit string) (Series, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.series[id+"\x00"+unit]
	return s, ok, nil
}

// Actions implements HistoryStore.
func (m *MemoryHistory) Actions(_ context.Context, r Range) ([]Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Action
	for _, c := range m.cases {
		for _, a := range c.Actions {
			if r.Contains(a.At) {
				out = append(out, a)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// Info implements HistoryStore.
func (m *MemoryHistory) Info(_ context.Context) (StoreInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info := StoreInfo{Kind: "memory", Retention: m.Retention}
	for _, c := range m.cases {
		if info.Since == nil || c.At.Before(*info.Since) {
			t := c.At
			info.Since = &t
		}
	}
	return info, nil
}

// Downsample reduces points to at most max by averaging neighbours, weighted by
// the calls behind each mean, so a stored series keeps its shape and its sample
// counts but costs a fixed amount. Each kept point sits at the time of the last
// observation it stands for.
func Downsample(in []Point, max int) []Point {
	if max < 2 || len(in) <= max {
		return in
	}
	out := make([]Point, 0, max)
	for b := 0; b < max; b++ {
		lo, hi := b*len(in)/max, (b+1)*len(in)/max
		if hi <= lo {
			continue
		}
		var n int64
		var sum float64
		for _, p := range in[lo:hi] {
			w := p.N
			if w <= 0 {
				w = 1
			}
			n += w
			sum += p.MeanMs * float64(w)
		}
		out = append(out, Point{At: in[hi-1].At, MeanMs: sum / float64(n), N: n})
	}
	return out
}
