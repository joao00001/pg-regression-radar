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
