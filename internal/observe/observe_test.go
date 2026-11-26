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
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var t0 = time.Date(2026, 11, 25, 21, 0, 0, 0, time.UTC)

func testFixture() Fixture {
	mk := func(id, ns, app string, at time.Time, phase Phase) Case {
		return Case{
			DeploySummary: DeploySummary{
				ID: id, Namespace: ns, App: app, Revision: "rev-" + id, At: at, Phase: phase,
				Evaluations: 1, Health: "regressed",
				Latest:   &Decision{At: at.Add(5 * time.Minute), Action: "rollback", Wanted: "rollback", Grade: "A", Impact: 0.31, Attribution: 0.97, Streak: 2, Enforced: true},
				Sentence: "x",
			},
			History: []Evaluation{{
				At: at.Add(5 * time.Minute), Health: "regressed", Coverage: 0.9,
				Units: []UnitEvidence{{Key: "q1", Family: "SELECT:orders", Text: "SELECT * FROM orders WHERE id = $1", Kind: "regressed", Grade: "A", Ratio: 2.1}},
			}},
		}
	}
	return Fixture{
		Overview: Overview{
			GeneratedAt: t0,
			Watches: []WatchStatus{
				{Name: "w1", Namespace: "shop", Mode: "enforce"},
				{Name: "w2", Namespace: "secret", Mode: "shadow"},
			},
		},
		Cases: []Case{
			mk("shop/orders/1", "shop", "orders", t0.Add(-2*time.Hour), PhaseActed),
			mk("secret/ledger/1", "secret", "ledger", t0.Add(-1*time.Hour), PhaseDecided),
			mk("shop/cart/1", "shop", "cart", t0.Add(-30*time.Minute), PhaseObserving),
		},
		Series: map[string]Series{
			"shop/orders/1/q1":   {DeployID: "shop/orders/1", Namespace: "shop", Key: "q1", DeployAt: t0, Treatment: []Point{{At: t0, MeanMs: 3, N: 10}}},
			"secret/ledger/1/q1": {DeployID: "secret/ledger/1", Namespace: "secret", Key: "q1", DeployAt: t0},
		},
		Actions: []Action{
			{At: t0.Add(-100 * time.Minute), DeployID: "shop/orders/1", Namespace: "shop", App: "orders", Action: "abort", Result: "done"},
			{At: t0.Add(-50 * time.Minute), DeployID: "secret/ledger/1", Namespace: "secret", App: "ledger", Action: "pause", Result: "done"},
		},
	}
}

// esc encodes a deploy id the way a client must: ids are opaque and contain "/".
func esc(id string) string { return url.PathEscape(id) }

func serve(t *testing.T, src Source, allow bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	(&Handler{Source: src, AllowQueries: allow, Now: func() time.Time { return t0 }, Heartbeat: 20 * time.Millisecond}).Routes(mux)
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		b.WriteString(sc.Text() + "\n")
	}
	return resp.StatusCode, []byte(b.String())
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("not json: %v\n%s", err, got)
	}
	pretty, _ := json.MarshalIndent(v, "", "  ")
	pretty = append(pretty, '\n')
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, pretty, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != string(pretty) {
		t.Fatalf("%s differs from golden:\n%s", name, pretty)
	}
}

func TestQueryTextIsHiddenUnlessTheManagerAllowsAndTheClientAsks(t *testing.T) {
	src := Scoped(NewFixtureSource(testFixture()), Scope{All: true})
	path := "/observe/v1/deploys/" + esc("shop/orders/1")
	locked := serve(t, src, false)
	for _, q := range []string{"", "?queries=1"} { // a client cannot switch it on
		_, body := get(t, locked.URL+path+q)
		if strings.Contains(string(body), "FROM orders") {
			t.Fatalf("query text leaked with %q:\n%s", q, body)
		}
	}
	open := serve(t, src, true)
	if _, body := get(t, open.URL+path); strings.Contains(string(body), "FROM orders") {
		t.Fatalf("text shown without being asked for:\n%s", body)
	}
	resp, err := http.Get(open.URL + path + "?queries=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get("X-Observe-Queries") != "shown" {
		t.Fatalf("header %q", resp.Header.Get("X-Observe-Queries"))
	}
	_, body := get(t, open.URL+path+"?queries=1")
	if !strings.Contains(string(body), "FROM orders") {
		t.Fatalf("the text was not shown when allowed and asked for:\n%s", body)
	}
	// stripping must not alter the source's own data
	if c, _, _ := src.Case(context.Background(), "shop/orders/1"); c.History[0].Units[0].Text == "" {
		t.Fatal("the handler modified the source's data")
	}
}

func TestGoldenResponses(t *testing.T) {
	s := serve(t, Scoped(NewFixtureSource(testFixture()), Scope{Namespaces: []string{"shop"}}), false)
	for name, path := range map[string]string{
		"overview": "/observe/v1/overview",
		"deploys":  "/observe/v1/deploys",
		"case":     "/observe/v1/deploys/" + esc("shop/orders/1"),
		"series":   "/observe/v1/deploys/" + esc("shop/orders/1") + "/series?unit=q1",
		"actions":  "/observe/v1/actions",
	} {
		code, body := get(t, s.URL+path)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		golden(t, name, body)
	}
}

// The allowlist is enforced on every endpoint, and what is out of scope looks
// exactly like what does not exist.
func TestScopeIsEnforcedEverywhere(t *testing.T) {
	s := serve(t, Scoped(NewFixtureSource(testFixture()), Scope{Namespaces: []string{"shop"}}), true)

	for _, p := range []string{"/observe/v1/deploys", "/observe/v1/actions"} {
		_, body := get(t, s.URL+p)
		if strings.Contains(string(body), "secret") || strings.Contains(string(body), "ledger") {
			t.Errorf("%s leaks the out-of-scope namespace:\n%s", p, body)
		}
	}
	// Asking for it by id, or its series, is the same as asking for nothing.
	c1, b1 := get(t, s.URL+"/observe/v1/deploys/"+esc("secret/ledger/1"))
	c2, b2 := get(t, s.URL+"/observe/v1/deploys/"+esc("secret/ghost/9"))
	if c1 != 404 || c2 != 404 || string(b1) != string(b2) {
		t.Errorf("out of scope %d %q vs missing %d %q", c1, b1, c2, b2)
	}
	if c, _ := get(t, s.URL+"/observe/v1/deploys/"+esc("secret/ledger/1")+"/series?unit=q1"); c != 404 {
		t.Errorf("series out of scope: %d", c)
	}
	// And a namespace filter cannot widen it.
	_, body := get(t, s.URL+"/observe/v1/deploys?namespace=secret")
	if strings.Contains(string(body), "ledger") {
		t.Errorf("namespace filter widened the scope:\n%s", body)
	}
}

func TestEmptyScopeShowsNothing(t *testing.T) {
	// An explicit allowlist with no entries is not "all".
	src := Scoped(NewFixtureSource(testFixture()), Scope{})
	l, _ := src.Deploys(context.Background(), Range{})
	if len(l) != 0 {
		t.Fatalf("got %d deploys", len(l))
	}
}

func TestScopeStopsEventsToo(t *testing.T) {
	fx := NewFixtureSource(testFixture())
	_, cancel := Scoped(fx, Scope{Namespaces: []string{"shop"}}).Subscribe()
	defer cancel()
	cancel() // idempotent
	ch, cancel2 := Scoped(fx, Scope{Namespaces: []string{"shop"}}).Subscribe()
	defer cancel2()
	fx.Hub.Publish(Event{Type: EventEvaluation, Namespace: "secret", DeployID: "secret/ledger/1"})
	fx.Hub.Publish(Event{Type: EventHealth}) // about the radar: passes
	fx.Hub.Publish(Event{Type: EventEvaluation, Namespace: "shop", DeployID: "shop/cart/1"})
	var got []Event
	for len(got) < 2 {
		select {
		case e := <-ch:
			got = append(got, e)
		case <-time.After(time.Second):
			t.Fatalf("timed out with %v", got)
		}
	}
	if got[0].Type != EventHealth || got[1].DeployID != "shop/cart/1" {
		t.Fatalf("got %+v", got)
	}
	select {
	case e := <-ch:
		t.Fatalf("an out-of-scope event came through: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRangeParsing(t *testing.T) {
	s := serve(t, Scoped(NewFixtureSource(testFixture()), Scope{All: true}), false)
	ids := func(path string) []string {
		code, body := get(t, s.URL+path)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		var r struct{ Deploys []DeploySummary }
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, d := range r.Deploys {
			out = append(out, d.ID)
		}
		return out
	}
	if got := ids("/observe/v1/deploys"); len(got) != 3 || got[0] != "shop/cart/1" {
		t.Errorf("newest first expected, got %v", got)
	}
	if got := ids("/observe/v1/deploys?from=now-90m"); len(got) != 2 {
		t.Errorf("now-90m: %v", got)
	}
	if got := ids("/observe/v1/deploys?from=" + t0.Add(-3*time.Hour).Format(time.RFC3339) + "&to=now-90m"); len(got) != 1 || got[0] != "shop/orders/1" {
		t.Errorf("absolute from, relative to: %v", got)
	}
	if got := ids("/observe/v1/deploys?from=now-1d"); len(got) != 3 {
		t.Errorf("days: %v", got)
	}
	if got := ids("/observe/v1/deploys?limit=1"); len(got) != 1 {
		t.Errorf("limit: %v", got)
	}
	for _, bad := range []string{"from=yesterday", "from=now-xyz", "from=now&to=now-1h", "to=2026-13-01"} {
		if code, _ := get(t, s.URL+"/observe/v1/deploys?"+bad); code != 400 {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
	if code, _ := get(t, s.URL+"/observe/v1/deploys/"+esc("shop/orders/1")+"/series"); code != 400 {
		t.Errorf("series without unit: %d", code)
	}
}

func TestVersionAndReadOnly(t *testing.T) {
	s := serve(t, Scoped(NewFixtureSource(testFixture()), Scope{All: true}), false)
	_, body := get(t, s.URL+"/observe/version")
	if !strings.Contains(string(body), `"v1"`) {
		t.Fatalf("%s", body)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(m, s.URL+"/observe/v1/actions", strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s answered %d; the API must be read-only", m, resp.StatusCode)
		}
	}
}

func TestEventStream(t *testing.T) {
	fx := NewFixtureSource(testFixture())
	s := serve(t, Scoped(fx, Scope{Namespaces: []string{"shop"}}), false)
	resp, err := http.Get(s.URL + "/observe/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	want := func(sub string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream ended before %q", sub)
				}
				if strings.Contains(l, sub) {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %q", sub)
			}
		}
	}
	want("event: hello")
	// The subscription exists once hello is out; publish after it.
	fx.Hub.Publish(Event{Type: EventEvaluation, Namespace: "secret", DeployID: "secret/ledger/1"})
	fx.Hub.Publish(Event{Type: EventAction, Namespace: "shop", DeployID: "shop/orders/1", Summary: "aborted"})
	want("event: action")
	want(`"deployId":"shop/orders/1"`)
	want(": ping") // idle heartbeat
}

func TestHubNeverBlocksAndNumbersEvents(t *testing.T) {
	h := NewHub(2)
	ch, cancel := h.Subscribe()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ { // nobody reads: must not block
			h.Publish(Event{Type: EventHealth})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	a, b := <-ch, <-ch
	if a.Seq != 1 || b.Seq != 2 {
		t.Fatalf("seq %d,%d", a.Seq, b.Seq)
	}
	// A client that missed events sees the gap in Seq on the next one.
	h.Publish(Event{Type: EventHealth})
	if c := <-ch; c.Seq != 101 {
		t.Fatalf("expected a gap to 101, got %d", c.Seq)
	}
}

func TestSentenceAndFlags(t *testing.T) {
	if Flag("never-seen-code").Text == "" {
		t.Error("unknown flag codes must pass through readable")
	}
	if GradeNote("A") == "" || GradeNote("B") == "" || GradeNote("C") == "" {
		t.Error("grades need a plain note")
	}
}

// The recorded fixture (real engine output over a simulated workload) is a valid
// Source: the UI is developed and tested against it without a cluster.
func TestRecordedFixtureServes(t *testing.T) {
	f, err := LoadFixture("testdata/recorded-sim.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 2 || len(f.Series) == 0 {
		t.Fatalf("fixture is thin: %d cases, %d series", len(f.Cases), len(f.Series))
	}
	s := serve(t, Scoped(NewFixtureSource(f), Scope{All: true}), false)
	for _, c := range f.Cases {
		if code, body := get(t, s.URL+"/observe/v1/deploys/"+esc(c.ID)); code != 200 || !strings.Contains(string(body), c.Sentence[:10]) {
			t.Fatalf("%s: %d", c.ID, code)
		}
	}
	for k, sr := range f.Series {
		if code, _ := get(t, s.URL+"/observe/v1/deploys/"+esc(sr.DeployID)+"/series?unit="+url.QueryEscape(sr.Key)); code != 200 {
			t.Fatalf("series %s: %d", k, code)
		}
	}
}

// The servers apply a write timeout; an event stream must outlive it.
func TestEventStreamOutlivesTheServersWriteTimeout(t *testing.T) {
	mux := http.NewServeMux()
	(&Handler{Source: NewFixtureSource(testFixture()), Heartbeat: 30 * time.Millisecond}).Routes(mux)
	ts := httptest.NewUnstartedServer(mux)
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/observe/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(400 * time.Millisecond)
	pings := 0
	for sc.Scan() && time.Now().Before(deadline) {
		if strings.HasPrefix(sc.Text(), ": ping") {
			pings++
		}
	}
	if time.Now().Before(deadline) {
		t.Fatalf("the stream was cut at the write timeout after %d pings", pings)
	}
}

// The sentence is written in the manager's time zone and read in the viewer's, so
// it carries no clock time.
func TestSentenceHasNoClockTimes(t *testing.T) {
	d := DeploySummary{Health: "Regressed", Latest: &Decision{Grade: "A", Impact: 0.2},
		LastAction: &Action{At: t0, Action: "abort", Result: "done"}}
	for _, r := range []string{"done", "persisted", "recovered", "failed"} {
		d.LastAction.Result = r
		if s := Sentence(d, "enforce", 3); regexp.MustCompile(`\d{1,2}:\d{2}`).MatchString(s) {
			t.Errorf("%q has a clock time", s)
		}
	}
}
