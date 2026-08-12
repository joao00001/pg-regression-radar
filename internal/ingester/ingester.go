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

// ValidSourceTypes lists every source type Handler.ServeHTTP recognises
// explicitly. ServeHTTP's switch already treats any unrecognised value as
// "generic" via its default case (a deliberate lenient-fallback design, so
// a webhook never gets rejected outright), which has the side effect of
// silently masking a typo'd --source-type: "arogcd" just quietly parses as
// generic instead of erroring anywhere. ValidSourceTypes lets a caller that
// wants to catch that check explicitly instead -- cmd/operator's and
// cmd/ingester's --dry-run both do.
var ValidSourceTypes = map[string]bool{
	"argocd":        true,
	"argo-rollouts": true,
	"flux":          true,
	"generic":       true,
}

// Store keeps an in-memory ordered list of deploy events.
type Store struct {
	mu     sync.RWMutex
	events []v1alpha1.DeployEvent
}

// NewStore returns an empty Store with a small pre-allocated backing slice to
// reduce early-growth reallocations.
func NewStore() *Store {
	return &Store{events: make([]v1alpha1.DeployEvent, 0, 64)}
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

	// Not every webhook payload carries destination-cluster identity (Argo
	// Rollouts and vanilla Flux events don't). Fall back to the cluster
	// identity the operator configured for this ingester instance so
	// multi-cluster correlation never sees an empty Cluster.
	if ev.Cluster == "" {
		ev.Cluster = h.source.ClusterName
	}

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
// on the on-sync-succeeded trigger. The notification template is configurable
// (see README "Supported Webhook Sources"), so this mirrors the fields of the
// Application object (`.app` in the template) that operators are asked to
// forward, including the sync destination which identifies the target
// cluster (see https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/templates/).
type argoCDPayload struct {
	App struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Destination struct {
				// Name is the human-friendly registered cluster name
				// (`app.spec.destination.name` in ArgoCD's cluster list).
				Name string `json:"name"`
				// Server is the destination cluster's API server URL, used
				// when the destination is addressed by server instead of
				// by registered name.
				Server string `json:"server"`
			} `json:"destination"`
		} `json:"spec"`
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

	// Prefer the registered cluster name over the raw API server URL — it's
	// what operators actually recognise, and ArgoCD always populates at
	// least one of the two for a resolved destination.
	cluster := p.App.Spec.Destination.Name
	if cluster == "" {
		cluster = p.App.Spec.Destination.Server
	}

	return v1alpha1.DeployEvent{
		ID:        fmt.Sprintf("argocd-%s-%d", p.App.Metadata.Name, time.Now().UnixNano()),
		App:       p.App.Metadata.Name,
		Cluster:   cluster,
		Namespace: p.App.Metadata.Namespace,
		Revision:  p.App.Status.Sync.Revision,
		ImageTag:  imageTag,
		Timestamp: time.Now().UTC(),
	}, nil
}

// argoRolloutsPayload matches the shape of an Argo Rollouts notification
// webhook. Unlike ArgoCD, a Rollout is not a cross-cluster deploy pointer —
// the Rollout object lives in the very cluster its controller runs in, so
// there is no `destination` field on it to read a target cluster from (see
// https://argo-rollouts.readthedocs.io/en/latest/features/notifications/,
// where templates only expose `.rollout` and `.recipient`). ImageTag is
// already a precedent in this codebase for a value that doesn't exist on the
// stock Rollout object and must be injected by the operator's webhook
// template (e.g. from a label/annotation); Cluster follows the same pattern
// for the (uncommon) case where a single ingester fronts Rollouts from
// several clusters and the operator wants to disambiguate them explicitly
// rather than rely on the per-instance --cluster-name fallback.
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
	// Cluster is not part of the stock Rollout notification payload; it must
	// be added explicitly to the webhook body template if the operator wants
	// per-event cluster attribution instead of the DeploySource fallback.
	Cluster string `json:"cluster"`
}

func (h *Handler) parseArgoRolloutsPayload(r *http.Request) (v1alpha1.DeployEvent, error) {
	var p argoRolloutsPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return v1alpha1.DeployEvent{}, fmt.Errorf("argo-rollouts: decode: %w", err)
	}

	return v1alpha1.DeployEvent{
		ID:        fmt.Sprintf("argo-rollouts-%s-%d", p.Rollout.Metadata.Name, time.Now().UnixNano()),
		App:       p.Rollout.Metadata.Name,
		Cluster:   p.Cluster,
		Namespace: p.Rollout.Metadata.Namespace,
		Revision:  p.Rollout.Status.CurrentPodHash,
		ImageTag:  p.ImageTag,
		Timestamp: time.Now().UTC(),
	}, nil
}

// fluxPayload matches the Flux notification-controller Event type
// (github.com/fluxcd/pkg/apis/event/v1beta1), which is what the "generic"
// Provider POSTs verbatim to a webhook. Unlike ArgoCD's notification body,
// this shape is fixed by Flux, not a user-defined Go template, so there is
// no `destination`/`cluster` field on the event itself — Flux controllers
// are installed per-cluster and have no built-in concept of a remote target
// cluster (see https://fluxcd.io/flux/components/notification/events/).
// Cluster identity can still ride along in `metadata`: the notification
// Alert's `.spec.eventMetadata` is merged into every event it forwards (see
// https://fluxcd.io/flux/components/notification/alerts/#event-metadata),
// so operators are expected to set `eventMetadata.cluster: <cluster-name>`
// on their Alert to get per-event attribution; otherwise the DeploySource
// fallback is used.
type fluxPayload struct {
	InvolvedObject struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
	} `json:"involvedObject"`
	Message   string            `json:"message"`
	Severity  string            `json:"severity"`
	Metadata  map[string]string `json:"metadata"`
	Timestamp time.Time         `json:"timestamp"`
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
	// Populated only if the Alert sets `eventMetadata.cluster`; empty
	// otherwise, in which case ServeHTTP falls back to the configured
	// DeploySource.ClusterName.
	cluster := p.Metadata["cluster"]

	return v1alpha1.DeployEvent{
		ID:        fmt.Sprintf("flux-%s-%d", p.InvolvedObject.Name, time.Now().UnixNano()),
		App:       p.InvolvedObject.Name,
		Cluster:   cluster,
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
