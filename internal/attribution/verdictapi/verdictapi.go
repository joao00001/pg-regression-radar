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

// Package verdictapi serves the attribution engine's verdict on a deploy to a
// progressive-delivery controller. It is read-only: it never changes anything in
// the cluster, the controller that asks acts on the answer.
//
// The shape follows how Argo Rollouts consumes an external metric. An
// AnalysisTemplate with a web provider reads `$.verdict` and passes when it is
// "pass", fails (aborting the rollout) when it is "fail", and is inconclusive
// (pausing the rollout at its current step) otherwise. Anything that cannot be
// decided is "pass", so a missing evaluation, an unknown deploy or a watch in
// shadow mode never blocks a release.
package verdictapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
)

// Handler answers GET /api/v1/attribution/verdict.
type Handler struct {
	// Evaluators returns the evaluators to search, typically
	// controller.Registry.Shadows.
	Evaluators func() []*shadow.Evaluator
}

// Response is the JSON body. Verdict is the only field a controller needs.
type Response struct {
	Verdict attribution.Verdict `json:"verdict"`
	// Pending is true when no evaluation exists yet (the verdict is then "pass").
	Pending bool `json:"pending"`
	// Known is false when no deploy of that workload is tracked.
	Known bool `json:"known"`
	// Enforced is false in shadow mode, where Verdict is always "pass".
	Enforced bool `json:"enforced"`
	// WouldBe is the verdict the engine would serve if it were enforcing.
	WouldBe     attribution.Verdict `json:"wouldBe"`
	Action      attribution.Action  `json:"action,omitempty"`
	Grade       attribution.Grade   `json:"grade,omitempty"`
	Impact      float64             `json:"impact,omitempty"`
	Attribution float64             `json:"attribution,omitempty"`
	DeployID    string              `json:"deployId,omitempty"`
	EvaluatedAt *time.Time          `json:"evaluatedAt,omitempty"`
	Reasons     []string            `json:"reasons,omitempty"`
}

// Routes registers the endpoint on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/attribution/verdict", h.verdict)
}

func (h *Handler) verdict(w http.ResponseWriter, r *http.Request) {
	ns, app, rev := r.URL.Query().Get("namespace"), r.URL.Query().Get("app"), r.URL.Query().Get("revision")
	if ns == "" || app == "" {
		http.Error(w, "namespace and app are required", http.StatusBadRequest)
		return
	}
	resp := Response{Verdict: attribution.VerdictPass, WouldBe: attribution.VerdictPass}
	var evs []*shadow.Evaluator
	if h.Evaluators != nil {
		evs = h.Evaluators()
	}
	for _, ev := range evs {
		d, rec, found, ok := ev.Latest(ns, app, rev)
		if !found {
			continue
		}
		resp.Known = true
		resp.DeployID = d.ID
		if !ok {
			resp.Pending = true
			break
		}
		at := rec.At
		dec := rec.Decision
		resp.Verdict = dec.Verdict()
		resp.Enforced = dec.Enforced
		resp.Action, resp.Grade, resp.Impact = dec.Action, dec.Grade, dec.Impact
		resp.Attribution = rec.Assessment.Attribution
		resp.EvaluatedAt = &at
		resp.Reasons = dec.Reasons
		resp.WouldBe = wouldBe(dec)
		break
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// wouldBe is the verdict an enforcing policy would serve for dec.
func wouldBe(dec attribution.Decision) attribution.Verdict {
	switch dec.Action {
	case attribution.ActionRollback:
		return attribution.VerdictFail
	case attribution.ActionHold:
		return attribution.VerdictInconclusive
	}
	return attribution.VerdictPass
}
