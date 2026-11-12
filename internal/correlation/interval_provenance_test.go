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

package correlation

import (
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/pgstat"
)

func TestIntervalSamples_UnknownComputedIntervalsDoNotFallBack(t *testing.T) {
	// Changing row membership can leave every interval unknown even though
	// the cumulative mean moves. That is not evidence of a latency shift.
	pre := []pgstat.QuerySample{{MeanExecTimeMs: 10, IntervalComputed: true}}
	post := []pgstat.QuerySample{{MeanExecTimeMs: 100, IntervalComputed: true}}
	before, after, intervals := intervalSamples(pre, post)
	if !intervals || len(before) != 0 || len(after) != 0 {
		t.Fatalf("intervalSamples = (%v, %v, %v), want empty windows in interval mode", before, after, intervals)
	}
}

func TestIntervalSamples_UnknownComputedIntervalOnOneSideStillUsesIntervalMode(t *testing.T) {
	pre := []pgstat.QuerySample{{MeanExecTimeMs: 10}}
	post := []pgstat.QuerySample{{MeanExecTimeMs: 100, IntervalComputed: true}}
	before, after, intervals := intervalSamples(pre, post)
	if !intervals || len(before) != 0 || len(after) != 0 {
		t.Fatalf("intervalSamples = (%v, %v, %v), want empty windows in interval mode", before, after, intervals)
	}
}

func TestIntervalSamples_MeanOnlySourcesKeepLegacyBehavior(t *testing.T) {
	pre := []pgstat.QuerySample{{MeanExecTimeMs: 10}}
	post := []pgstat.QuerySample{{MeanExecTimeMs: 100}}
	before, after, intervals := intervalSamples(pre, post)
	if intervals || len(before) != 1 || len(after) != 1 || before[0].MeanExecTimeMs != 10 || after[0].MeanExecTimeMs != 100 {
		t.Fatalf("intervalSamples = (%v, %v, %v), want unchanged mean-only windows", before, after, intervals)
	}
}

func TestIntervalSamples_KnownIntervalsWithoutMarkerRemainSupported(t *testing.T) {
	pre := []pgstat.QuerySample{{IntervalKnown: true, IntervalCalls: 5, IntervalMeanMs: 10}}
	post := []pgstat.QuerySample{{IntervalKnown: true, IntervalCalls: 5, IntervalMeanMs: 100}}
	before, after, intervals := intervalSamples(pre, post)
	if !intervals || len(before) != 1 || len(after) != 1 || before[0].IntervalMeanMs != 10 || after[0].IntervalMeanMs != 100 {
		t.Fatalf("intervalSamples = (%v, %v, %v), want known interval windows", before, after, intervals)
	}
}
