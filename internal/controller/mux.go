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
	"net/http"
	"sync"
)

// DynamicMux is an http.Handler whose routes can be registered and
// unregistered at runtime. The standard library's http.ServeMux has no
// supported way to remove a route once added, which DeploySourceReconciler
// needs: a DeploySource CR can be deleted (or its postgresWatchRef can
// change) at any time, and the corresponding webhook path must stop being
// served immediately rather than silently keep forwarding to a stale
// internal/ingester.Handler.
type DynamicMux struct {
	mu     sync.RWMutex
	routes map[string]http.Handler
}

// NewDynamicMux creates an empty DynamicMux.
func NewDynamicMux() *DynamicMux {
	return &DynamicMux{routes: make(map[string]http.Handler)}
}

// Register binds path to h, replacing any previous handler at that path.
func (m *DynamicMux) Register(path string, h http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[path] = h
}

// Unregister removes path, if present.
func (m *DynamicMux) Unregister(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.routes, path)
}

// ServeHTTP dispatches to the handler registered for r.URL.Path, or 404s.
func (m *DynamicMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	h, ok := m.routes[r.URL.Path]
	m.mu.RUnlock()

	if !ok {
		http.NotFound(w, r)
		return
	}
	h.ServeHTTP(w, r)
}

// Len reports how many routes are currently registered. Useful for tests.
func (m *DynamicMux) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.routes)
}
