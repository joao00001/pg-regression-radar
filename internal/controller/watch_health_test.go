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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/pglog"
	"github.com/joao00001/pg-regression-radar/internal/reason"
)

func healthWatch(mode string, actuate bool) *radarv1alpha1.PostgresWatch {
	w := &radarv1alpha1.PostgresWatch{}
	w.Generation = 4
	w.Spec.Attribution = &radarv1alpha1.AttributionConfig{Mode: mode}
	if actuate {
		w.Spec.Attribution.Actuate = &radarv1alpha1.ActuateConfig{Enabled: true}
	}
	return w
}

func healthRuntime(enforce bool, actuate *ActuateRuntime) *WatchRuntime {
	ev := shadow.NewWithPolicy(replayOf(nil), collector.FingerprintQuery, attribution.Config{}, attribution.PolicyConfig{Enforce: enforce, BreakerLimit: 1})
	return &WatchRuntime{Shadow: ev, Actuate: actuate}
}

func condition(t *testing.T, conds []metav1.Condition, typ string) metav1.Condition {
	t.Helper()
	for _, c := range conds {
		if c.Type == typ {
			return c
		}
	}
	t.Fatalf("no %s condition in %+v", typ, conds)
	return metav1.Condition{}
}

func hasCondition(conds []metav1.Condition, typ string) bool {
	for _, c := range conds {
		if c.Type == typ {
			return true
		}
	}
	return false
}

func TestActuatingCondition(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	act := &ActuateRuntime{}
	cases := []struct {
		name   string
		watch  func() *radarv1alpha1.PostgresWatch
		rt     func() *WatchRuntime
		status metav1.ConditionStatus
		code   reason.Code
	}{
		{"shadow mode", func() *radarv1alpha1.PostgresWatch { return healthWatch("shadow", false) },
			func() *WatchRuntime { return healthRuntime(false, nil) }, metav1.ConditionFalse, reason.ShadowMode},
		{"shadow mode ignores actuate", func() *radarv1alpha1.PostgresWatch { return healthWatch("shadow", true) },
			func() *WatchRuntime { return healthRuntime(false, nil) }, metav1.ConditionFalse, reason.ShadowMode},
		{"enforce without actuate", func() *radarv1alpha1.PostgresWatch { return healthWatch("enforce", false) },
			func() *WatchRuntime { return healthRuntime(true, nil) }, metav1.ConditionFalse, reason.ActuationNotAsked},
		{"actuate asked, no actuator", func() *radarv1alpha1.PostgresWatch { return healthWatch("enforce", true) },
			func() *WatchRuntime { return healthRuntime(true, nil) }, metav1.ConditionFalse, reason.NoActuator},
		{"paused by annotation", func() *radarv1alpha1.PostgresWatch {
			w := healthWatch("enforce", true)
			w.Annotations = map[string]string{ActuationPausedAnnotation: "true"}
			return w
		}, func() *WatchRuntime { return healthRuntime(true, act) }, metav1.ConditionFalse, reason.ActuationPaused},
		{"annotation with another value does not pause", func() *radarv1alpha1.PostgresWatch {
			w := healthWatch("enforce", true)
			w.Annotations = map[string]string{ActuationPausedAnnotation: "false"}
			return w
		}, func() *WatchRuntime { return healthRuntime(true, act) }, metav1.ConditionTrue, reason.ActuationReady},
		{"ready", func() *radarv1alpha1.PostgresWatch { return healthWatch("enforce", true) },
			func() *WatchRuntime { return healthRuntime(true, act) }, metav1.ConditionTrue, reason.ActuationReady},
		{"breaker open wins over everything", func() *radarv1alpha1.PostgresWatch { return healthWatch("enforce", true) },
			func() *WatchRuntime {
				rt := healthRuntime(true, act)
				rt.Shadow.Policy.Verify(attribution.Assessment{
					Health: attribution.HealthRegressed, Impact: attribution.Impact{A: 0.5, AB: 0.5, ABC: 0.5}})
				return rt
			}, metav1.ConditionFalse, reason.BreakerOpen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := condition(t, healthConditions(c.watch(), c.rt(), now), ConditionActuating)
			if got.Status != c.status || got.Reason != string(c.code) {
				t.Fatalf("got %s/%s, want %s/%s (%s)", got.Status, got.Reason, c.status, c.code, got.Message)
			}
			if got.ObservedGeneration != 4 {
				t.Errorf("observedGeneration = %d, want the watch's generation", got.ObservedGeneration)
			}
			if c.status == metav1.ConditionFalse && !strings.Contains(got.Message, reason.DocsURL(c.code)) {
				t.Errorf("an unhealthy condition must link to its docs entry: %q", got.Message)
			}
			if c.status == metav1.ConditionTrue && strings.Contains(got.Message, "http") {
				t.Errorf("a healthy condition needs no link: %q", got.Message)
			}
		})
	}
}

func TestActuatingConditionNamesTheNamespaces(t *testing.T) {
	rt := healthRuntime(true, &ActuateRuntime{Namespaces: []string{"shop"}})
	got := condition(t, healthConditions(healthWatch("enforce", true), rt, time.Now()), ConditionActuating)
	if !strings.Contains(got.Message, "shop") {
		t.Fatalf("message should say where it may act: %q", got.Message)
	}
}

func TestNoAttributationNoActuatingCondition(t *testing.T) {
	got := healthConditions(&radarv1alpha1.PostgresWatch{}, &WatchRuntime{}, time.Now())
	for _, typ := range healthConditionTypes {
		if hasCondition(got, typ) {
			t.Errorf("%s must be absent when the engine, the collector and the arms are all off", typ)
		}
	}
}

func TestScrapeHealth(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	iv := time.Minute
	cases := []struct {
		name string
		last time.Time
		ok   bool
		code reason.Code
	}{
		{"never", time.Time{}, false, reason.NoScrapeYet},
		{"just now", now.Add(-5 * time.Second), true, reason.ScrapeFresh},
		{"one missed scrape is normal", now.Add(-2 * iv), true, reason.ScrapeFresh},
		{"exactly three intervals is still on schedule", now.Add(-3 * iv), true, reason.ScrapeFresh},
		{"more than three intervals", now.Add(-3*iv - time.Second), false, reason.ScrapeStale},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, code, msg := scrapeHealth(c.last, iv, now)
			if ok != c.ok || code != c.code {
				t.Fatalf("got %v/%s, want %v/%s (%s)", ok, code, c.ok, c.code, msg)
			}
		})
	}
}

func TestArmsLogCondition(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	w := healthWatch("shadow", false)

	rt := &WatchRuntime{Arms: newArms(fakeHashes{"a", "b", nil}, now)}
	if c := condition(t, healthConditions(w, rt, now), ConditionArmsLog); c.Status != metav1.ConditionTrue || c.Reason != string(reason.ArmsUsable) {
		t.Fatalf("flowing log: %+v", c)
	}

	// the settings check failed
	a := newArms(fakeHashes{"a", "b", nil}, now)
	a.usable.Store(false)
	c := condition(t, healthConditions(w, &WatchRuntime{Arms: a}, now), ConditionArmsLog)
	if c.Status != metav1.ConditionFalse || c.Reason != string(reason.ArmsSettingsRejected) {
		t.Fatalf("rejected settings: %+v", c)
	}
	if !strings.Contains(c.Message, "cannot abort") {
		t.Errorf("say what it costs: %q", c.Message)
	}

	// settings fine, but no line has ever arrived
	empty := &ArmsRuntime{Store: pglog.NewStore(time.Minute, time.Hour, 0), now: func() time.Time { return now }}
	empty.usable.Store(true)
	if c := condition(t, healthConditions(w, &WatchRuntime{Arms: empty}, now), ConditionArmsLog); c.Reason != string(reason.ArmsNoLogYet) {
		t.Fatalf("no line yet: %+v", c)
	}

	// lines arrived, then stopped
	later := now.Add(armsStale + time.Minute)
	stale := newArms(fakeHashes{"a", "b", nil}, now)
	stale.now = func() time.Time { return later }
	c = condition(t, healthConditions(w, &WatchRuntime{Arms: stale}, later), ConditionArmsLog)
	if c.Status != metav1.ConditionFalse || c.Reason != string(reason.ArmsLogStale) {
		t.Fatalf("stale log: %+v", c)
	}
}

func TestApplyHealthConditionsReplacesAndRemoves(t *testing.T) {
	now := metav1.Now()
	conds := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "WorkerStarted", LastTransitionTime: now},
		{Type: ConditionArmsLog, Status: metav1.ConditionTrue, Reason: string(reason.ArmsUsable), LastTransitionTime: now},
		{Type: ConditionActuating, Status: metav1.ConditionFalse, Reason: string(reason.ShadowMode), LastTransitionTime: now},
	}
	// the arms were switched off and the watch moved to enforce
	applyHealthConditions(&conds, []metav1.Condition{
		{Type: ConditionActuating, Status: metav1.ConditionTrue, Reason: string(reason.ActuationReady)},
	})
	if hasCondition(conds, ConditionArmsLog) {
		t.Error("ArmsLog must go when arms are off, or it would describe a setting that no longer exists")
	}
	if c := condition(t, conds, ConditionActuating); c.Status != metav1.ConditionTrue || c.Reason != string(reason.ActuationReady) {
		t.Errorf("Actuating not updated: %+v", c)
	}
	if !hasCondition(conds, "Ready") {
		t.Error("Ready is not ours to remove")
	}
	if len(conds) != 2 {
		t.Errorf("want Ready + Actuating, got %+v", conds)
	}
}

func TestApplyHealthConditionsKeepsTransitionTimeWhileStatusHolds(t *testing.T) {
	then := metav1.NewTime(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))
	conds := []metav1.Condition{{Type: ConditionCollecting, Status: metav1.ConditionTrue, Reason: string(reason.ScrapeFresh), LastTransitionTime: then}}
	applyHealthConditions(&conds, []metav1.Condition{{Type: ConditionCollecting, Status: metav1.ConditionTrue, Reason: string(reason.ScrapeFresh)}})
	if !conds[0].LastTransitionTime.Equal(&then) {
		t.Errorf("lastTransitionTime moved although the status did not change: %v", conds[0].LastTransitionTime)
	}
	applyHealthConditions(&conds, []metav1.Condition{{Type: ConditionCollecting, Status: metav1.ConditionFalse, Reason: string(reason.ScrapeStale)}})
	if conds[0].LastTransitionTime.Equal(&then) {
		t.Error("lastTransitionTime must move when the status flips")
	}
}

func openBreaker(rt *WatchRuntime) {
	rt.Shadow.Policy.Verify(attribution.Assessment{
		Health: attribution.HealthRegressed, Impact: attribution.Impact{A: 0.5, AB: 0.5, ABC: 0.5}})
}

func annotated(token string) *radarv1alpha1.PostgresWatch {
	w := healthWatch("enforce", true)
	w.Namespace, w.Name = "shop", "orders"
	if token != "" {
		w.Annotations = map[string]string{BreakerResetAnnotation: token}
	}
	return w
}

func TestResetBreakerAnnotation(t *testing.T) {
	cap := &logCapture{}
	r := &PostgresWatchReconciler{Logger: cap.logger()}
	rt := healthRuntime(true, &ActuateRuntime{})
	openBreaker(rt)
	if !rt.Shadow.BreakerOpen() {
		t.Fatal("the test needs an open breaker")
	}

	r.maybeResetBreaker(annotated(""), rt)
	if !rt.Shadow.BreakerOpen() {
		t.Fatal("no annotation: the breaker must stay open")
	}

	r.maybeResetBreaker(annotated("100"), rt)
	if rt.Shadow.BreakerOpen() {
		t.Fatal("a new annotation value must close the breaker")
	}
	m := cap.mustFind(t, reason.BreakerReset)
	if m["level"] != "WARN" || m["watch"] != "shop/orders" || m["token"] != "100" {
		t.Fatalf("the reset must be logged where an operator will see it: %v", m)
	}

	// the same value again must not close it a second time: the breaker opens
	// again if the next aborts do not help either, and a stale annotation must
	// not keep undoing that
	openBreaker(rt)
	r.maybeResetBreaker(annotated("100"), rt)
	if !rt.Shadow.BreakerOpen() {
		t.Fatal("an annotation that has not changed must not reset the breaker again")
	}

	// removing it and putting the same value back is a new request
	r.maybeResetBreaker(annotated(""), rt)
	r.maybeResetBreaker(annotated("100"), rt)
	if rt.Shadow.BreakerOpen() {
		t.Fatal("removing the annotation and setting it again must reset")
	}
}

func TestResetBreakerLeftoverAnnotationDoesNothingAtStartup(t *testing.T) {
	r := &PostgresWatchReconciler{Logger: (&logCapture{}).logger()}
	rt := healthRuntime(true, &ActuateRuntime{})
	rt.breakerResetSeen = "old" // what startWatch records from the object
	openBreaker(rt)
	r.maybeResetBreaker(annotated("old"), rt)
	if !rt.Shadow.BreakerOpen() {
		t.Fatal("an annotation that predates the runtime must not reset a breaker that opened after it")
	}
}

func TestResetBreakerWhileClosedIsHarmless(t *testing.T) {
	cap := &logCapture{}
	r := &PostgresWatchReconciler{Logger: cap.logger()}
	rt := healthRuntime(true, &ActuateRuntime{})
	r.maybeResetBreaker(annotated("1"), rt)
	if _, ok := cap.find(reason.BreakerReset); ok {
		t.Fatal("nothing was reset, so nothing should be reported as reset")
	}
	if len(cap.lines()) != 1 {
		t.Fatalf("want one info line saying it was already closed, got %v", cap.lines())
	}
	// and a runtime with no engine at all must not panic
	r.maybeResetBreaker(annotated("2"), &WatchRuntime{})
}

func TestBreakerOpenConditionSaysHowToCloseIt(t *testing.T) {
	rt := healthRuntime(true, &ActuateRuntime{})
	openBreaker(rt)
	got := condition(t, healthConditions(annotated(""), rt, time.Now()), ConditionActuating)
	for _, want := range []string{"kubectl annotate postgreswatch orders -n shop", BreakerResetAnnotation, reason.DocsURL(reason.BreakerOpen)} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("message lacks %q: %s", want, got.Message)
		}
	}
}
