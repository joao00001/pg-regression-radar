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

import "fmt"

// GradeNote says in one sentence what a grade means, so a badge never stands
// alone.
func GradeNote(grade string) string {
	switch grade {
	case "A":
		return "Concurrent control: the same query on the pods the deploy did not reach, at the same time. The strongest evidence; the only one that justifies an abort."
	case "B":
		return "Synthetic control: built from queries the deploy did not touch and checked against fake deploy times. Strong enough to pause, not to abort."
	case "C":
		return "The query's own history only. It cannot tell the deploy from anything else that happened then, so it can alert but not act."
	case "D":
		return "Not enough data to say anything."
	}
	return ""
}

// flagTexts are the plain-language sentences for the engine's flags. The codes
// are the engine's own and are what a user searches the docs for.
var flagTexts = map[string]string{
	"new-unit":             "No history: the query is new, or its text changed.",
	"low-data":             "Too few observations after the deploy so far.",
	"global-shift":         "Most untouched queries moved too: load, or the deploy itself. Undecidable without a control arm.",
	"few-donors":           "Not enough untouched queries to build a synthetic control.",
	"placebo-failed":       "The counterfactual produced false effects at fake deploy times, so it is not trusted.",
	"space-placebo-failed": "The effect is not unusual among untouched queries.",
	"control-unreliable":   "The control arm did not track the treatment before the deploy.",
	"confounded":           "Another event could explain the shift.",
	"warming-up":           "Still inside the grace period after the deploy.",
	"table-level":          "Most queries on one table moved together, which points at the table, not the code.",
	"transient":            "The shift was there but faded after the grace period (a cold cache, a rollout in progress).",
	"poor-fit":             "The model left structure in the pre-deploy residuals.",
	"extrapolating":        "Covariates moved well outside what the pre-deploy period showed.",
}

// Flag builds a FlagInfo for a code. An unknown code (a newer engine) is passed
// through with a generic sentence rather than dropped.
func Flag(code string) FlagInfo {
	if t, ok := flagTexts[code]; ok {
		return FlagInfo{Code: code, Text: t}
	}
	return FlagInfo{Code: code, Text: "Reported by the engine; see the attribution engine documentation."}
}

// Sentence is the one plain line at the end of a card. It states what was seen
// and what was done, and says so plainly when the evidence is thin.
func Sentence(s DeploySummary, mode string, regressed int) string {
	if s.Latest == nil {
		return "Registered; the first judgement comes after the grace period."
	}
	d := s.Latest
	pct := fmt.Sprintf("%.0f%%", d.Impact*100)
	acted := ""
	if a := s.LastAction; a != nil && a.Result != "skipped" {
		// No clock times in the sentence: it is written here, in the manager's time
		// zone, and read there, in the viewer's. The card carries the time itself.
		acted = fmt.Sprintf("; %s %s", a.Action, doneWord(a.Result))
		if a.Action == "abort" && (a.Result == "recovered" || a.Result == "persisted") {
			acted = fmt.Sprintf("; abort checked: regression %s", a.Result)
		}
	}
	switch s.Health {
	case "Regressed":
		what := fmt.Sprintf("Grade %s: %d %s slower, %s of database time added", d.Grade, regressed, plural(regressed, "query", "queries"), pct)
		if d.Grade == "C" || d.Grade == "D" {
			what = fmt.Sprintf("Grade %s (history only): %d %s look slower, %s of database time, but the evidence cannot say it was this deploy", d.Grade, regressed, plural(regressed, "query", "queries"), pct)
		}
		if mode != "enforce" {
			return what + "; shadow mode, nothing is acted on" + acted
		}
		return what + acted
	case "Healthy":
		return "Healthy: nothing regressed beyond the margin" + acted
	case "InsufficientData":
		return "Too little of the workload could be tested to say" + acted
	default:
		return "Inconclusive: nothing regressed, but the evidence does not rule it out" + acted
	}
}

func doneWord(result string) string {
	switch result {
	case "done":
		return "applied"
	case "failed":
		return "failed"
	}
	return result
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
