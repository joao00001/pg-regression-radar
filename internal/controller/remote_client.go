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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// remoteClientTTL is how long a cached remote client may sit unused
	// before it is evicted. A watch whose remoteClusterSecretRef is still
	// in active use gets its cache entry refreshed at least every
	// statusRefreshInterval (30s) via Reconcile -> resolveDSN ->
	// dsnSecretClient -> get(), so this is generous headroom, not a tight
	// budget: it exists to reclaim clients for kubeconfigs that have been
	// rotated away from or whose PostgresWatch was deleted, not to evict
	// anything still genuinely in use.
	remoteClientTTL = 1 * time.Hour

	// remoteClientEvictionInterval is how often Start's sweep runs.
	remoteClientEvictionInterval = 10 * time.Minute
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
// Entries unused for longer than remoteClientTTL are evicted by Start's
// periodic sweep (see docs/multi-cluster.md) — this used to never happen at
// all, so a remote cluster that's decommissioned, or a kubeconfig that's
// rotated away from, would leave its old client's transport alive in memory
// for the life of the manager process. What's still deliberately out of
// scope: this cache does not proactively detect that a cached client's
// remote cluster has become unreachable — that only surfaces as a failed
// request the next time resolveDSN uses it.
type remoteClientCache struct {
	mu      sync.Mutex
	clients map[string]cacheEntry
}

// cacheEntry pairs a built client with when it was last handed out by get,
// so Start's sweep can tell an actively-used entry from an abandoned one.
type cacheEntry struct {
	client   client.Client
	lastUsed time.Time
}

// newRemoteClientCache returns an empty cache.
func newRemoteClientCache() *remoteClientCache {
	return &remoteClientCache{clients: make(map[string]cacheEntry)}
}

// get returns a client.Client for kubeconfig, building and caching one if
// this exact kubeconfig content hasn't been seen before. It returns an
// error if kubeconfig cannot be parsed into a valid REST config (e.g. it is
// empty, malformed YAML, or missing a current context). Every call — cache
// hit or miss — refreshes the entry's lastUsed timestamp, which is what
// keeps an actively-used remote client from ever being swept by Start.
func (c *remoteClientCache) get(kubeconfig []byte) (client.Client, error) {
	key := hashKubeconfig(kubeconfig)
	now := time.Now()

	c.mu.Lock()
	cached, ok := c.clients[key]
	if ok {
		cached.lastUsed = now
		c.clients[key] = cached
	}
	c.mu.Unlock()
	if ok {
		return cached.client, nil
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
		existing.lastUsed = now
		c.clients[key] = existing
		return existing.client, nil
	}
	c.clients[key] = cacheEntry{client: built, lastUsed: now}
	return built, nil
}

// evictOlderThan removes every entry last used before now.Add(-ttl),
// returning how many were removed. A free function of (ttl, now) rather
// than reading time.Now() itself so tests can drive eviction deterministic
// ally without a real clock or a real ticker.
func (c *remoteClientCache) evictOlderThan(ttl time.Duration, now time.Time) int {
	cutoff := now.Add(-ttl)

	c.mu.Lock()
	defer c.mu.Unlock()
	evicted := 0
	for key, entry := range c.clients {
		if entry.lastUsed.Before(cutoff) {
			delete(c.clients, key)
			evicted++
		}
	}
	return evicted
}

// Start implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable,
// registered via mgr.Add in PostgresWatchReconciler.SetupWithManager. It
// runs remoteClientEvictionInterval sweeps for the life of the manager
// process (gated by leader election exactly like the controller itself,
// since this cache is only ever populated by Reconcile calls, which only
// run on the leader) and returns cleanly when ctx is cancelled at shutdown.
func (c *remoteClientCache) Start(ctx context.Context) error {
	ticker := time.NewTicker(remoteClientEvictionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			c.evictOlderThan(remoteClientTTL, now)
		}
	}
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
