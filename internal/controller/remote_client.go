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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// remoteClientScheme is the scheme used for clients built against remote
// ("spoke") clusters. Today the only thing PostgresWatchReconciler ever
// reads from a remote cluster is the Secret named by spec.dsnSecretRef (see
// resolveDSN in postgreswatch_controller.go), so registering anything beyond
// corev1 would be surface area with no caller.
var remoteClientScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		// corev1.AddToScheme only fails if given a scheme in an
		// inconsistent state, which cannot happen for a scheme we just
		// constructed; a panic here would only ever fire in a broken build.
		panic(fmt.Sprintf("controller: register corev1 in remote client scheme: %v", err))
	}
	return s
}()

// remoteClientCache builds and caches controller-runtime clients for remote
// ("spoke") Kubernetes clusters, keyed by a hash of the kubeconfig bytes
// that describe them. Rebuilding a client.Client (and the TLS transport
// underneath it) on every reconcile would be wasteful — and, at fleet
// scale, could add up to real overhead — so a client is reused across
// reconciles as long as the referenced Secret's content doesn't change. A
// changed kubeconfig (e.g. a credential rotation) naturally produces a
// different hash and gets its own client, with no explicit invalidation
// path required.
//
// Known gaps, deliberately left out of scope and tracked in
// docs/multi-cluster.md: this cache never evicts an entry (so a remote
// cluster that's decommissioned, or a kubeconfig that's rotated away from,
// leaves its old client's transport alive in memory for the life of the
// manager process), and it does not proactively detect that a cached
// client's remote cluster has become unreachable — that only surfaces as a
// failed request the next time resolveDSN uses it. Both are acceptable for
// the fleet sizes (tens, not thousands, of PostgresWatch CRs) this project
// targets today.
type remoteClientCache struct {
	mu      sync.Mutex
	clients map[string]client.Client
}

// newRemoteClientCache returns an empty cache.
func newRemoteClientCache() *remoteClientCache {
	return &remoteClientCache{clients: make(map[string]client.Client)}
}

// get returns a client.Client for kubeconfig, building and caching one if
// this exact kubeconfig content hasn't been seen before. It returns an
// error if kubeconfig cannot be parsed into a valid REST config (e.g. it is
// empty, malformed YAML, or missing a current context).
func (c *remoteClientCache) get(kubeconfig []byte) (client.Client, error) {
	key := hashKubeconfig(kubeconfig)

	c.mu.Lock()
	cached, ok := c.clients[key]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	built, err := buildRemoteClient(kubeconfig)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Another reconcile may have raced us and already built (and cached) a
	// client for this exact kubeconfig; prefer whichever one got there
	// first so callers holding onto an earlier reference stay consistent.
	if existing, ok := c.clients[key]; ok {
		return existing, nil
	}
	c.clients[key] = built
	return built, nil
}

func hashKubeconfig(kubeconfig []byte) string {
	sum := sha256.Sum256(kubeconfig)
	return hex.EncodeToString(sum[:])
}

// buildRemoteClient parses kubeconfig (raw YAML or JSON, the same format
// `kubectl config view --raw` produces) and returns a controller-runtime
// client scoped to whatever cluster/user/context it selects as current. It
// does not perform any network I/O itself — REST config construction is
// local — so an unreachable remote API server is only discovered the first
// time the returned client is actually used (e.g. resolveDSN's Get call),
// which is what causes markFailed / backoff to kick in rather than this
// function.
func buildRemoteClient(kubeconfig []byte) (client.Client, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}

	cl, err := client.New(restConfig, client.Options{Scheme: remoteClientScheme})
	if err != nil {
		return nil, fmt.Errorf("build client for remote cluster: %w", err)
	}
	return cl, nil
}
