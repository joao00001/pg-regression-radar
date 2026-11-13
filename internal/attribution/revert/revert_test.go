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

package revert_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/revert"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

// gh is a fake GitHub. Each field switches one behaviour.
type gh struct {
	mu          sync.Mutex
	existing    int    // search results
	defaultBr   string // repository default branch
	prBase      string // base of the merged PR
	prMergeSHA  string
	merged      bool
	gqlErr      string
	status      int // forced status for every call
	calls       []string
	gqlBody     string
	auth        []string
	revertCount int
}

func newGH() *gh { return &gh{defaultBr: "main", prBase: "main", prMergeSHA: sha, merged: true} }

func (g *gh) handler() http.Handler {
	mux := http.NewServeMux()
	rec := func(r *http.Request) {
		g.mu.Lock()
		g.calls = append(g.calls, r.Method+" "+r.URL.Path)
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		g.mu.Unlock()
	}
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		if g.status != 0 {
			w.WriteHeader(g.status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"total_count": g.existing})
	})
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": g.defaultBr})
	})
	mux.HandleFunc("/repos/o/r/commits/", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		pr := map[string]any{"number": 7, "node_id": "PR_node", "merge_commit_sha": g.prMergeSHA, "base": map[string]string{"ref": g.prBase}}
		if g.merged {
			pr["merged_at"] = "2026-01-01T00:00:00Z"
		}
		_ = json.NewEncoder(w).Encode([]any{pr})
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		b, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.gqlBody = string(b)
		g.mu.Unlock()
		if g.gqlErr != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"message": g.gqlErr}}})
			return
		}
		g.mu.Lock()
		g.revertCount++
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"revertPullRequest": map[string]any{
			"revertPullRequest": map[string]any{"number": 8, "url": "https://github.com/o/r/pull/8"}}}})
	})
	return mux
}

func setup(t *testing.T, g *gh, max int) *revert.Proposer {
	t.Helper()
	srv := httptest.NewServer(g.handler())
	t.Cleanup(srv.Close)
	p, err := revert.New(revert.Config{Repository: "o/r", APIURL: srv.URL, MaxPerDay: max},
		func(context.Context) (string, error) { return "tok-secret", nil })
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func in(id string) revert.Input { return revert.Input{DeployID: id, Revision: sha, Body: "diagnosis"} }

func TestOpensADraftRevertForAMergedPR(t *testing.T) {
	g := newGH()
	p := setup(t, g, 3)
	res := p.Propose(context.Background(), in("argocd-api-"+sha))
	if res.Outcome != revert.Opened || res.URL != "https://github.com/o/r/pull/8" {
		t.Fatalf("%+v", res)
	}
	var sent struct {
		Variables struct{ ID, Body string } `json:"variables"`
		Query     string                    `json:"query"`
	}
	if err := json.Unmarshal([]byte(g.gqlBody), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Variables.ID != "PR_node" || !strings.Contains(sent.Variables.Body, revert.Marker("argocd-api-"+sha)) {
		t.Fatalf("bad mutation variables: %+v", sent.Variables)
	}
	if !strings.Contains(sent.Query, "draft: true") {
		t.Fatal("the pull request must always be a draft")
	}
	for _, a := range g.auth {
		if a != "Bearer tok-secret" {
			t.Fatalf("auth header %q", a)
		}
	}
}

func TestIdempotentWithinAProcess(t *testing.T) {
	g := newGH()
	p := setup(t, g, 3)
	p.Propose(context.Background(), in("d1"))
	if res := p.Propose(context.Background(), in("d1")); res.Outcome != revert.AlreadyProposed {
		t.Fatalf("%+v", res)
	}
	if g.revertCount != 1 {
		t.Fatalf("opened %d pull requests", g.revertCount)
	}
}

func TestIdempotentAcrossRestarts(t *testing.T) {
	g := newGH()
	g.existing = 1 // an earlier process opened it; the search finds the marker
	p := setup(t, g, 3)
	if res := p.Propose(context.Background(), in("d1")); res.Outcome != revert.AlreadyProposed {
		t.Fatalf("%+v", res)
	}
	if g.revertCount != 0 {
		t.Fatal("must not open a second pull request")
	}
}

func TestRefusesWhatItCannotRevertSafely(t *testing.T) {
	cases := map[string]struct {
		mutate func(*gh)
		input  revert.Input
	}{
		"pod hash is not a commit": {func(*gh) {}, revert.Input{DeployID: "d", Revision: "7d9f6b5c8d", Body: "x"}},
		"short sha":                {func(*gh) {}, revert.Input{DeployID: "d", Revision: sha[:7], Body: "x"}},
		"uppercase or tag":         {func(*gh) {}, revert.Input{DeployID: "d", Revision: "v1.2.3", Body: "x"}},
		"hostile deploy id":        {func(*gh) {}, revert.Input{DeployID: "d\" OR repo:x", Revision: sha, Body: "x"}},
		"not merged":               {func(g *gh) { g.merged = false }, in("d")},
		"merged into another base": {func(g *gh) { g.prBase = "release" }, in("d")},
		"not the merge commit":     {func(g *gh) { g.prMergeSHA = strings.Repeat("a", 40) }, in("d")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g := newGH()
			c.mutate(g)
			res := setup(t, g, 3).Propose(context.Background(), c.input)
			if res.Outcome != revert.Skipped {
				t.Fatalf("%+v", res)
			}
			if g.revertCount != 0 {
				t.Fatal("opened a pull request it should have refused")
			}
		})
	}
}

func TestDailyLimit(t *testing.T) {
	g := newGH()
	p := setup(t, g, 2)
	for _, id := range []string{"a", "b"} {
		if res := p.Propose(context.Background(), in(id)); res.Outcome != revert.Opened {
			t.Fatalf("%s: %+v", id, res)
		}
	}
	if res := p.Propose(context.Background(), in("c")); res.Outcome != revert.Skipped || !strings.Contains(res.Reason, "daily limit") {
		t.Fatalf("%+v", res)
	}
	if g.revertCount != 2 {
		t.Fatalf("opened %d", g.revertCount)
	}
}

func TestAPIFailuresAreReportedAndNotRemembered(t *testing.T) {
	g := newGH()
	g.status = 500
	p := setup(t, g, 3)
	if res := p.Propose(context.Background(), in("d")); res.Outcome != revert.Failed {
		t.Fatalf("%+v", res)
	}
	g.status = 0 // GitHub recovers: a retry must go through
	if res := p.Propose(context.Background(), in("d")); res.Outcome != revert.Opened {
		t.Fatalf("a failed attempt must not block the retry: %+v", res)
	}
}

func TestGraphQLErrorIsAFailure(t *testing.T) {
	g := newGH()
	g.gqlErr = "merge conflict"
	if res := setup(t, g, 3).Propose(context.Background(), in("d")); res.Outcome != revert.Failed || !strings.Contains(res.Reason, "merge conflict") {
		t.Fatalf("%+v", res)
	}
}

func TestTokenErrorNeverReachesGitHubAndIsNotLeaked(t *testing.T) {
	g := newGH()
	srv := httptest.NewServer(g.handler())
	defer srv.Close()
	p, _ := revert.New(revert.Config{Repository: "o/r", APIURL: srv.URL},
		func(context.Context) (string, error) { return "", errors.New("secret x is missing: s3cr3t") })
	res := p.Propose(context.Background(), in("d"))
	if res.Outcome != revert.Failed || strings.Contains(res.Reason, "s3cr3t") {
		t.Fatalf("%+v", res)
	}
	if len(g.calls) != 0 {
		t.Fatal("no request may be made without a token")
	}
}

func TestConfigurationThatCouldLeakTheTokenIsRejected(t *testing.T) {
	tok := func(context.Context) (string, error) { return "t", nil }
	for name, c := range map[string]revert.Config{
		"plain http to a remote host": {Repository: "o/r", APIURL: "http://github.example.com"},
		"bad repository":              {Repository: "not a repo"},
		"repository with a path":      {Repository: "o/r/../x"},
		"no scheme":                   {Repository: "o/r", APIURL: "github.example.com"},
	} {
		if _, err := revert.New(c, tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := revert.New(revert.Config{Repository: "o/r"}, tok); err != nil {
		t.Errorf("default api url rejected: %v", err)
	}
	if _, err := revert.New(revert.Config{Repository: "o/r", APIURL: "https://ghe.example.com/api/v3"}, tok); err != nil {
		t.Errorf("enterprise url rejected: %v", err)
	}
}

func TestRedirectIsNeverFollowedWithTheToken(t *testing.T) {
	var leaked bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
	}))
	defer evil.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL, http.StatusFound)
	}))
	defer redir.Close()
	p, _ := revert.New(revert.Config{Repository: "o/r", APIURL: redir.URL}, func(context.Context) (string, error) { return "tok", nil })
	p.Propose(context.Background(), in("d"))
	if leaked {
		t.Fatal("the token was sent to a redirect target")
	}
}

func TestDiagnosisBody(t *testing.T) {
	units, ev, d, _ := sim.World{Seed: 3, Queries: 60, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	a := attribution.Assess(attribution.Config{}, units, ev, d)
	dec := attribution.Decision{Grade: attribution.GradeB, Impact: 0.12, Streak: 2, Reasons: []string{"grade B evidence supports 12.0% added database time"}}
	base := revert.DiagnosisInput{App: "api", Namespace: "prod", Revision: sha, Assessment: a, Decision: dec, PriorDeploySeen: true}

	body := revert.Diagnosis(base)
	for _, want := range []string{"draft", "Grade B", "12.0%", "Database migrations", "Most affected queries"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	for _, u := range a.Units {
		if u.Kind == attribution.KindRegressed && u.Text != "" && strings.Contains(body, u.Text) {
			t.Fatal("query text must not be included by default")
		}
	}
	if strings.Contains(body, "No earlier deploy was observed") {
		t.Error("warned about a first deploy when a prior one was seen")
	}
	base.PriorDeploySeen = false
	if !strings.Contains(revert.Diagnosis(base), "No earlier deploy was observed") {
		t.Error("no warning when no prior deploy was seen")
	}
	base.IncludeQueries = true
	with := revert.Diagnosis(base)
	if len(with) <= len(body) {
		t.Error("IncludeQueries added nothing")
	}
}

func TestDiagnosisNeutralisesMarkdownAndMentions(t *testing.T) {
	a := attribution.Assessment{Units: []attribution.UnitVerdict{{
		Kind: attribution.KindRegressed, Key: "k", Text: "SELECT 1 -- @everyone |<script>| `x` #123\nDROP", Ratio: 2, RatioLow: 1.5, RatioHigh: 2.5,
	}}}
	body := revert.Diagnosis(revert.DiagnosisInput{App: "a`b", Namespace: "n@s", Revision: sha, Assessment: a, Decision: attribution.Decision{Grade: attribution.GradeB}, IncludeQueries: true})
	for _, bad := range []string{"@everyone", "<script>", "#123", "a`b", "n@s"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains %q", bad)
		}
	}
}
