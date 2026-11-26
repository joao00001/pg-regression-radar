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
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Fixture is a recorded state of the radar. It stands in for the live manager
// in tests and when developing the dashboard without a cluster; its JSON shape
// is the API's own, so a recording of real responses is a valid fixture.
type Fixture struct {
	Overview Overview `json:"overview"`
	Cases    []Case   `json:"cases"`
	// Series is keyed by "<deployId>/<unit key>".
	Series map[string]Series `json:"series"`
	// Actions is the ledger, oldest first.
	Actions []Action `json:"actions"`
}

// LoadFixture reads a fixture from a JSON file.
func LoadFixture(path string) (Fixture, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- a developer-chosen fixture path, never a request value
	if err != nil {
		return Fixture{}, err
	}
	var f Fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return Fixture{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// FixtureSource serves a Fixture and lets a test publish events to it.
type FixtureSource struct {
	F   Fixture
	Hub *Hub
}

// NewFixtureSource returns a Source over f.
func NewFixtureSource(f Fixture) *FixtureSource { return &FixtureSource{F: f, Hub: NewHub(0)} }

// Overview implements Source.
func (s *FixtureSource) Overview(context.Context) (Overview, error) { return s.F.Overview, nil }

// Deploys implements Source.
func (s *FixtureSource) Deploys(_ context.Context, r Range) ([]DeploySummary, error) {
	var out []DeploySummary
	for _, c := range s.F.Cases {
		if r.Contains(c.At) {
			out = append(out, c.DeploySummary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// Case implements Source.
func (s *FixtureSource) Case(_ context.Context, id string) (Case, bool, error) {
	for _, c := range s.F.Cases {
		if c.ID == id {
			return c, true, nil
		}
	}
	return Case{}, false, nil
}

// Series implements Source.
func (s *FixtureSource) Series(_ context.Context, id, unit string) (Series, bool, error) {
	x, ok := s.F.Series[id+"/"+unit]
	return x, ok, nil
}

// Actions implements Source.
func (s *FixtureSource) Actions(_ context.Context, r Range) ([]Action, error) {
	var out []Action
	for i := len(s.F.Actions) - 1; i >= 0; i-- {
		if a := s.F.Actions[i]; r.Contains(a.At) {
			out = append(out, a)
		}
	}
	return out, nil
}

// Subscribe implements Source.
func (s *FixtureSource) Subscribe() (<-chan Event, func()) { return s.Hub.Subscribe() }
