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
		"severity": "info",
		"message":  "Reconciliation finished",
		"metadata": map[string]string{"revision": "main@sha1:abc123"},
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
