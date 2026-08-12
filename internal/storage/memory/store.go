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

// Package memory provides a process-memory implementation of the
// storage.SampleStore and storage.EventStore interfaces.
//
// It exists for two reasons:
//  1. As a reference implementation: it is the simplest possible thing that
//     satisfies the interfaces, useful when reading/testing the contract in
//     isolation from Postgres.
//  2. As a real option for anyone embedding these interfaces (e.g. tests
//     elsewhere in the codebase, or a future in-process caching layer) who
//     wants the storage.SampleStore/EventStore shape without a database.
//
// It is NOT wired into cmd/operator's --state-backend=memory path: that
// default already gets equivalent behaviour "for free" from
// internal/collector.Collector and internal/ingester.Store, which keep their
// own in-memory state. Adding this as a second, redundant copy of the same
// data would just be wasted memory. This package is what --state-backend
// would eventually delegate to once the Collector/Ingester themselves become
// storage-pluggable.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// SampleStore is an in-memory, mutex-guarded storage.SampleStore.
type SampleStore struct {
	mu      sync.RWMutex
	samples map[int64][]collector.QuerySample
}

// NewSampleStore returns an empty SampleStore.
func NewSampleStore() *SampleStore {
	return &SampleStore{samples: make(map[int64][]collector.QuerySample)}
}

// Append implements storage.SampleStore.
func (s *SampleStore) Append(_ context.Context, sample collector.QuerySample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.samples[sample.QueryID]
	if sl == nil {
		sl = make([]collector.QuerySample, 0, 32)
	}
	s.samples[sample.QueryID] = append(sl, sample)
	return nil
}

// SamplesInRange implements storage.SampleStore.
func (s *SampleStore) SamplesInRange(_ context.Context, queryID int64, from, to time.Time) ([]collector.QuerySample, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.samples[queryID]
	out := make([]collector.QuerySample, 0, len(all))
	for _, sample := range all {
		if !sample.RecordedAt.Before(from) && !sample.RecordedAt.After(to) {
			out = append(out, sample)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RecordedAt.Before(out[j].RecordedAt) })
	return out, nil
}

// AllQueryIDs implements storage.SampleStore.
func (s *SampleStore) AllQueryIDs(_ context.Context) ([]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]int64, 0, len(s.samples))
	for id, samples := range s.samples {
		if len(samples) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// Prune implements storage.SampleStore.
func (s *SampleStore) Prune(_ context.Context, olderThan time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, samples := range s.samples {
		kept := samples[:0:0]
		for _, sample := range samples {
			if !sample.RecordedAt.Before(olderThan) {
				kept = append(kept, sample)
			}
		}
		if len(kept) == 0 {
			delete(s.samples, id)
		} else {
			s.samples[id] = kept
		}
	}
	return nil
}

// EventStore is an in-memory, mutex-guarded storage.EventStore.
type EventStore struct {
	mu     sync.RWMutex
	events map[string]v1alpha1.DeployEvent
	order  []string // preserves insertion order for All()
}

// NewEventStore returns an empty EventStore.
func NewEventStore() *EventStore {
	return &EventStore{events: make(map[string]v1alpha1.DeployEvent), order: make([]string, 0, 64)}
}

// Add implements storage.EventStore. Re-adding the same ID overwrites in
// place (idempotent upsert), matching the Postgres implementation's
// ON CONFLICT semantics.
func (s *EventStore) Add(_ context.Context, ev v1alpha1.DeployEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.events[ev.ID]; !exists {
		s.order = append(s.order, ev.ID)
	}
	s.events[ev.ID] = ev
	return nil
}

// EventsInRange implements storage.EventStore.
func (s *EventStore) EventsInRange(_ context.Context, from, to time.Time) ([]v1alpha1.DeployEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]v1alpha1.DeployEvent, 0, len(s.order))
	for _, id := range s.order {
		ev := s.events[id]
		if !ev.Timestamp.Before(from) && !ev.Timestamp.After(to) {
			out = append(out, ev)
		}
	}
	return out, nil
}

// All implements storage.EventStore.
func (s *EventStore) All(_ context.Context) ([]v1alpha1.DeployEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]v1alpha1.DeployEvent, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.events[id])
	}
	return out, nil
}
