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
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/reason"
)

// Condition types a running PostgresWatch reports besides Ready. They answer the
// three questions "kubectl describe" could not: is the radar reading the
// database, can it tell a canary from the stable pods, and may it act. Each one
// carries a reason from internal/reason and, when it is not healthy, a link to
// the docs entry for that reason.
const (
	// ConditionCollecting is True while the collector scrapes on schedule. It is
	// absent for a watch whose sampleSource is not the built-in collector.
	ConditionCollecting = "Collecting"
	// ConditionArmsLog is True while the Postgres log is flowing and its settings
	// allow grade A evidence. It is absent when attribution.arms is not enabled.
	ConditionArmsLog = "ArmsLog"
	// ConditionActuating is True only when the manager may pause and abort
	// Rollouts for this watch right now. It is absent when the attribution
	// engine is off.
	ConditionActuating = "Actuating"
)

var healthConditionTypes = []string{ConditionCollecting, ConditionArmsLog, ConditionActuating}

// scrapeStaleAfter is how many scrape intervals may pass without a scrape before
// the collector is reported as not scraping on schedule. One missed scrape is
// normal (a slow query on the database); three is not.
const scrapeStaleAfter = 3

// scrapeHealth judges the collector from the time of its last scrape.
func scrapeHealth(last time.Time, interval time.Duration, now time.Time) (ok bool, code reason.Code, msg string) {
	switch {
	case last.IsZero():
		return false, reason.NoScrapeYet, "The collector has not completed a scrape yet."
	case now.Sub(last) > scrapeStaleAfter*interval:
		return false, reason.ScrapeStale,
			fmt.Sprintf("The last scrape was %s ago; the interval is %s.", now.Sub(last).Round(time.Second), interval)
	}
	return true, reason.ScrapeFresh, "The collector is scraping pg_stat_statements on schedule."
}

// healthConditions computes the health conditions of a running watch. It reads
// the runtime and the object, and writes nothing.
func healthConditions(watch *radarv1alpha1.PostgresWatch, rt *WatchRuntime, now time.Time) []metav1.Condition {
	var out []metav1.Condition
	add := func(typ string, ok bool, code reason.Code, msg string) {
		status := metav1.ConditionTrue
		if !ok {
			status = metav1.ConditionFalse
			msg += " See " + reason.DocsURL(code)
		}
		out = append(out, metav1.Condition{
			Type: typ, Status: status, Reason: string(code), Message: msg,
			ObservedGeneration: watch.Generation,
		})
	}

	if rt.Collector != nil {
		interval := time.Duration(watch.Spec.ScrapeIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 60 * time.Second
		}
		ok, code, msg := scrapeHealth(rt.Collector.LastScrapeTime(), interval, now)
		add(ConditionCollecting, ok, code, msg)
	}

	if rt.Arms != nil {
		h := armsHealth(rt.Arms, now)
		switch {
		case h.Usable:
			add(ConditionArmsLog, true, reason.ArmsUsable,
				"The Postgres log is flowing and its settings allow arm-labelled evidence (grade A).")
		case !rt.Arms.usable.Load():
			add(ConditionArmsLog, false, reason.ArmsSettingsRejected,
				"The Postgres server's logging settings do not allow grade A evidence, or could not be read; the manager log says which setting and how to fix it. Without grade A the engine cannot abort.")
		case h.Newest == nil:
			add(ConditionArmsLog, false, reason.ArmsNoLogYet,
				"No Postgres log line has arrived yet. Without grade A the engine cannot abort.")
		default:
			add(ConditionArmsLog, false, reason.ArmsLogStale,
				"No Postgres log line for "+now.Sub(*h.Newest).Round(time.Second).String()+"; the log is not flowing. Without grade A the engine cannot abort.")
		}
	}

	if rt.Shadow != nil {
		switch {
		case rt.Shadow.BreakerOpen():
			add(ConditionActuating, false, reason.BreakerOpen,
				"Earlier aborts did not make the regression go away, so the engine only alerts. "+
					"Once you have looked at why, close it with: kubectl annotate postgreswatch "+watch.Name+" -n "+watch.Namespace+
					" "+BreakerResetAnnotation+`="$(date +%s)" --overwrite.`)
		case !rt.Shadow.Policy.Enforcing():
			add(ConditionActuating, false, reason.ShadowMode,
				"Shadow mode: the engine judges and logs, and nothing is paused or aborted.")
		case watch.Spec.Attribution == nil || watch.Spec.Attribution.Actuate == nil || !watch.Spec.Attribution.Actuate.Enabled:
			add(ConditionActuating, false, reason.ActuationNotAsked,
				"Enforce mode without attribution.actuate.enabled: the engine serves verdicts and the rollout controller decides; this manager does not pause or abort.")
		case rt.Actuate == nil:
			add(ConditionActuating, false, reason.NoActuator,
				"attribution.actuate is on but this manager cannot patch Rollouts.")
		case watch.Annotations[ActuationPausedAnnotation] == "true":
			add(ConditionActuating, false, reason.ActuationPaused,
				"The "+ActuationPausedAnnotation+" annotation is set: nothing is paused or aborted until it is removed.")
		default:
			msg := "Enforce mode: the engine may pause and abort Rollouts."
			if len(rt.Actuate.Namespaces) > 0 {
				msg = fmt.Sprintf("Enforce mode: the engine may pause and abort Rollouts in %v.", rt.Actuate.Namespaces)
			}
			add(ConditionActuating, true, reason.ActuationReady, msg)
		}
	}
	return out
}

// applyHealthConditions writes the computed conditions into the object's list
// and removes the health conditions that no longer apply (the arms were turned
// off, say), so a stale one never outlives the setting it described.
func applyHealthConditions(conds *[]metav1.Condition, computed []metav1.Condition) {
	have := map[string]bool{}
	for _, c := range computed {
		have[c.Type] = true
		setCondition(conds, c)
	}
	for _, typ := range healthConditionTypes {
		if have[typ] {
			continue
		}
		kept := (*conds)[:0]
		for _, c := range *conds {
			if c.Type != typ {
				kept = append(kept, c)
			}
		}
		*conds = kept
	}
}
