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

import "testing"

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
