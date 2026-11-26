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

// Package observe is the read-only API the live dashboard consumes.
//
// It is a contract, not a view of the engine's internals: the types here are the
// only thing a client sees, they are versioned, and fields are only ever added.
// Everything the engine knows that a person needs in order to trust a decision
// is in it: what was seen (per-query evidence), what was concluded and why
// (grade, reasons, cap), what was done (the ledger) and whether it helped (the
// verification of an abort).
package observe

import "time"

// Version is the contract version served at /observe/version. A client that
// does not understand it must not guess.
const Version = "v1"

// Scope says which namespaces the API may show. It is applied inside the API,
// on every endpoint, so a client cannot ask around it.
type Scope struct {
	// All is true when no allowlist is set: every namespace the deploy sources
	// report.
	All        bool     `json:"all"`
	Namespaces []string `json:"namespaces,omitempty"`
}

// Allows reports whether a namespace is visible.
func (s Scope) Allows(ns string) bool {
	if s.All {
		return true
	}
	for _, n := range s.Namespaces {
		if n == ns {
			return true
		}
	}
	return false
}

// History says where the history comes from and how far back it reaches, so a
// short list is never mistaken for a complete one.
type History struct {
	// Store is "memory" or "postgres".
	Store string `json:"store"`
	// Since is the oldest moment the store can answer for; zero when empty.
	Since *time.Time `json:"since,omitempty"`
	// RetentionSeconds is the configured retention; 0 means bounded by size only.
	RetentionSeconds int64 `json:"retentionSeconds,omitempty"`
	// Recording is false when the store could not be written to: the radar keeps
	// acting, but this stretch of history will have a gap.
	Recording bool `json:"recording"`
	// Note is a short human sentence for the status strip.
	Note string `json:"note,omitempty"`
}

// Overview is the status strip: is the radar alive and what may it do.
type Overview struct {
	Version     string        `json:"version"`
	GeneratedAt time.Time     `json:"generatedAt"`
	Scope       Scope         `json:"scope"`
	History     History       `json:"history"`
	Watches     []WatchStatus `json:"watches"`
}

// WatchStatus is the state of one PostgresWatch.
type WatchStatus struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Cluster   string `json:"cluster"`
	// Mode is "shadow" (judges and logs, serves pass) or "enforce".
	Mode      string          `json:"mode"`
	Actuation ActuationStatus `json:"actuation"`
	// BreakerOpen is true when earlier aborts did not make the regression go
	// away and automatic actions are stopped.
	BreakerOpen bool            `json:"breakerOpen"`
	Collector   CollectorHealth `json:"collector"`
	Arms        *ArmsHealth     `json:"arms,omitempty"`
}

// ActuationStatus is what the manager may do to a Rollout.
type ActuationStatus struct {
	Enabled bool `json:"enabled"`
	// Paused is true when the kill-switch annotation is set.
	Paused     bool     `json:"paused"`
	Namespaces []string `json:"namespaces,omitempty"`
}

// CollectorHealth says whether the data the engine reads is fresh.
type CollectorHealth struct {
	LastScrape *time.Time `json:"lastScrape,omitempty"`
	// StaleSeconds is how old the last scrape is; -1 when there has been none.
	StaleSeconds int64 `json:"staleSeconds"`
}

// ArmsHealth is the state of the Postgres-log reader behind grade A.
type ArmsHealth struct {
	Usable  bool       `json:"usable"`
	Records int64      `json:"records"`
	Dropped int64      `json:"dropped"`
	Newest  *time.Time `json:"newest,omitempty"`
	// Why is a short sentence when Usable is false.
	Why string `json:"why,omitempty"`
}

// Phase is where a deploy is in the radar's handling.
type Phase string

const (
	// PhaseObserving: registered, not decided yet (or still being judged).
	PhaseObserving Phase = "observing"
	// PhaseDecided: the radar has a decision and did not act (none, alert, or
	// shadow mode).
	PhaseDecided Phase = "decided"
	// PhaseActed: the radar paused or aborted.
	PhaseActed Phase = "acted"
	// PhaseVerified: an abort was checked afterwards.
	PhaseVerified Phase = "verified"
)

// DeploySummary is one card: where a deploy is and what the radar made of it.
type DeploySummary struct {
	ID        string    `json:"id"`
	Namespace string    `json:"namespace"`
	App       string    `json:"app"`
	Revision  string    `json:"revision,omitempty"`
	At        time.Time `json:"at"`
	Phase     Phase     `json:"phase"`
	// NextEvaluation is when the radar will judge it next; nil when it will not.
	NextEvaluation *time.Time `json:"nextEvaluation,omitempty"`
	Evaluations    int        `json:"evaluations"`
	// Latest is nil until the first evaluation.
	Latest *Decision `json:"latest,omitempty"`
	// Health is the engine's reading of the whole deploy.
	Health string `json:"health,omitempty"`
	// Sentence is one plain line a person can read without the rest.
	Sentence string `json:"sentence"`
	// LastAction is the newest ledger entry for this deploy.
	LastAction *Action `json:"lastAction,omitempty"`
}

// Decision is what the policy decided at one evaluation.
type Decision struct {
	At time.Time `json:"at"`
	// Action is what the policy did: none, alert, hold or rollback.
	Action string `json:"action"`
	// Wanted is what the evidence called for before a safety guard capped it.
	Wanted      string   `json:"wanted"`
	Grade       string   `json:"grade"`
	GradeNote   string   `json:"gradeNote"`
	Impact      float64  `json:"impact"`
	Attribution float64  `json:"attribution"`
	Streak      int      `json:"streak"`
	Enforced    bool     `json:"enforced"`
	Reasons     []string `json:"reasons,omitempty"`
}

// Action is one entry of the ledger.
type Action struct {
	At        time.Time `json:"at"`
	DeployID  string    `json:"deployId"`
	Namespace string    `json:"namespace"`
	App       string    `json:"app"`
	// Action is "pause" or "abort".
	Action string `json:"action"`
	// Result is done, failed or skipped, then recovered or persisted for the
	// check after an abort.
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

// Case is everything about one deploy: the cards' detail view.
type Case struct {
	DeploySummary
	History []Evaluation `json:"history"`
	Actions []Action     `json:"actions"`
}

// Evaluation is one judging of a deploy and its evidence.
type Evaluation struct {
	At          time.Time `json:"at"`
	Health      string    `json:"health"`
	Coverage    float64   `json:"coverage"`
	Impact      ImpactBy  `json:"impact"`
	PointImpact float64   `json:"pointImpact"`
	Attribution float64   `json:"attribution"`
	GlobalShift bool      `json:"globalShift"`
	Counts      Counts    `json:"counts"`
	Decision    Decision  `json:"decision"`
	// CappedBy says what held the evidence at grade C, when it was.
	CappedBy []string       `json:"cappedBy,omitempty"`
	Units    []UnitEvidence `json:"units"`
}

// ImpactBy is the supported added database time, by the weakest grade admitted.
type ImpactBy struct {
	A   float64 `json:"a"`
	AB  float64 `json:"ab"`
	ABC float64 `json:"abc"`
}

// Counts tallies the units of an evaluation by verdict.
type Counts struct {
	Tested       int `json:"tested"`
	Regressed    int `json:"regressed"`
	Improved     int `json:"improved"`
	Healthy      int `json:"healthy"`
	Inconclusive int `json:"inconclusive"`
	Untestable   int `json:"untestable"`
}

// FlagInfo is a flag with a stable code and a sentence.
type FlagInfo struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// UnitEvidence is the engine's finding for one query.
type UnitEvidence struct {
	// Key is the stable fingerprint of the query; Family is the statement kind
	// and the tables it touches ("SELECT:orders"), which is what a person
	// recognises. Text is the normalised SQL and is empty unless the dashboard
	// was started with --show-queries.
	Key    string `json:"key"`
	Family string `json:"family"`
	Text   string `json:"text,omitempty"`

	Kind      string     `json:"kind"`
	Grade     string     `json:"grade"`
	GradeNote string     `json:"gradeNote"`
	Method    string     `json:"method"`
	Flags     []FlagInfo `json:"flags,omitempty"`

	Ratio     float64 `json:"ratio"`
	RatioLow  float64 `json:"ratioLow"`
	RatioHigh float64 `json:"ratioHigh"`
	PReg      float64 `json:"pReg"`
	Rejected  bool    `json:"rejected"`

	PreN        int     `json:"preN"`
	PostN       int     `json:"postN"`
	Donors      int     `json:"donors"`
	ImpactShare float64 `json:"impactShare"`
	OnsetSecs   int64   `json:"onsetSeconds"`
	Persisting  bool    `json:"persisting"`
}

// Point is one observation of a series.
type Point struct {
	At     time.Time `json:"at"`
	MeanMs float64   `json:"meanMs"`
	// N is how many calls (or sampled executions) stand behind the mean, so a
	// chart can show how thin the evidence was.
	N int64 `json:"n"`
}

// Series is the chart data for one query of one deploy: what the engine
// compared. Control is present for a grade A comparison.
type Series struct {
	DeployID  string    `json:"deployId"`
	Namespace string    `json:"namespace"`
	Key       string    `json:"key"`
	DeployAt  time.Time `json:"deployAt"`
	Treatment []Point   `json:"treatment"`
	Control   []Point   `json:"control,omitempty"`
	// Whole is the unit as pg_stat_statements reports it (every client together),
	// when it differs from Treatment.
	Whole []Point `json:"whole,omitempty"`
}

// EventType names what changed.
type EventType string

const (
	EventDeployRegistered EventType = "deploy.registered"
	EventEvaluation       EventType = "evaluation"
	EventAction           EventType = "action"
	EventHealth           EventType = "health"
)

// Event is one change, sent on the live stream. It carries the id of the deploy
// it concerns so a client refetches only that case; it never carries evidence,
// so the stream stays small and the snapshot stays the single source of truth.
type Event struct {
	Seq       uint64    `json:"seq"`
	At        time.Time `json:"at"`
	Type      EventType `json:"type"`
	Namespace string    `json:"namespace,omitempty"`
	DeployID  string    `json:"deployId,omitempty"`
	Summary   string    `json:"summary,omitempty"`
}
