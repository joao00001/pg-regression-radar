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
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func good() map[string]string {
	return map[string]string{
		"server_version_num": "160004", "compute_query_id": "on", "shared_preload_libraries": "pg_stat_statements",
		"log_destination": "jsonlog", "log_min_duration_statement": "-1", "log_min_duration_sample": "0",
		"log_statement_sample_rate": "0.02",
	}
}

func TestCheckSettingsAcceptsTheRecommendedSetup(t *testing.T) {
	c := CheckSettings(good())
	if !c.OK() {
		t.Fatalf("issues: %v", c.Issues)
	}
	if c.Sampling.Rate != 0.02 || c.Sampling.AlwaysAtOrAboveMs != -1 {
		t.Fatalf("sampling %+v", c.Sampling)
	}
}

func TestCheckSettingsFlagsWhatBreaksTheData(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m map[string]string)
		want   string
	}{
		{"old server", func(m map[string]string) { m["server_version_num"] = "140010" }, "server_version_num"},
		{"no query id", func(m map[string]string) { m["compute_query_id"] = "off" }, "compute_query_id"},
		{"auto without pgss", func(m map[string]string) { m["compute_query_id"] = "auto"; m["shared_preload_libraries"] = "" }, "compute_query_id"},
		{"plain text log", func(m map[string]string) { m["log_destination"] = "stderr" }, "log_destination"},
		{"no sampling", func(m map[string]string) { m["log_statement_sample_rate"] = "0" }, "log_statement_sample_rate"},
		{"sample off", func(m map[string]string) { m["log_min_duration_sample"] = "-1" }, "log_min_duration_sample"},
		{"slow only", func(m map[string]string) { m["log_min_duration_sample"] = "100" }, "log_min_duration_sample"},
	}
	for _, c := range cases {
		m := good()
		c.mutate(m)
		got := CheckSettings(m)
		found := false
		for _, i := range got.Issues {
			if i.Setting == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want an issue on %s, got %v", c.name, c.want, got.Issues)
		}
	}
}

func TestCheckSettingsAutoWithPgssIsFine(t *testing.T) {
	m := good()
	m["compute_query_id"] = "auto"
	if c := CheckSettings(m); !c.OK() {
		t.Fatalf("%v", c.Issues)
	}
}

func TestSlowQueryLogCoexistsAndIsWeighted(t *testing.T) {
	m := good()
	m["log_min_duration_statement"] = "500"
	c := CheckSettings(m)
	if !c.OK() {
		t.Fatalf("a slow-query log must not be an error: %v", c.Issues)
	}
	if c.Sampling.Weight(600) != 1 || c.Sampling.Weight(5) != 50 {
		t.Fatalf("weights %v %v", c.Sampling.Weight(600), c.Sampling.Weight(5))
	}
}

type fakeLogs struct {
	mu      sync.Mutex
	opened  map[string]int
	content map[string]string
}

func (f *fakeLogs) Stream(_ context.Context, _, pod, _ string, _ time.Time) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened[pod]++
	return io.NopCloser(strings.NewReader(f.content[pod])), nil
}

func TestFollowPodsReadsEveryPodAndPicksUpNewOnes(t *testing.T) {
	line := func(app string, q int) string {
		return `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 1 ms  statement: select 1","application_name":"` + app + `","query_id":` + string(rune('0'+q)) + `}` + "\n"
	}
	logs := &fakeLogs{opened: map[string]int{}, content: map[string]string{"pg-1": line("a", 1), "pg-2": line("b", 2)}}
	var mu sync.Mutex
	pods := []string{"pg-1"}
	list := func(context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), pods...), nil
	}

	var got sync.Map
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		FollowPods(ctx, "db", "postgres", list, logs, 20*time.Millisecond, func(r Record) { got.Store(r.App, true) }, nil)
		close(done)
	}()
	wait := func(app string) {
		t.Helper()
		for i := 0; i < 300; i++ {
			if _, ok := got.Load(app); ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("never saw %s", app)
	}
	wait("a")
	mu.Lock()
	pods = append(pods, "pg-2") // a replica appears
	mu.Unlock()
	wait("b")
	cancel()
	<-done
}

func TestAHighSampleRateWarnsButIsUsed(t *testing.T) {
	m := good()
	m["log_statement_sample_rate"] = "1"
	c := CheckSettings(m)
	if !c.OK() {
		t.Fatalf("a high rate must not stop the log from being used: %v", c.Issues)
	}
	if len(c.Issues) != 1 || !c.Issues[0].Warning || c.Issues[0].Setting != "log_statement_sample_rate" {
		t.Fatalf("want one warning, got %v", c.Issues)
	}
}
