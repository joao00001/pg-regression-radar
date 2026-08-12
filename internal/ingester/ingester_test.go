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

package ingester_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func TestStore_AddAndRange(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}

	now := time.Now().UTC()
	store.Add(v1alpha1.DeployEvent{ID: "a", Timestamp: now.Add(-10 * time.Minute)})
	store.Add(v1alpha1.DeployEvent{ID: "b", Timestamp: now})
	store.Add(v1alpha1.DeployEvent{ID: "c", Timestamp: now.Add(10 * time.Minute)})

	all := store.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}

	inRange := store.EventsInRange(now.Add(-15*time.Minute), now.Add(5*time.Minute))
	if len(inRange) != 2 {
		t.Fatalf("expected 2 events in range, got %d", len(inRange))
	}
}

func TestHandler_ArgoCDWebhook(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "test", SourceType: "argocd"}
	h := ingester.NewHandler(store, src, nil)

	payload := map[string]interface{}{
		"app": map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "my-app",
				"namespace": "production",
			},
			"status": map[string]interface{}{
				"sync": map[string]interface{}{"revision": "deadbeef"},
				"summary": map[string]interface{}{
					"images": []string{"my-app:v42"},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	ev := events[0]
	if ev.App != "my-app" {
		t.Errorf("expected app=my-app, got %s", ev.App)
	}
	if ev.Revision != "deadbeef" {
		t.Errorf("expected revision=deadbeef, got %s", ev.Revision)
	}
	if ev.ImageTag != "my-app:v42" {
		t.Errorf("expected imageTag=my-app:v42, got %s", ev.ImageTag)
	}
}

func TestHandler_AppNameFilter(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{
		Name:       "filtered",
		SourceType: "generic",
		AppName:    "allowed-app",
	}
	h := ingester.NewHandler(store, src, nil)

	// This event should be filtered out (app name doesn't match).
	payload, _ := json.Marshal(v1alpha1.DeployEvent{App: "other-app", Timestamp: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if len(store.All()) != 0 {
		t.Error("expected event to be filtered out, but it was stored")
	}

	// This event should be accepted.
	payload, _ = json.Marshal(v1alpha1.DeployEvent{App: "allowed-app", Timestamp: time.Now()})
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if len(store.All()) != 1 {
		t.Errorf("expected 1 stored event, got %d", len(store.All()))
	}
}

func TestHandler_FluxWebhook(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "flux-src", SourceType: "flux"}
	h := ingester.NewHandler(store, src, nil)

	now := time.Now().UTC().Truncate(time.Second)
	payload := map[string]interface{}{
		"involvedObject": map[string]interface{}{
			"name":      "flux-app",
			"namespace": "flux-system",
			"kind":      "Kustomization",
		},
		"severity":  "info",
		"message":   "Reconciliation finished",
		"metadata":  map[string]string{"revision": "main@sha1:abc123"},
		"timestamp": now.Format(time.RFC3339),
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Revision != "main@sha1:abc123" {
		t.Errorf("expected revision=main@sha1:abc123, got %s", events[0].Revision)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	h := ingester.NewHandler(store, v1alpha1.DeploySource{SourceType: "generic"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// --- Cluster attribution tests ---
//
// DeployEvent.Cluster should be populated from the webhook payload when the
// source tool provides destination-cluster identity, and fall back to the
// DeploySource.ClusterName configured on the ingester otherwise.

func TestHandler_ArgoCDWebhook_ClusterFromDestinationName(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "test", SourceType: "argocd", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	payload := map[string]interface{}{
		"app": map[string]interface{}{
			"metadata": map[string]interface{}{"name": "my-app", "namespace": "production"},
			"spec": map[string]interface{}{
				"destination": map[string]interface{}{
					"name":   "prod-cluster",
					"server": "https://10.0.0.1:6443",
				},
			},
			"status": map[string]interface{}{
				"sync": map[string]interface{}{"revision": "deadbeef"},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	// destination.name takes precedence over destination.server, and over the
	// DeploySource fallback.
	if events[0].Cluster != "prod-cluster" {
		t.Errorf("expected cluster=prod-cluster, got %q", events[0].Cluster)
	}
}

func TestHandler_ArgoCDWebhook_ClusterFromDestinationServer(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "test", SourceType: "argocd", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	payload := map[string]interface{}{
		"app": map[string]interface{}{
			"metadata": map[string]interface{}{"name": "my-app", "namespace": "production"},
			"spec": map[string]interface{}{
				"destination": map[string]interface{}{
					"server": "https://10.0.0.1:6443",
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	// No destination.name: falls back to destination.server, still preferred
	// over the DeploySource fallback since the payload did carry identity.
	if events[0].Cluster != "https://10.0.0.1:6443" {
		t.Errorf("expected cluster=https://10.0.0.1:6443, got %q", events[0].Cluster)
	}
}

func TestHandler_ArgoCDWebhook_ClusterFallbackWhenDestinationMissing(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "test", SourceType: "argocd", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	// No spec.destination at all — operator hasn't updated their notification
	// template yet, or it's genuinely unresolved.
	payload := map[string]interface{}{
		"app": map[string]interface{}{
			"metadata": map[string]interface{}{"name": "my-app", "namespace": "production"},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Cluster != "fallback-cluster" {
		t.Errorf("expected cluster=fallback-cluster, got %q", events[0].Cluster)
	}
}

func TestHandler_ArgoRolloutsWebhook_ClusterFromPayload(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "rollouts-src", SourceType: "argo-rollouts", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	payload := map[string]interface{}{
		"rollout": map[string]interface{}{
			"metadata": map[string]interface{}{"name": "canary-app", "namespace": "staging"},
			"status":   map[string]interface{}{"currentPodHash": "abc123"},
		},
		"imageTag": "canary-app:v7",
		"cluster":  "staging-cluster",
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Cluster != "staging-cluster" {
		t.Errorf("expected cluster=staging-cluster, got %q", events[0].Cluster)
	}
}

func TestHandler_ArgoRolloutsWebhook_ClusterFallbackWhenAbsent(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "rollouts-src", SourceType: "argo-rollouts", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	// Stock Argo Rollouts webhook templates don't carry a cluster field
	// unless the operator adds one explicitly (see README).
	payload := map[string]interface{}{
		"rollout": map[string]interface{}{
			"metadata": map[string]interface{}{"name": "canary-app", "namespace": "staging"},
			"status":   map[string]interface{}{"currentPodHash": "abc123"},
		},
		"imageTag": "canary-app:v7",
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Cluster != "fallback-cluster" {
		t.Errorf("expected cluster=fallback-cluster, got %q", events[0].Cluster)
	}
}

func TestHandler_FluxWebhook_ClusterFromEventMetadata(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "flux-src", SourceType: "flux", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	// Mirrors what a Flux Alert with `spec.eventMetadata.cluster` set
	// produces on the wire (metadata is merged into the Event by the
	// notification-controller before it's POSTed to the webhook).
	payload := map[string]interface{}{
		"involvedObject": map[string]interface{}{
			"name":      "flux-app",
			"namespace": "flux-system",
			"kind":      "Kustomization",
		},
		"severity": "info",
		"message":  "Reconciliation finished",
		"metadata": map[string]string{"revision": "main@sha1:abc123", "cluster": "flux-cluster-1"},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Cluster != "flux-cluster-1" {
		t.Errorf("expected cluster=flux-cluster-1, got %q", events[0].Cluster)
	}
}

func TestHandler_FluxWebhook_ClusterFallbackWhenMetadataMissing(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "flux-src", SourceType: "flux", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	// No `cluster` key in eventMetadata — the common case since Flux doesn't
	// set this by default; the Alert must be configured for it explicitly.
	payload := map[string]interface{}{
		"involvedObject": map[string]interface{}{
			"name":      "flux-app",
			"namespace": "flux-system",
			"kind":      "Kustomization",
		},
		"severity": "info",
		"message":  "Reconciliation finished",
		"metadata": map[string]string{"revision": "main@sha1:abc123"},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Cluster != "fallback-cluster" {
		t.Errorf("expected cluster=fallback-cluster, got %q", events[0].Cluster)
	}
}

func TestHandler_GenericWebhook_ClusterFallbackWhenAbsent(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "generic-src", SourceType: "generic", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	payload, _ := json.Marshal(v1alpha1.DeployEvent{App: "some-app", Timestamp: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Cluster != "fallback-cluster" {
		t.Errorf("expected cluster=fallback-cluster, got %q", events[0].Cluster)
	}
}

func TestHandler_GenericWebhook_ClusterFromPayloadNotOverridden(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{Name: "generic-src", SourceType: "generic", ClusterName: "fallback-cluster"}
	h := ingester.NewHandler(store, src, nil)

	// A custom CI integration posting the DeployEvent schema directly can
	// set Cluster explicitly; the fallback must not clobber it.
	payload, _ := json.Marshal(v1alpha1.DeployEvent{App: "some-app", Cluster: "explicit-cluster", Timestamp: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Cluster != "explicit-cluster" {
		t.Errorf("expected cluster=explicit-cluster, got %q", events[0].Cluster)
	}
}

func TestStore_DrainSince(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	now := time.Now().UTC()

	// Empty store: cursor stays at zero, no events returned.
	evs, cursor := store.DrainSince(0)
	if len(evs) != 0 || cursor != 0 {
		t.Fatalf("expected empty drain on empty store, got %d events cursor=%d", len(evs), cursor)
	}

	store.Add(v1alpha1.DeployEvent{ID: "a", Timestamp: now})
	store.Add(v1alpha1.DeployEvent{ID: "b", Timestamp: now.Add(time.Second)})

	// First drain should return both events and advance cursor to 2.
	evs, cursor = store.DrainSince(0)
	if len(evs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evs))
	}
	if cursor != 2 {
		t.Fatalf("expected cursor=2, got %d", cursor)
	}

	// Second drain with updated cursor should return nothing new.
	evs, cursor = store.DrainSince(cursor)
	if len(evs) != 0 {
		t.Fatalf("expected 0 new events, got %d", len(evs))
	}

	// Add one more and drain only the new event.
	store.Add(v1alpha1.DeployEvent{ID: "c", Timestamp: now.Add(2 * time.Second)})
	evs, cursor = store.DrainSince(cursor)
	if len(evs) != 1 {
		t.Fatalf("expected 1 new event, got %d", len(evs))
	}
	if evs[0].ID != "c" {
		t.Errorf("expected event ID=c, got %s", evs[0].ID)
	}
	if cursor != 3 {
		t.Fatalf("expected cursor=3, got %d", cursor)
	}
}

// ----- Backfill -----

func TestStore_Backfill_SeedsHistoryAndDedupsByID(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	now := time.Now().UTC()

	n := store.Backfill([]v1alpha1.DeployEvent{
		{ID: "a", Timestamp: now.Add(-10 * time.Minute)},
		{ID: "b", Timestamp: now},
	})
	if n != 2 {
		t.Fatalf("expected Backfill to report 2 events, got %d", n)
	}

	// Re-backfilling an event with an ID already present must not duplicate
	// it (matches EventStore.Add's upsert-by-ID semantics) — first write wins.
	n = store.Backfill([]v1alpha1.DeployEvent{
		{ID: "a", Timestamp: now.Add(-10 * time.Minute), App: "should-be-ignored"},
		{ID: "c", Timestamp: now.Add(10 * time.Minute)},
	})
	if n != 3 {
		t.Fatalf("expected 3 total events after a partially-overlapping backfill, got %d", n)
	}

	all := store.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 stored events, got %d", len(all))
	}
	for _, ev := range all {
		if ev.ID == "a" && ev.App == "should-be-ignored" {
			t.Fatalf("expected the original event 'a' to be left in place, got its App overwritten")
		}
	}

	// EventsInRange must reflect the backfilled data too (byTime kept in sync).
	inRange := store.EventsInRange(now.Add(-15*time.Minute), now.Add(15*time.Minute))
	if len(inRange) != 3 {
		t.Fatalf("expected 3 events in range after backfill, got %d", len(inRange))
	}
}

// TestStore_Backfill_CursorPreventsReplay proves the core correctness
// requirement of Backfill: DrainSince(returnedCursor) on a store that was
// JUST backfilled (i.e. no new events have arrived since) must return an
// EMPTY batch. If it didn't, the operator's poll loop would treat every
// backfilled deploy event as brand new on restart and could re-run
// correlation / re-send alerts for regressions already reported before the
// restart — see internal/cli.RunOperator's poll loop and Backfill's doc
// comment.
func TestStore_Backfill_CursorPreventsReplay(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	now := time.Now().UTC()

	cursor := store.Backfill([]v1alpha1.DeployEvent{
		{ID: "old-1", Timestamp: now.Add(-2 * time.Hour)},
		{ID: "old-2", Timestamp: now.Add(-time.Hour)},
	})

	evs, newCursor := store.DrainSince(cursor)
	if len(evs) != 0 {
		t.Fatalf("expected DrainSince(backfillCursor) to return no events (replay bug), got %d: %+v", len(evs), evs)
	}
	if newCursor != cursor {
		t.Fatalf("expected cursor to stay at %d with no new events, got %d", cursor, newCursor)
	}

	// A genuinely new event arriving after the backfill must still show up.
	store.Add(v1alpha1.DeployEvent{ID: "new-1", Timestamp: now})
	evs, _ = store.DrainSince(cursor)
	if len(evs) != 1 || evs[0].ID != "new-1" {
		t.Fatalf("expected exactly the new event to drain after backfill, got %+v", evs)
	}
}

func TestStore_Backfill_EmptyReturnsCurrentLength(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	store.Add(v1alpha1.DeployEvent{ID: "a", Timestamp: time.Now()})

	n := store.Backfill(nil)
	if n != 1 {
		t.Fatalf("expected Backfill(nil) to report the current event count (1), got %d", n)
	}
}

func TestStore_EventsInRange_OutOfOrder(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	now := time.Now().UTC()

	// Add events out of timestamp order to verify the sorted index is correct.
	store.Add(v1alpha1.DeployEvent{ID: "late", Timestamp: now.Add(10 * time.Minute)})
	store.Add(v1alpha1.DeployEvent{ID: "early", Timestamp: now.Add(-10 * time.Minute)})
	store.Add(v1alpha1.DeployEvent{ID: "mid", Timestamp: now})

	inRange := store.EventsInRange(now.Add(-5*time.Minute), now.Add(5*time.Minute))
	if len(inRange) != 1 {
		t.Fatalf("expected 1 event in range, got %d", len(inRange))
	}
	if inRange[0].ID != "mid" {
		t.Errorf("expected event ID=mid, got %s", inRange[0].ID)
	}
}

// ----- Webhook authentication -----

func TestHandler_WebhookSecret_RejectsRequestWithoutToken(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{
		Name:          "secure-src",
		SourceType:    "generic",
		WebhookSecret: "supersecret",
	}
	h := ingester.NewHandler(store, src, nil)

	payload, _ := json.Marshal(v1alpha1.DeployEvent{App: "my-app", Timestamp: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	// No X-Webhook-Token header set.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing token, got %d", rr.Code)
	}
	if len(store.All()) != 0 {
		t.Error("expected no event to be stored when token is missing")
	}
}

func TestHandler_WebhookSecret_RejectsRequestWithWrongToken(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{
		Name:          "secure-src",
		SourceType:    "generic",
		WebhookSecret: "supersecret",
	}
	h := ingester.NewHandler(store, src, nil)

	payload, _ := json.Marshal(v1alpha1.DeployEvent{App: "my-app", Timestamp: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Webhook-Token", "wrongtoken")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong token, got %d", rr.Code)
	}
	if len(store.All()) != 0 {
		t.Error("expected no event to be stored when token is wrong")
	}
}

func TestHandler_WebhookSecret_AcceptsRequestWithCorrectToken(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	src := v1alpha1.DeploySource{
		Name:          "secure-src",
		SourceType:    "generic",
		WebhookSecret: "supersecret",
	}
	h := ingester.NewHandler(store, src, nil)

	payload, _ := json.Marshal(v1alpha1.DeployEvent{App: "my-app", Timestamp: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Webhook-Token", "supersecret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 for correct token, got %d", rr.Code)
	}
	if len(store.All()) != 1 {
		t.Errorf("expected 1 stored event, got %d", len(store.All()))
	}
}

func TestHandler_WebhookSecret_EmptySecretAllowsAll(t *testing.T) {
	t.Parallel()

	store := &ingester.Store{}
	// WebhookSecret intentionally left empty — no auth enforced.
	src := v1alpha1.DeploySource{Name: "open-src", SourceType: "generic"}
	h := ingester.NewHandler(store, src, nil)

	payload, _ := json.Marshal(v1alpha1.DeployEvent{App: "my-app", Timestamp: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	// No token header — should still succeed because no secret is configured.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 when no secret configured, got %d", rr.Code)
	}
	if len(store.All()) != 1 {
		t.Errorf("expected 1 stored event, got %d", len(store.All()))
	}
}
