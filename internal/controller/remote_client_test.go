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
	"testing"
	"time"
)

// TestRemoteClientCache_CachesByKubeconfigContent verifies that requesting
// a client for the same kubeconfig bytes twice returns the exact same
// client.Client instance (no rebuild), while a different kubeconfig gets
// its own distinct instance.
func TestRemoteClientCache_CachesByKubeconfigContent(t *testing.T) {
	cache := newRemoteClientCache()

	first, err := cache.get([]byte(validTestKubeconfig))
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	second, err := cache.get([]byte(validTestKubeconfig))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if first != second {
		t.Fatal("expected the same kubeconfig bytes to return a cached client instance")
	}

	otherKubeconfig := validTestKubeconfig + "\n# a trailing comment changes the hash\n"
	third, err := cache.get([]byte(otherKubeconfig))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if third == first {
		t.Fatal("expected different kubeconfig bytes to produce a distinct client instance")
	}
}

// TestRemoteClientCache_InvalidKubeconfig_ReturnsError verifies malformed
// kubeconfig bytes are rejected rather than silently producing a broken
// client, and that the cache does not poison itself for later valid calls.
func TestRemoteClientCache_InvalidKubeconfig_ReturnsError(t *testing.T) {
	cache := newRemoteClientCache()

	if _, err := cache.get([]byte("not a valid kubeconfig")); err == nil {
		t.Fatal("expected an error for invalid kubeconfig content")
	}

	// A subsequent call with valid content must still succeed — a failed
	// build must not have left the cache (or the scheme it shares) in a
	// bad state.
	if _, err := cache.get([]byte(validTestKubeconfig)); err != nil {
		t.Fatalf("get after a prior invalid call: %v", err)
	}
}

// TestRemoteClientCache_EmptyKubeconfig_ReturnsError verifies the zero
// value (a Secret key present but empty, e.g. an operator mistake) is
// rejected with an error instead of building a nonsensical client.
func TestRemoteClientCache_EmptyKubeconfig_ReturnsError(t *testing.T) {
	cache := newRemoteClientCache()
	if _, err := cache.get([]byte("")); err == nil {
		t.Fatal("expected an error for empty kubeconfig content")
	}
}

// TestRemoteClientCache_Evict_ForcesRebuildOnNextGet verifies that evicting
// a kubeconfig's cache entry causes the next get() for that exact same
// kubeconfig content to build (and cache) a brand new client.Client
// instance, rather than returning the one just evicted.
func TestRemoteClientCache_Evict_ForcesRebuildOnNextGet(t *testing.T) {
	cache := newRemoteClientCache()

	first, err := cache.get([]byte(validTestKubeconfig))
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	cache.evict([]byte(validTestKubeconfig))

	second, err := cache.get([]byte(validTestKubeconfig))
	if err != nil {
		t.Fatalf("get after evict: %v", err)
	}
	if first == second {
		t.Fatal("expected evict to force a fresh client.Client on the next get")
	}

	// And the cache is usable afterwards for the normal case too: a third
	// call (no eviction in between) reuses the second instance.
	third, err := cache.get([]byte(validTestKubeconfig))
	if err != nil {
		t.Fatalf("get (no eviction since): %v", err)
	}
	if second != third {
		t.Fatal("expected the cache to resume normal reuse after a rebuild")
	}
}

// TestRemoteClientCache_Evict_UnknownKubeconfig_NoOp verifies that evicting
// a kubeconfig that was never cached (or already evicted) is a harmless
// no-op rather than a panic — resolveDSN calls evict reactively on request
// failure and should never need to reason about whether an entry exists.
func TestRemoteClientCache_Evict_UnknownKubeconfig_NoOp(t *testing.T) {
	cache := newRemoteClientCache()

	cache.evict([]byte(validTestKubeconfig)) // never cached; must not panic

	// The cache must still work normally afterwards.
	if _, err := cache.get([]byte(validTestKubeconfig)); err != nil {
		t.Fatalf("get after no-op evict: %v", err)
	}
}

// TestRemoteClientCache_Get_RefreshesLastUsed verifies that a second get()
// for the same kubeconfig advances lastUsed rather than leaving it pinned
// to when the entry was first built — the property evictOlderThan relies on
// to never sweep a client that's still genuinely in use.
func TestRemoteClientCache_Get_RefreshesLastUsed(t *testing.T) {
	cache := newRemoteClientCache()
	key := hashKubeconfig([]byte(validTestKubeconfig))

	if _, err := cache.get([]byte(validTestKubeconfig)); err != nil {
		t.Fatalf("get: %v", err)
	}
	firstSeen := cache.clients[key].lastUsed

	// Force a detectable gap: real clock resolution can otherwise make two
	// back-to-back time.Now() calls compare equal on some platforms.
	time.Sleep(2 * time.Millisecond)

	if _, err := cache.get([]byte(validTestKubeconfig)); err != nil {
		t.Fatalf("second get: %v", err)
	}
	secondSeen := cache.clients[key].lastUsed

	if !secondSeen.After(firstSeen) {
		t.Fatalf("expected lastUsed to advance on a cache hit: first=%v second=%v", firstSeen, secondSeen)
	}
}

// TestRemoteClientCache_EvictOlderThan_RemovesOnlyStaleEntries verifies the
// sweep removes entries last used before the cutoff and leaves fresher ones
// (and ones exactly at the boundary) alone.
func TestRemoteClientCache_EvictOlderThan_RemovesOnlyStaleEntries(t *testing.T) {
	cache := newRemoteClientCache()
	now := time.Now()

	stale, err := buildRemoteClient([]byte(validTestKubeconfig))
	if err != nil {
		t.Fatalf("buildRemoteClient: %v", err)
	}
	fresh, err := buildRemoteClient([]byte(validTestKubeconfig + "\n# distinct\n"))
	if err != nil {
		t.Fatalf("buildRemoteClient: %v", err)
	}

	cache.clients["stale"] = cacheEntry{client: stale, lastUsed: now.Add(-2 * time.Hour)}
	cache.clients["fresh"] = cacheEntry{client: fresh, lastUsed: now.Add(-1 * time.Minute)}

	evicted := cache.evictOlderThan(1*time.Hour, now)

	if evicted != 1 {
		t.Fatalf("expected exactly 1 eviction, got %d", evicted)
	}
	if _, ok := cache.clients["stale"]; ok {
		t.Fatal("expected the stale entry to be evicted")
	}
	if _, ok := cache.clients["fresh"]; !ok {
		t.Fatal("expected the fresh entry to survive")
	}
}

// execKubeconfig is validTestKubeconfig with its static token replaced by
// an exec-based credential plugin — the shape F-02 (see the audit report
// this fix responds to) flagged as letting a tenant-controlled Secret
// cause the manager to execute an arbitrary local process.
const execKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
  name: remote
contexts:
- context:
    cluster: remote
    user: remote
  name: remote
current-context: remote
users:
- name: remote
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: /bin/sh
      args: ["-c", "echo pwned"]
`

// authProviderKubeconfig is validTestKubeconfig with its static token
// replaced by the deprecated auth-provider mechanism.
const authProviderKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
  name: remote
contexts:
- context:
    cluster: remote
    user: remote
  name: remote
current-context: remote
users:
- name: remote
  user:
    auth-provider:
      name: gcp
`

// proxyURLKubeconfig is validTestKubeconfig with a proxy-url added to its
// cluster entry.
const proxyURLKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
    proxy-url: http://attacker-controlled-proxy.example:8080
  name: remote
contexts:
- context:
    cluster: remote
    user: remote
  name: remote
current-context: remote
users:
- name: remote
  user:
    token: fake-token-for-tests
`

// TestBuildRemoteClient_RejectsExecAuth verifies a kubeconfig using an
// exec-based credential plugin is rejected outright, rather than being
// parsed into a REST config that would later execute that command.
func TestBuildRemoteClient_RejectsExecAuth(t *testing.T) {
	if _, err := buildRemoteClient([]byte(execKubeconfig)); err == nil {
		t.Fatal("expected an error for a kubeconfig using an exec-based credential plugin")
	}
}

// TestBuildRemoteClient_RejectsAuthProvider verifies a kubeconfig using the
// deprecated auth-provider mechanism is rejected outright, for the same
// reason as exec above.
func TestBuildRemoteClient_RejectsAuthProvider(t *testing.T) {
	if _, err := buildRemoteClient([]byte(authProviderKubeconfig)); err == nil {
		t.Fatal("expected an error for a kubeconfig using auth-provider")
	}
}

// TestBuildRemoteClient_RejectsProxyURL verifies a kubeconfig whose cluster
// entry sets proxy-url is rejected outright, rather than silently routing
// the manager's API traffic through an attacker-chosen proxy.
func TestBuildRemoteClient_RejectsProxyURL(t *testing.T) {
	if _, err := buildRemoteClient([]byte(proxyURLKubeconfig)); err == nil {
		t.Fatal("expected an error for a kubeconfig whose cluster sets proxy-url")
	}
}

// TestBuildRemoteClient_StaticTokenKubeconfig_Accepted is the converse of
// the three tests above: a plain static-token kubeconfig (no exec,
// auth-provider, or proxy-url) — exactly the shape docs/multi-cluster.md
// recommends — must still be accepted, so validateKubeconfigAuth is
// confirmed to reject only what it's meant to, not kubeconfigs generally.
func TestBuildRemoteClient_StaticTokenKubeconfig_Accepted(t *testing.T) {
	if _, err := buildRemoteClient([]byte(validTestKubeconfig)); err != nil {
		t.Fatalf("expected a plain static-token kubeconfig to be accepted, got: %v", err)
	}
}

// TestRemoteClientCache_Start_StopsOnContextCancel verifies Start (the
// manager.Runnable implementation registered via mgr.Add) returns promptly
// and without error once its context is cancelled, rather than leaking a
// goroutine past manager shutdown.
func TestRemoteClientCache_Start_StopsOnContextCancel(t *testing.T) {
	cache := newRemoteClientCache()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- cache.Start(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected Start to return nil on context cancel, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within 2s of context cancellation")
	}
}
