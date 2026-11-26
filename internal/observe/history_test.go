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
	"testing"
	"time"
)

func TestMemoryHistoryIsBoundedAndForgets(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	m := &MemoryHistory{MaxDeploys: 3, Retention: 24 * time.Hour, Now: func() time.Time { return now }}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		_ = m.Save(ctx, Case{DeploySummary: DeploySummary{ID: id, At: now.Add(-time.Duration(i) * time.Hour)}})
		_ = m.SaveSeries(ctx, Series{DeployID: id, Key: "q"})
	}
	list, _ := m.Deploys(ctx, Range{})
	if len(list) != 3 || list[0].ID != "a" {
		t.Fatalf("a ring of 3, newest first: %+v", list)
	}
	if _, ok, _ := m.Series(ctx, "e", "q"); ok {
		t.Error("the series of a dropped deploy must go with it")
	}
	// Retention.
	_ = m.Save(ctx, Case{DeploySummary: DeploySummary{ID: "old", At: now.Add(-48 * time.Hour)}})
	if _, ok, _ := m.Case(ctx, "old"); ok {
		t.Error("a deploy older than the retention must not be kept")
	}
	info, _ := m.Info(ctx)
	if info.Kind != "memory" || info.Since == nil || !info.Since.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("info: %+v", info)
	}
	// Ranges.
	got, _ := m.Deploys(ctx, Range{From: now.Add(-90 * time.Minute)})
	if len(got) != 2 {
		t.Fatalf("range: %+v", got)
	}
}

func TestDownsampleKeepsTheShapeAndTheCounts(t *testing.T) {
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	var in []Point
	var calls int64
	for i := 0; i < 1000; i++ {
		in = append(in, Point{At: t0.Add(time.Duration(i) * time.Second), MeanMs: float64(i), N: 10})
		calls += 10
	}
	out := Downsample(in, 100)
	if len(out) != 100 {
		t.Fatalf("got %d points", len(out))
	}
	var n int64
	for i, p := range out {
		n += p.N
		if i > 0 && !p.At.After(out[i-1].At) {
			t.Fatal("time must move forward")
		}
		if i > 0 && p.MeanMs <= out[i-1].MeanMs {
			t.Fatal("a rising series must still rise")
		}
	}
	if n != calls {
		t.Errorf("sample counts changed: %d != %d", n, calls)
	}
	if !out[len(out)-1].At.Equal(in[len(in)-1].At) {
		t.Error("the last observation time must survive")
	}
	if got := Downsample(in[:5], 100); len(got) != 5 {
		t.Error("a short series is returned as it is")
	}
}
