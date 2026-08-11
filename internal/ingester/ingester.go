// Package ingester normalises deploy-event webhooks from ArgoCD, Argo Rollouts,
// and Flux into a unified DeployEvent so the correlation engine has a single
// source of truth regardless of GitOps tool.
package ingester

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// Store keeps an in-memory ordered list of deploy events.
type Store struct {
	mu     sync.RWMutex
	events []v1alpha1.DeployEvent
}

// Add appends a new event to the store.
func (s *Store) Add(ev v1alpha1.DeployEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

// EventsInRange returns all events whose Timestamp falls within [from, to].
func (s *Store) EventsInRange(from, to time.Time) []v1alpha1.DeployEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []v1alpha1.DeployEvent
	for _, ev := range s.events {
		if !ev.Timestamp.Before(from) && !ev.Timestamp.After(to) {
			result = append(result, ev)
		}
	}
	return result
}

// All returns a snapshot of all stored events.
func (s *Store) All() []v1alpha1.DeployEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]v1alpha1.DeployEvent, len(s.events))
	copy(out, s.events)
	return out
}

// Handler is an HTTP handler that accepts webhook payloads from ArgoCD,
// Argo Rollouts, and Flux and stores normalised DeployEvents.
type Handler struct {
	store  *Store
	source v1alpha1.DeploySource
	logger *slog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(store *Store, source v1alpha1.DeploySource, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, source: source, logger: logger}
}

// ServeHTTP handles an incoming webhook POST request. The payload shape differs
// per source type and is normalised here.
//
//	POST /webhook  (Content-Type: application/json)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var ev v1alpha1.DeployEvent
	var err error

	switch h.source.SourceType {
	case "argocd":
		ev, err = h.parseArgoCDPayload(r)
	case "argo-rollouts":
		ev, err = h.parseArgoRolloutsPayload(r)
	case "flux":
		ev, err = h.parseFluxPayload(r)
	default:
		ev, err = h.parseGenericPayload(r)
	}

	if err != nil {
		h.logger.Error("ingester: failed to parse webhook", "source_type", h.source.SourceType, "err", err)
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	ev.Source = h.source.Name

	// Skip events that don't belong to the configured app so unrelated deploys
	// never pollute the correlation window.
	if h.source.AppName != "" && ev.App != h.source.AppName {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.store.Add(ev)
	h.logger.Info("ingester: deploy event stored",
		"id", ev.ID,
		"app", ev.App,
		"revision", ev.Revision,
		"timestamp", ev.Timestamp)

	w.WriteHeader(http.StatusNoContent)
}

// ----- payload parsers -----

// argoCDPayload matches the shape of an ArgoCD Notifications webhook body sent
// on the on-sync-succeeded trigger.
type argoCDPayload struct {
	App struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Status struct {
			Sync struct {
				Revision string `json:"revision"`
			} `json:"sync"`
			Summary struct {
				Images []string `json:"images"`
			} `json:"summary"`
		} `json:"status"`
	} `json:"app"`
}

func (h *Handler) parseArgoCDPayload(r *http.Request) (v1alpha1.DeployEvent, error) {
	var p argoCDPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return v1alpha1.DeployEvent{}, fmt.Errorf("argocd: decode: %w", err)
	}

	imageTag := ""
	if len(p.App.Status.Summary.Images) > 0 {
		imageTag = p.App.Status.Summary.Images[0]
	}

	return v1alpha1.DeployEvent{
		ID:        fmt.Sprintf("argocd-%s-%d", p.App.Metadata.Name, time.Now().UnixNano()),
		App:       p.App.Metadata.Name,
		Namespace: p.App.Metadata.Namespace,
		Revision:  p.App.Status.Sync.Revision,
		ImageTag:  imageTag,
		Timestamp: time.Now().UTC(),
	}, nil
}

// argoRolloutsPayload matches the shape of an Argo Rollouts notification webhook.
type argoRolloutsPayload struct {
	Rollout struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Status struct {
			CurrentPodHash string `json:"currentPodHash"`
		} `json:"status"`
	} `json:"rollout"`
	ImageTag string `json:"imageTag"`
}

func (h *Handler) parseArgoRolloutsPayload(r *http.Request) (v1alpha1.DeployEvent, error) {
	var p argoRolloutsPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return v1alpha1.DeployEvent{}, fmt.Errorf("argo-rollouts: decode: %w", err)
	}

	return v1alpha1.DeployEvent{
		ID:        fmt.Sprintf("argo-rollouts-%s-%d", p.Rollout.Metadata.Name, time.Now().UnixNano()),
		App:       p.Rollout.Metadata.Name,
		Namespace: p.Rollout.Metadata.Namespace,
		Revision:  p.Rollout.Status.CurrentPodHash,
		ImageTag:  p.ImageTag,
		Timestamp: time.Now().UTC(),
	}, nil
}

// fluxPayload matches the Flux notification controller event shape.
type fluxPayload struct {
	InvolvedObject struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
	} `json:"involvedObject"`
	Message  string    `json:"message"`
	Severity string    `json:"severity"`
	Metadata map[string]string `json:"metadata"`
	Timestamp time.Time `json:"timestamp"`
}

func (h *Handler) parseFluxPayload(r *http.Request) (v1alpha1.DeployEvent, error) {
	var p fluxPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return v1alpha1.DeployEvent{}, fmt.Errorf("flux: decode: %w", err)
	}

	ts := p.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	revision := p.Metadata["revision"]

	return v1alpha1.DeployEvent{
		ID:        fmt.Sprintf("flux-%s-%d", p.InvolvedObject.Name, time.Now().UnixNano()),
		App:       p.InvolvedObject.Name,
		Namespace: p.InvolvedObject.Namespace,
		Revision:  revision,
		Timestamp: ts,
	}, nil
}

// genericPayload accepts a minimal JSON body matching the DeployEvent schema
// directly. Useful for testing or custom integrations.
func (h *Handler) parseGenericPayload(r *http.Request) (v1alpha1.DeployEvent, error) {
	var ev v1alpha1.DeployEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		return v1alpha1.DeployEvent{}, fmt.Errorf("generic: decode: %w", err)
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if ev.ID == "" {
		ev.ID = fmt.Sprintf("generic-%s-%d", ev.App, time.Now().UnixNano())
	}
	return ev, nil
}
