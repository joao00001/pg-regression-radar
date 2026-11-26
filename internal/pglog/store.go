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
	"sort"
	"sync"
	"time"
)

// Bucket is the sampled executions of one query by one application_name in one
// interval. N is how many sampled executions there were: the weight of a mean is
// the number of samples behind it, not the calls they stand for. W is the number
// of calls they stand for (each sample weighs the inverse of its chance of having
// been logged) and SumWMs their weighted total duration, so the mean is
// SumWMs/W, which stays unbiased when slow statements are always logged and fast
// ones only sampled.
type Bucket struct {
	At      time.Time // end of the interval
	App     string
	QueryID int64
	N       int64
	W       float64
	SumWMs  float64
}

// Mean is the weighted mean duration of the executions the samples stand for.
func (b Bucket) Mean() float64 {
	if b.W == 0 {
		return 0
	}
	return b.SumWMs / b.W
}

// Sampling is how the server decided what to log, so each logged execution can
// be weighted by the inverse of its chance of being logged.
//
// PostgreSQL logs a statement if its duration reaches log_min_duration_statement
// (always), or if it reaches log_min_duration_sample and a draw under
// log_statement_sample_rate succeeds. Without correcting for it, a server that
// also keeps a slow-query log would show a mean pulled up by slow statements
// that were logged with certainty while fast ones were not.
type Sampling struct {
	// Rate is log_statement_sample_rate, in (0, 1]. Zero or less means the
	// statements are not sampled, which is weighted as 1.
	Rate float64
	// AlwaysAtOrAboveMs is log_min_duration_statement. Negative means off.
	AlwaysAtOrAboveMs float64
}

// Weight is the number of calls one logged execution of duration ms stands for.
func (s Sampling) Weight(ms float64) float64 {
	if s.Rate <= 0 || s.Rate >= 1 {
		return 1
	}
	if s.AlwaysAtOrAboveMs >= 0 && ms >= s.AlwaysAtOrAboveMs {
		return 1
	}
	return 1 / s.Rate
}

type key struct {
	app string
	qid int64
}

// Store holds the log's executions, bucketed by interval, for a bounded time and
// a bounded number of distinct (application_name, query_id) pairs. It is safe
// for concurrent use.
type Store struct {
	interval  time.Duration
	retention time.Duration
	maxKeys   int

	mu      sync.Mutex
	samp    Sampling
	series  map[key][]Bucket // each in time order
	last    time.Time        // the newest record seen
	seen    int64
	dropped int64
}

// NewStore returns a store that buckets at interval and forgets what is older
// than retention (relative to the newest record). maxKeys bounds the distinct
// (application_name, query_id) pairs; records for pairs beyond it are counted
// and dropped, so a client that puts a request id in its application_name
// cannot grow the manager without limit.
func NewStore(interval, retention time.Duration, maxKeys int) *Store {
	if interval <= 0 {
		interval = time.Minute
	}
	if retention < 2*interval {
		retention = 2 * interval
	}
	if maxKeys <= 0 {
		maxKeys = 20000
	}
	return &Store{interval: interval, retention: retention, maxKeys: maxKeys, series: map[key][]Bucket{}}
}

// SetSampling tells the store how the server samples its log. Executions added
// afterwards are weighted by it.
func (s *Store) SetSampling(x Sampling) {
	s.mu.Lock()
	s.samp = x
	s.mu.Unlock()
}

// Interval is the bucket width.
func (s *Store) Interval() time.Duration { return s.interval }

// Add records one execution.
func (s *Store) Add(r Record) {
	if r.At.IsZero() {
		return
	}
	end := r.At.Truncate(s.interval).Add(s.interval)
	k := key{r.App, r.QueryID}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen++
	list, ok := s.series[k]
	if !ok && len(s.series) >= s.maxKeys {
		s.dropped++
		return
	}
	if r.At.After(s.last) {
		s.last = r.At
	}
	// Records arrive nearly in order; walk back from the end.
	i := len(list)
	for i > 0 && list[i-1].At.After(end) {
		i--
	}
	w := s.samp.Weight(r.DurationMs)
	if i > 0 && list[i-1].At.Equal(end) {
		list[i-1].N++
		list[i-1].W += w
		list[i-1].SumWMs += w * r.DurationMs
	} else {
		list = append(list, Bucket{})
		copy(list[i+1:], list[i:])
		list[i] = Bucket{At: end, App: r.App, QueryID: r.QueryID, N: 1, W: w, SumWMs: w * r.DurationMs}
	}
	s.series[k] = list
}

// Prune forgets buckets older than the retention, and pairs left empty.
func (s *Store) Prune() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.last.Add(-s.retention)
	for k, list := range s.series {
		i := sort.Search(len(list), func(i int) bool { return list[i].At.After(cut) })
		if i == len(list) {
			delete(s.series, k)
		} else if i > 0 {
			s.series[k] = append([]Bucket(nil), list[i:]...)
		}
	}
}

// Range returns the buckets that end in (from, to], oldest first.
func (s *Store) Range(from, to time.Time) []Bucket {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bucket
	for _, list := range s.series {
		for _, b := range list {
			if b.At.After(from) && !b.At.After(to) {
				out = append(out, b)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].App != out[j].App {
			return out[i].App < out[j].App
		}
		return out[i].QueryID < out[j].QueryID
	})
	return out
}

// Stats say how much the store has taken in.
type Stats struct {
	Records int64     // executions accepted or dropped
	Dropped int64     // executions dropped for lack of room
	Pairs   int       // distinct (application_name, query_id) pairs held
	Newest  time.Time // the newest execution seen
}

// Stats returns the counters.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{Records: s.seen, Dropped: s.dropped, Pairs: len(s.series), Newest: s.last}
}
