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
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/pglog"
)

type fakeHashes struct {
	stable, current string
	err             error
}

func (f fakeHashes) PodHashes(context.Context, string, string) (string, string, error) {
	return f.stable, f.current, f.err
}

func newArms(h RolloutHashes, now time.Time) *ArmsRuntime {
	a := &ArmsRuntime{Store: pglog.NewStore(time.Minute, time.Hour, 0), Hashes: h,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), now: func() time.Time { return now }}
	a.Store.Add(pglog.Record{At: now.Add(-time.Minute), App: "x", QueryID: 1, DurationMs: 1})
	a.usable.Store(true)
	return a
}

func TestPodArmsTellsPodsApartByName(t *testing.T) {
	s := PodArms("checkout", "aaa111", "bbb222")
	if !s.Treatment("checkout-bbb222-xk4pq") || s.Treatment("checkout-aaa111-xk4pq") {
		t.Fatal("treatment")
	}
	if !s.Control("checkout-aaa111-xk4pq") || s.Control("checkout-bbb222-xk4pq") {
		t.Fatal("control")
	}
	// another Rollout whose name merely starts the same is not ours
	if s.Control("checkout-v2-aaa111-xk4pq") || s.Treatment("checkout-bbb2222-x") {
		t.Fatal("prefix confusion")
	}
	if s.Control("psql") || s.Treatment("") {
		t.Fatal("foreign application_name")
	}
}

func TestArmsForOnlyWhenThereAreTwoArmsAndTheLogIsLive(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	d := shadow.Deploy{ID: "d", Namespace: "shop", App: "checkout"}

	if _, ok := newArms(fakeHashes{"a", "b", nil}, now).ArmsFor(d); !ok {
		t.Fatal("a canary with a live log must have arms")
	}
	if _, ok := newArms(fakeHashes{"a", "a", nil}, now).ArmsFor(d); ok {
		t.Fatal("a promoted Rollout has no canary arm")
	}
	if _, ok := newArms(fakeHashes{"", "b", nil}, now).ArmsFor(d); ok {
		t.Fatal("unknown stable hash")
	}
	if _, ok := newArms(fakeHashes{err: errors.New("forbidden")}, now).ArmsFor(d); ok {
		t.Fatal("an unreadable Rollout must not yield arms")
	}

	stale := newArms(fakeHashes{"a", "b", nil}, now)
	stale.now = func() time.Time { return now.Add(10 * time.Minute) }
	if _, ok := stale.ArmsFor(d); ok {
		t.Fatal("a log that stopped flowing must not be compared")
	}

	bad := newArms(fakeHashes{"a", "b", nil}, now)
	bad.usable.Store(false)
	if _, ok := bad.ArmsFor(d); ok {
		t.Fatal("settings that fail validation must not yield arms")
	}
}
