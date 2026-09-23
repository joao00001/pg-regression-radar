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

package dashboardapi

import (
	"context"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// collectorSampleReader adapts *collector.Collector's own in-memory sample
// history to SampleReader. This lets GET /api/v1/queries serve real data
// from processes running with --state-backend=memory (the default), not
// only when a storage.SampleStore (postgres backend) is configured — the
// Collector already keeps exactly this data in-process either way.
type collectorSampleReader struct {
	col *collector.Collector
}

// NewCollectorSampleReader returns a SampleReader backed directly by col's
// in-memory sample history, for use when no storage.SampleStore is
// configured (the default --state-backend=memory).
func NewCollectorSampleReader(col *collector.Collector) SampleReader {
	return collectorSampleReader{col: col}
}

func (r collectorSampleReader) SamplesInRange(_ context.Context, queryID int64, from, to time.Time) ([]collector.QuerySample, error) {
	return r.col.SamplesInRange(queryID, from, to), nil
}

func (r collectorSampleReader) AllQueryIDs(_ context.Context) ([]int64, error) {
	return r.col.AllQueryIDs(), nil
}

// ingesterEventReader adapts *ingester.Store's own in-memory event history
// to EventReader. This lets GET /api/v1/deploys serve real data from
// processes running with --state-backend=memory (the default), not only
// when a storage.EventStore (postgres backend) is configured — the Store
// already keeps exactly this data in-process either way.
type ingesterEventReader struct {
	store *ingester.Store
}

// NewIngesterEventReader returns an EventReader backed directly by store's
// in-memory event history, for use when no storage.EventStore is configured
// (the default --state-backend=memory).
func NewIngesterEventReader(store *ingester.Store) EventReader {
	return ingesterEventReader{store: store}
}

func (r ingesterEventReader) EventsInRange(_ context.Context, from, to time.Time) ([]v1alpha1.DeployEvent, error) {
	return r.store.EventsInRange(from, to), nil
}
