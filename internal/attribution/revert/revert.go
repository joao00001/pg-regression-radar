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

// Package revert opens a draft pull request that reverts the change behind a
// deploy the attribution engine has convicted.
//
// It is built around what GitOps asks of a rollback. With Argo CD's automated
// sync on, the application cannot be rolled back from the Argo CD side ("Rollback
// cannot be performed against an application with automated sync enabled"), and
// anything done to the cluster directly is undone by the next sync, so the
// durable fix is a revert in git. GitHub can build that revert itself
// (the GraphQL revertPullRequest mutation: "Create a pull request that reverts
// the changes from a merged pull request"), which also keeps merge commits and
// squash merges out of this code.
//
// The package is deliberately a proposer, not an actor:
//
//   - the pull request is always a draft and is never merged here; a person
//     reviews it, which is the one safeguard that does not depend on the
//     statistics being right;
//   - it acts at most once per deploy, across restarts (a marker in the body is
//     searched for) and within a process (an in-memory set);
//   - it opens at most MaxPerDay pull requests per watch, so a bad day cannot
//     bury a repository;
//   - it only reverts a commit that is the merge commit of a merged pull request
//     into the default branch, so a hash that is really a pod hash, a tag or a
//     direct push is refused, not guessed at;
//   - the token never leaves this package except in the Authorization header of
//     requests to the configured API host, which must be https (loopback is
//     allowed for tests), and it is read through a function per call, not stored.
package revert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Config says where and how to propose reverts.
type Config struct {
	// Repository is "owner/name".
	Repository string
	// APIURL is the GitHub REST base, "https://api.github.com" by default
	// (a GitHub Enterprise Server uses "https://host/api/v3"). It must be https,
	// except for a loopback host.
	APIURL string
	// MaxPerDay caps the pull requests opened in any 24 hours (default 3).
	MaxPerDay int
}

// Outcome is what Propose did.
type Outcome string

const (
	// Opened means a draft revert pull request now exists.
	Opened Outcome = "opened"
	// AlreadyProposed means this deploy was already handled; nothing was created.
	AlreadyProposed Outcome = "already-proposed"
	// Skipped means a guard declined to act; Reason says which.
	Skipped Outcome = "skipped"
	// Failed means an API call failed; the caller may try again later.
	Failed Outcome = "failed"
)

// Result is the outcome of one Propose call.
type Result struct {
	Outcome Outcome
	Reason  string
	URL     string
	Number  int
}

// Input is one deploy to propose a revert for.
type Input struct {
	// DeployID identifies the deploy; it is the idempotency key.
	DeployID string
	// Revision must be the full 40-hex git commit SHA that the deploy shipped.
	Revision string
	// Body is the diagnosis to put in the pull request. A marker line carrying
	// DeployID is appended to it.
	Body string
}

// Proposer opens revert pull requests.
type Proposer struct {
	cfg   Config
	token func(ctx context.Context) (string, error)
	http  *http.Client
	now   func() time.Time

	mu     sync.Mutex
	done   map[string]bool
	opened []time.Time
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
var repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var idRE = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,200}$`)

// New returns a Proposer. token is called for every request batch and its result
// is not kept. It returns an error for a configuration that could leak the token
// or never work.
func New(cfg Config, token func(ctx context.Context) (string, error)) (*Proposer, error) {
	if !repoRE.MatchString(cfg.Repository) {
		return nil, fmt.Errorf("revert: repository %q must be owner/name", cfg.Repository)
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com"
	}
	u, err := url.Parse(cfg.APIURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("revert: invalid api url %q", cfg.APIURL)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return nil, fmt.Errorf("revert: api url must be https, the token is sent to it")
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	if cfg.MaxPerDay <= 0 {
		cfg.MaxPerDay = 3
	}
	if token == nil {
		return nil, errors.New("revert: a token source is required")
	}
	return &Proposer{
		cfg: cfg, token: token, now: time.Now, done: map[string]bool{},
		http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // never follow a redirect with the token attached
		}},
	}, nil
}

func isLoopback(h string) bool { return h == "127.0.0.1" || h == "localhost" || h == "::1" }

// Marker is the line put in every pull request body so a later run can find it.
func Marker(deployID string) string { return "pgrr-revert-id: " + deployID }

// Propose opens a draft revert pull request for the deploy, or says why not.
func (p *Proposer) Propose(ctx context.Context, in Input) Result {
	if !idRE.MatchString(in.DeployID) {
		return Result{Outcome: Skipped, Reason: "deploy id has characters that cannot be used as an idempotency key"}
	}
	if !shaRE.MatchString(in.Revision) {
		return Result{Outcome: Skipped, Reason: "the deploy's revision is not a 40-character git commit SHA (a pod hash or tag cannot be reverted in git)"}
	}

	p.mu.Lock()
	if p.done[in.DeployID] {
		p.mu.Unlock()
		return Result{Outcome: AlreadyProposed, Reason: "this deploy was already handled by this process"}
	}
	cutoff := p.now().Add(-24 * time.Hour)
	recent := p.opened[:0]
	for _, t := range p.opened {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	p.opened = recent
	if len(p.opened) >= p.cfg.MaxPerDay {
		p.mu.Unlock()
		return Result{Outcome: Skipped, Reason: fmt.Sprintf("daily limit of %d revert pull requests reached", p.cfg.MaxPerDay)}
	}
	p.mu.Unlock()

	tok, err := p.token(ctx)
	if err != nil || tok == "" {
		return Result{Outcome: Failed, Reason: "could not read the GitHub token"}
	}

	// Durable idempotency: has a pull request for this deploy been opened by an
	// earlier process? The search index lags by a short while, so the in-memory
	// set above covers the gap within a process.
	q := fmt.Sprintf(`repo:%s is:pr "%s"`, p.cfg.Repository, Marker(in.DeployID))
	var found struct {
		TotalCount int `json:"total_count"`
	}
	if err := p.get(ctx, tok, "/search/issues?per_page=1&q="+url.QueryEscape(q), &found); err != nil {
		return Result{Outcome: Failed, Reason: "searching for an existing revert failed: " + err.Error()}
	}
	if found.TotalCount > 0 {
		p.remember(in.DeployID, false)
		return Result{Outcome: AlreadyProposed, Reason: "a pull request for this deploy already exists"}
	}

	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := p.get(ctx, tok, "/repos/"+p.cfg.Repository, &repo); err != nil || repo.DefaultBranch == "" {
		return Result{Outcome: Failed, Reason: "reading the repository failed"}
	}

	var prs []struct {
		Number         int        `json:"number"`
		NodeID         string     `json:"node_id"`
		MergedAt       *time.Time `json:"merged_at"`
		MergeCommitSHA string     `json:"merge_commit_sha"`
		HTMLURL        string     `json:"html_url"`
		Base           struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := p.get(ctx, tok, "/repos/"+p.cfg.Repository+"/commits/"+in.Revision+"/pulls?per_page=100", &prs); err != nil {
		return Result{Outcome: Failed, Reason: "finding the pull request for the commit failed: " + err.Error()}
	}
	nodeID, number := "", 0
	for _, pr := range prs {
		if pr.MergedAt != nil && pr.MergeCommitSHA == in.Revision && pr.Base.Ref == repo.DefaultBranch {
			nodeID, number = pr.NodeID, pr.Number
			break
		}
	}
	if nodeID == "" {
		return Result{Outcome: Skipped, Reason: "the commit is not the merge commit of a merged pull request into " + repo.DefaultBranch + " (a direct push, or a commit another release built on)"}
	}

	body := strings.TrimRight(in.Body, "\n") + "\n\n" + Marker(in.DeployID) + "\n"
	var gql struct {
		Data struct {
			Revert struct {
				RevertPullRequest struct {
					Number int    `json:"number"`
					URL    string `json:"url"`
				} `json:"revertPullRequest"`
			} `json:"revertPullRequest"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	req := map[string]any{
		"query": `mutation($id: ID!, $body: String!) {
  revertPullRequest(input: {pullRequestId: $id, body: $body, draft: true}) {
    revertPullRequest { number url }
  }
}`,
		"variables": map[string]any{"id": nodeID, "body": body},
	}
	if err := p.post(ctx, tok, p.graphqlURL(), req, &gql); err != nil {
		return Result{Outcome: Failed, Reason: "creating the revert failed: " + err.Error()}
	}
	if len(gql.Errors) > 0 {
		return Result{Outcome: Failed, Reason: "creating the revert failed: " + gql.Errors[0].Message}
	}
	r := gql.Data.Revert.RevertPullRequest
	if r.URL == "" {
		return Result{Outcome: Failed, Reason: "GitHub did not return the new pull request"}
	}
	p.remember(in.DeployID, true)
	return Result{Outcome: Opened, URL: r.URL, Number: r.Number, Reason: fmt.Sprintf("reverts #%d", number)}
}

func (p *Proposer) remember(id string, opened bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done[id] = true
	if opened {
		p.opened = append(p.opened, p.now())
	}
}

// graphqlURL is the GraphQL endpoint matching the REST base: api.github.com
// serves it at /graphql, and a GitHub Enterprise Server at /api/graphql next to
// its /api/v3 REST root.
func (p *Proposer) graphqlURL() string {
	if strings.HasSuffix(p.cfg.APIURL, "/api/v3") {
		return strings.TrimSuffix(p.cfg.APIURL, "/v3") + "/graphql"
	}
	return p.cfg.APIURL + "/graphql"
}

func (p *Proposer) get(ctx context.Context, tok, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.APIURL+path, nil)
	if err != nil {
		return err
	}
	return p.do(req, tok, out)
}

func (p *Proposer) post(ctx context.Context, tok, u string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return p.do(req, tok, out)
}

func (p *Proposer) do(req *http.Request, tok string, out any) error {
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := p.http.Do(req)
	if err != nil {
		// A transport error can quote the URL; it never contains the token.
		return fmt.Errorf("request failed")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github answered %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unreadable answer")
	}
	return nil
}
