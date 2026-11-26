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

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/joao00001/pg-regression-radar/internal/observe"
)

// brokenStore is a history store that cannot be written.
type brokenStore struct{ observe.MemoryHistory }

func (b *brokenStore) Save(context.Context, observe.Case) error {
	return errors.New("connection refused")
}

func TestHistoryOutlivesTheEvaluators(t *testing.T) {
	src, d, _ := liveFixture(t)
	ctx := context.Background()
	hist := &observe.MemoryHistory{}
	src.History = hist
	src.RecordNow(ctx, d.ID)

	// A manager that restarted holds no evaluator state, but the store still answers.
	later := &LiveSource{Registry: NewRegistry(), Started: src.Started, Now: func() time.Time { return src.Now().Add(time.Hour) }, History: hist}
	list, _ := later.Deploys(ctx, observe.Range{})
	if len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("stored deploys: %+v", list)
	}
	if list[0].NextEvaluation != nil {
		t.Error("a stored card must not promise a check that is already in the past")
	}
	c, ok, _ := later.Case(ctx, d.ID)
	if !ok || len(c.History) != 2 || len(c.History[1].Units) == 0 {
		t.Fatalf("stored case: %v %+v", ok, c)
	}
	first := c.History[1].Units[0]
	s, ok, _ := later.Series(ctx, d.ID, first.Key)
	if !ok || len(s.Treatment) == 0 {
		t.Fatalf("a chart of a past deploy needs the stored series: %v", ok)
	}
	if len(s.Treatment) > seriesPoints {
		t.Errorf("stored series has %d points, want at most %d", len(s.Treatment), seriesPoints)
	}
}

func TestHistoryDoesNotStoreQueryTextUnlessAllowed(t *testing.T) {
	src, d, _ := liveFixture(t)
	ctx := context.Background()
	src.History = &observe.MemoryHistory{}
	// The live case has text; the stored one must not.
	live, _ := src.liveCase(d.ID)
	if live.History[1].Units[0].Text == "" {
		t.Fatal("the fixture should carry query text")
	}
	src.RecordNow(ctx, d.ID)
	stored, _, _ := src.History.Case(ctx, d.ID)
	for _, e := range stored.History {
		for _, u := range e.Units {
			if u.Text != "" {
				t.Fatalf("query text was stored: %q", u.Text)
			}
		}
	}
	src.StoreQueryText = true
	src.RecordNow(ctx, d.ID)
	stored, _, _ = src.History.Case(ctx, d.ID)
	if stored.History[1].Units[0].Text == "" {
		t.Error("with text allowed the stored case should keep it")
	}
}

func TestHistoryRespectsTheAllowlistOnRead(t *testing.T) {
	src, d, _ := liveFixture(t)
	ctx := context.Background()
	hist := &observe.MemoryHistory{}
	src.History = hist
	// Something recorded earlier, in a namespace no watch may show now.
	_ = hist.Save(ctx, observe.Case{DeploySummary: observe.DeploySummary{ID: "secret/x/1", Namespace: "secret", App: "x", At: src.Now().Add(-time.Hour)}})
	_ = hist.Save(ctx, observe.Case{DeploySummary: observe.DeploySummary{ID: "shop/old/1", Namespace: "shop", App: "old", At: src.Now().Add(-2 * time.Hour)}})
	// Both watches: "open" allows everything, so the secret namespace is visible
	// through it. Close it, as the real allowlist does.
	src.Registry.Each(func(k types.NamespacedName, rt *WatchRuntime) {
		if k.Name == "open" {
			rt.ObserveNamespaces = []string{"shop"}
		}
	})
	list, _ := src.Deploys(ctx, observe.Range{})
	ids := map[string]bool{}
	for _, x := range list {
		ids[x.ID] = true
	}
	if ids["secret/x/1"] || !ids["shop/old/1"] || !ids[d.ID] {
		t.Fatalf("history must be filtered like the live view: %v", ids)
	}
	if _, ok, _ := src.Case(ctx, "secret/x/1"); ok {
		t.Error("a stored case outside the allowlist must look like it does not exist")
	}
}

func TestAFailingStoreNeverBlocksAndIsShown(t *testing.T) {
	src, d, _ := liveFixture(t)
	ctx := context.Background()
	src.History = &brokenStore{}

	done := make(chan struct{})
	go func() { src.RecordNow(ctx, d.ID); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recording blocked")
	}
	o, _ := src.Overview(ctx)
	if o.History.Recording || !strings.Contains(o.History.Note, "not recorded since") {
		t.Fatalf("a store that cannot be written must be said so: %+v", o.History)
	}
	// The live view keeps answering.
	if list, _ := src.Deploys(ctx, observe.Range{}); len(list) != 1 {
		t.Fatalf("live deploys: %+v", list)
	}
}

func TestOverviewSaysWhereHistoryLives(t *testing.T) {
	src, _, _ := liveFixture(t)
	o, _ := src.Overview(context.Background())
	if o.History.Store != "memory" || !o.History.Recording || !strings.Contains(o.History.Note, "restart") {
		t.Fatalf("default history: %+v", o.History)
	}
	src.History = &observe.MemoryHistory{Retention: 72 * time.Hour}
	o, _ = src.Overview(context.Background())
	if o.History.RetentionSeconds != 72*3600 {
		t.Fatalf("retention: %+v", o.History)
	}
}
