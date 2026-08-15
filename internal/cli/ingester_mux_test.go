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

package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// TestEventsEndpoint_NoSecret verifies that /events returns 404 when no
// webhook secret is configured, preventing unauthenticated access.
func TestEventsEndpoint_NoSecret(t *testing.T) {
	t.Parallel()

	store := ingester.NewStore()
	source := v1alpha1.DeploySource{Name: "test", SourceType: "generic"}
	mux := newIngesterMux(store, source, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("/events with no secret: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestEventsEndpoint_WithSecret_NoToken verifies that /events returns 401 when
// a webhook secret is configured but no token is provided.
func TestEventsEndpoint_WithSecret_NoToken(t *testing.T) {
	t.Parallel()

	store := ingester.NewStore()
	source := v1alpha1.DeploySource{Name: "test", SourceType: "generic"}
	mux := newIngesterMux(store, source, "supersecret", nil)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/events with secret but no token: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestEventsEndpoint_WithSecret_WrongToken verifies that /events returns 401
// when the provided token does not match the configured secret.
func TestEventsEndpoint_WithSecret_WrongToken(t *testing.T) {
	t.Parallel()

	store := ingester.NewStore()
	source := v1alpha1.DeploySource{Name: "test", SourceType: "generic"}
	mux := newIngesterMux(store, source, "supersecret", nil)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("X-Webhook-Token", "wrongtoken")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/events with wrong token: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestEventsEndpoint_WithSecret_CorrectToken verifies that /events returns 200
// and JSON when the correct token is provided.
func TestEventsEndpoint_WithSecret_CorrectToken(t *testing.T) {
	t.Parallel()

	store := ingester.NewStore()
	source := v1alpha1.DeploySource{Name: "test", SourceType: "generic"}
	mux := newIngesterMux(store, source, "supersecret", nil)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("X-Webhook-Token", "supersecret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/events with correct token: got %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("/events Content-Type: got %q, want application/json", ct)
	}
}
