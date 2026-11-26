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

//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/observe"
)

func testHistory(t *testing.T) *History {
	t.Helper()
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h, err := OpenHistory(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.db.Exec(`TRUNCATE ` + SchemaName + `.attribution_deploys CASCADE`)
		_ = h.Close()
	})
	_, _ = h.db.Exec(`TRUNCATE ` + SchemaName + `.attribution_deploys CASCADE`)
	return h
}

func sampleCase(id string, at time.Time) observe.Case {
	return observe.Case{
		DeploySummary: observe.DeploySummary{ID: id, Namespace: "shop", App: "orders", At: at, Phase: observe.PhaseActed, Evaluations: 1,
			Sentence: "Grade A: the canary is slower."},
		History: []observe.Evaluation{{At: at.Add(10 * time.Minute), Health: "Regressed",
			Units: []observe.UnitEvidence{{Key: "k1", Family: "SELECT:orders", Ratio: 1.8, Rejected: true, Grade: "A"}}}},
		Actions: []observe.Action{{At: at.Add(11 * time.Minute), DeployID: id, Namespace: "shop", App: "orders", Action: "abort", Result: "done"}},
	}
}

func TestHistoryRoundTripsAndPrunes(t *testing.T) {
	h := testHistory(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Migration is idempotent: a second open is fine.
	if h2, err := OpenHistory(ctx, os.Getenv("PGRR_TEST_DSN")); err != nil {
		t.Fatalf("second open: %v", err)
	} else {
		_ = h2.Close()
	}

	fresh, old := sampleCase("shop/orders/new", now.Add(-time.Hour)), sampleCase("shop/orders/old", now.Add(-40*24*time.Hour))
	for _, c := range []observe.Case{fresh, old} {
		if err := h.Save(ctx, c); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	// Saving again replaces, it does not duplicate (and the ledger entry is not doubled).
	fresh.Evaluations = 2
	if err := h.Save(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := h.SaveSeries(ctx, observe.Series{DeployID: fresh.ID, Namespace: "shop", Key: "k1", DeployAt: fresh.At,
		Treatment: []observe.Point{{At: now, MeanMs: 12.5, N: 40}}}); err != nil {
		t.Fatalf("SaveSeries: %v", err)
	}
	if err := h.SaveSeries(ctx, observe.Series{DeployID: "nope", Key: "k"}); err == nil {
		t.Error("a series for an unknown deploy should be refused")
	}

	list, err := h.Deploys(ctx, observe.Range{})
	if err != nil || len(list) != 2 || list[0].ID != fresh.ID || list[0].Evaluations != 2 {
		t.Fatalf("deploys: %v %+v", err, list)
	}
	if got, _ := h.Deploys(ctx, observe.Range{From: now.Add(-2 * time.Hour)}); len(got) != 1 {
		t.Fatalf("a range must reach only what it asks for: %+v", got)
	}
	c, ok, err := h.Case(ctx, fresh.ID)
	if err != nil || !ok || len(c.History) != 1 || c.History[0].Units[0].Grade != "A" {
		t.Fatalf("case: %v %v %+v", err, ok, c)
	}
	if _, ok, _ := h.Case(ctx, "missing"); ok {
		t.Error("an unknown case must not exist")
	}
	s, ok, _ := h.Series(ctx, fresh.ID, "k1")
	if !ok || len(s.Treatment) != 1 || s.Treatment[0].N != 40 {
		t.Fatalf("series: %v %+v", ok, s)
	}
	acts, _ := h.Actions(ctx, observe.Range{})
	if len(acts) != 2 {
		t.Fatalf("one ledger entry per deploy, not per save: %+v", acts)
	}
	info, _ := h.Info(ctx)
	if info.Kind != "postgres" || info.Since == nil || !info.Since.Before(now.Add(-39*24*time.Hour)) {
		t.Fatalf("info: %+v", info)
	}

	// Retention: the prune loop's Pruner deletes the old deploy with its ledger and series.
	if err := h.Prune(ctx, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := h.Case(ctx, old.ID); ok {
		t.Error("the old deploy should have been pruned")
	}
	if acts, _ := h.Actions(ctx, observe.Range{}); len(acts) != 1 {
		t.Errorf("its ledger should go with it: %+v", acts)
	}
	if _, ok, _ := h.Case(ctx, fresh.ID); !ok {
		t.Error("the recent deploy must survive")
	}
}

func TestHistoryErrorsDoNotLeakTheDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := OpenHistory(ctx, "postgres://user:hunter2@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if msg := err.Error(); contains(msg, "hunter2") {
		t.Errorf("the error carries the password: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
