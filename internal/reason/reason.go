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

// Package reason is the list of stable reason codes the radar puts on the log
// lines and status conditions that explain a decision or a fault.
//
// A log message is free text and may be reworded; a Code is an identifier that
// stays. It appears as the "reason" field of a log line and as the Reason of a
// status condition, and every code has an entry in the docs page
// troubleshooting/reasons.md with its causes, how to confirm each, and the
// fix. internal/reason's tests fail if a code is added here without that entry,
// if an entry has no code, or if a code is never used.
//
// Keep free text out of the "reason" field: a sentence that explains one
// occurrence goes in "detail" (or "why"), the code that names the situation goes
// in "reason".
package reason

import (
	"log/slog"
	"strings"
)

// Code names one situation. It is CamelCase, like a Kubernetes condition reason.
type Code string

// Component is the part of the radar a code belongs to; it is also the first
// word of the docs section the code is listed under.
type Component string

const (
	Attribution  Component = "attribution"
	Arms         Component = "arms"
	Watch        Component = "watch"
	DeploySource Component = "deploysource"
	History      Component = "history"
	Cluster      Component = "cluster"
)

// Severity is how loud a code normally is. It documents the usual level of the
// log line; the code itself says nothing about it.
type Severity string

const (
	// Info is a normal event, or a deliberate choice the radar made.
	Info Severity = "info"
	// Warn is something an operator may want to look at; the radar keeps working.
	Warn Severity = "warn"
	// Error is a fault: something the radar was asked to do did not happen.
	Error Severity = "error"
)

// Entry describes a code.
type Entry struct {
	Code      Code
	Component Component
	Severity  Severity
	// Summary is one sentence, the same words as the first line of the docs entry.
	Summary string
}

// The codes. Add one here, use it in a log line or a condition, and add its
// entry to docs/troubleshooting/reasons.md; the tests check all three.
const (
	// Attribution engine.
	DeployRegistered     Code = "DeployRegistered"
	NoSamples            Code = "NoSamples"
	ShadowMode           Code = "ShadowMode"
	BelowActingThreshold Code = "BelowActingThreshold"
	VerdictServed        Code = "VerdictServed"
	NamespaceNotAllowed  Code = "NamespaceNotAllowed"
	ActuationPaused      Code = "ActuationPaused"
	NotArgoRollout       Code = "NotArgoRollout"
	ActuationFailed      Code = "ActuationFailed"
	Acted                Code = "Acted"
	AbortRecovered       Code = "AbortRecovered"
	AbortPersisted       Code = "AbortPersisted"
	AbortUnverifiable    Code = "AbortUnverifiable"
	BreakerOpen          Code = "BreakerOpen"
	BreakerReset         Code = "BreakerReset"
	ActuationReady       Code = "ActuationReady"
	ActuationNotAsked    Code = "ActuationNotAsked"
	NoActuator           Code = "NoActuator"

	// Arms (the Postgres log behind grade A evidence).
	ArmsDisabled         Code = "ArmsDisabled"
	ArmsSettingsUnread   Code = "ArmsSettingsUnread"
	ArmsSettingsRejected Code = "ArmsSettingsRejected"
	ArmsUsable           Code = "ArmsUsable"
	ArmsNoLogYet         Code = "ArmsNoLogYet"
	ArmsLogStale         Code = "ArmsLogStale"
	ArmsUndistinguished  Code = "ArmsUndistinguished"

	// PostgresWatch.
	WorkerStarted           Code = "WorkerStarted"
	WorkerStartFailed       Code = "WorkerStartFailed"
	DSNUnresolved           Code = "DSNUnresolved"
	SecretsUnresolved       Code = "SecretsUnresolved"
	CollectorExited         Code = "CollectorExited"
	ScrapeFresh             Code = "ScrapeFresh"
	NoScrapeYet             Code = "NoScrapeYet"
	ScrapeStale             Code = "ScrapeStale"
	AlertNotifyFailed       Code = "AlertNotifyFailed"
	RegressionPersistFailed Code = "RegressionPersistFailed"
	RevertDisabled          Code = "RevertDisabled"
	AutoAbortIgnored        Code = "AutoAbortIgnored"
	AutoAbortSkipped        Code = "AutoAbortSkipped"
	AutoAbortFailed         Code = "AutoAbortFailed"
	AutoAbortTriggered      Code = "AutoAbortTriggered"
	MultipleTestingUnknown  Code = "MultipleTestingUnknown"

	// DeploySource and the native workload watch.
	RouteRegistered        Code = "RouteRegistered"
	NativeWatchActive      Code = "NativeWatchActive"
	InvalidSpec            Code = "InvalidSpec"
	PostgresWatchNotReady  Code = "PostgresWatchNotReady"
	DeployEventSynthesised Code = "DeployEventSynthesised"

	// Observe history.
	HistoryNotRecording Code = "HistoryNotRecording"

	// PostgresRadarCluster.
	ClusterHealthCheckFailed Code = "ClusterHealthCheckFailed"
)

var all = []Entry{
	{DeployRegistered, Attribution, Info, "A deploy was registered and will be judged between 10 and 45 minutes after it."},
	{NoSamples, Attribution, Warn, "A deploy could not be judged because there are no query samples to read."},
	{ShadowMode, Attribution, Info, "The decision was logged and not executed, because the watch is in shadow mode."},
	{BelowActingThreshold, Attribution, Info, "The watch is in enforce mode, but this decision is not strong enough to act on."},
	{VerdictServed, Attribution, Info, "An enforced decision was served to the rollout controller."},
	{NamespaceNotAllowed, Attribution, Info, "The radar decided to act but the Rollout's namespace is not in attribution.actuate.namespaces."},
	{ActuationPaused, Attribution, Warn, "The radar decided to act but the actuation-paused annotation is set on the PostgresWatch."},
	{NotArgoRollout, Attribution, Info, "The radar decided to act but the deploy did not come from an argo-rollouts DeploySource."},
	{ActuationFailed, Attribution, Error, "The call to pause or abort the Rollout was refused or failed."},
	{Acted, Attribution, Info, "The radar paused or aborted the Rollout."},
	{AbortRecovered, Attribution, Info, "After an abort, the regression went away."},
	{AbortPersisted, Attribution, Info, "After an abort, the regression was still there: the action did not help."},
	{AbortUnverifiable, Attribution, Info, "An abort could not be checked: too little data came after it."},
	{BreakerOpen, Attribution, Warn, "Earlier aborts did not help, so the engine only alerts until the manager restarts."},
	{BreakerReset, Attribution, Warn, "An operator closed the circuit breaker with the reset-breaker annotation, so the engine may act again."},
	{ActuationReady, Attribution, Info, "The watch is in enforce mode and may pause and abort Rollouts."},
	{ActuationNotAsked, Attribution, Info, "The watch does not ask for actuation, or is in shadow mode."},
	{NoActuator, Attribution, Info, "Actuation was asked for but this manager has no way to pause or abort a Rollout."},

	{ArmsDisabled, Arms, Error, "attribution.arms is configured but cannot work, so grade A evidence is off."},
	{ArmsSettingsUnread, Arms, Warn, "The radar could not read the Postgres server's logging settings."},
	{ArmsSettingsRejected, Arms, Warn, "The Postgres logging settings do not allow arm-labelled evidence."},
	{ArmsUsable, Arms, Info, "The Postgres log can serve as an arm-labelled source."},
	{ArmsNoLogYet, Arms, Info, "No Postgres log line has arrived yet."},
	{ArmsLogStale, Arms, Warn, "The Postgres log has stopped flowing."},
	{ArmsUndistinguished, Arms, Warn, "The pods of a deploy's canary and stable arms could not be told apart."},

	{WorkerStarted, Watch, Info, "The collector and the correlation engine are running."},
	{WorkerStartFailed, Watch, Error, "The watch's worker could not be started."},
	{DSNUnresolved, Watch, Error, "The watch's database connection string could not be resolved."},
	{SecretsUnresolved, Watch, Error, "A Secret the watch refers to could not be read."},
	{CollectorExited, Watch, Error, "The collector stopped on its own."},
	{ScrapeFresh, Watch, Info, "The collector scraped pg_stat_statements recently."},
	{NoScrapeYet, Watch, Info, "The collector has not completed a scrape yet."},
	{ScrapeStale, Watch, Warn, "The collector has not scraped for longer than it should."},
	{AlertNotifyFailed, Watch, Error, "An alert could not be delivered."},
	{RegressionPersistFailed, Watch, Error, "A detected regression could not be saved as a PerformanceRegression."},
	{RevertDisabled, Watch, Error, "attribution.revert is configured wrongly, so draft revert pull requests are off."},
	{AutoAbortIgnored, Watch, Info, "spec.autoAbort stands down because attribution.actuate decides."},
	{AutoAbortSkipped, Watch, Info, "The p-value auto-abort found no DeploySource for the deploy and did nothing."},
	{AutoAbortFailed, Watch, Error, "The p-value auto-abort tried to abort a Rollout and failed."},
	{AutoAbortTriggered, Watch, Info, "The p-value auto-abort aborted a Rollout."},
	{MultipleTestingUnknown, Watch, Warn, "spec.multipleTesting has a value the radar does not know and was ignored."},

	{RouteRegistered, DeploySource, Info, "The webhook route of the DeploySource is registered."},
	{NativeWatchActive, DeploySource, Info, "The DeploySource is watched through the Kubernetes API, with no webhook route."},
	{InvalidSpec, DeploySource, Warn, "The DeploySource's spec is invalid, so no route is registered."},
	{PostgresWatchNotReady, DeploySource, Info, "The PostgresWatch the DeploySource points to does not exist or is not running yet."},
	{DeployEventSynthesised, DeploySource, Info, "A deploy event was made from a native Rollout or Deployment change."},

	{HistoryNotRecording, History, Warn, "The dashboard's history could not be written; it will have a gap."},

	{ClusterHealthCheckFailed, Cluster, Warn, "The health check of a PostgresRadarCluster failed."},
}

// All returns every code, in the order the docs list them.
func All() []Entry { return append([]Entry(nil), all...) }

// Lookup returns the entry for c.
func Lookup(c Code) (Entry, bool) {
	for _, e := range all {
		if e.Code == c {
			return e, true
		}
	}
	return Entry{}, false
}

// docsBase is where the reason catalog is published. "latest" is the newest
// release; a link is meant for a person reading a log line, not for a script.
const docsBase = "https://joao00001.github.io/pg-regression-radar/latest/troubleshooting/reasons/"

// Anchor is the fragment of the docs page that explains c. The heading of each
// entry is the code itself, so the anchor is its lower-case form.
func Anchor(c Code) string { return strings.ToLower(string(c)) }

// DocsURL is the page that explains c.
func DocsURL(c Code) string { return docsBase + "#" + Anchor(c) }

// String returns the code as text.
func (c Code) String() string { return string(c) }

// LogValue makes a Code usable directly as a log attribute value:
// logger.Warn("...", "reason", reason.BreakerOpen).
func (c Code) LogValue() slog.Value { return slog.StringValue(string(c)) }
