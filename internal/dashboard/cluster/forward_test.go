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

package cluster

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/streaming/pkg/httpstream"
	"k8s.io/streaming/pkg/httpstream/spdy"
)

// fakeAPIServer speaks the pod portforward subresource over SPDY, as kubelet
// behind an API server does, and bridges each forwarded connection to backend.
// It has no WebSocket tunnel, so the client must fall back to SPDY.
func fakeAPIServer(t *testing.T, backend string, wantPath string, wsAttempts *int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			http.NotFound(w, r)
			return
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "SPDY/3.1") {
			mu.Lock()
			*wsAttempts++
			mu.Unlock()
			http.Error(w, "websocket tunnel not offered", http.StatusBadRequest)
			return
		}
		if _, err := httpstream.Handshake(r, w, []string{"portforward.k8s.io"}); err != nil {
			return
		}
		type pair struct{ data, errs httpstream.Stream }
		pairs := map[string]*pair{}
		var pmu sync.Mutex
		upgrader := spdy.NewResponseUpgrader()
		conn := upgrader.UpgradeResponse(w, r, func(s httpstream.Stream, replySent <-chan struct{}) error {
			id := s.Headers().Get("requestID")
			pmu.Lock()
			p := pairs[id]
			if p == nil {
				p = &pair{}
				pairs[id] = p
			}
			if s.Headers().Get("streamType") == "error" {
				p.errs = s
			} else {
				p.data = s
			}
			ready := p.data != nil && p.errs != nil
			pmu.Unlock()
			// The reply to this stream is sent after the handler returns, so
			// waiting for it has to happen off to the side, as kubelet does.
			if ready {
				go func() {
					<-replySent
					defer func() { _ = p.data.Close() }()
					_ = p.errs.Close()
					c, err := net.Dial("tcp", backend)
					if err != nil {
						return
					}
					defer func() { _ = c.Close() }()
					go func() { _, _ = io.Copy(c, p.data) }()
					_, _ = io.Copy(p.data, c)
				}()
			}
			return nil
		})
		if conn == nil {
			return
		}
		defer func() { _ = conn.Close() }()
		<-conn.CloseChan()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestKubeDialerForwardsOverSPDYWithFallback(t *testing.T) {
	backendSrv, port := backendServer(t)
	_ = backendSrv
	backend := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	attempts := 0
	api := fakeAPIServer(t, backend, "/api/v1/namespaces/pgrr/pods/mgr-0/portforward", &attempts)

	cfg := &rest.Config{Host: api.URL}
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d := &KubeDialer{Config: cfg, Kube: kube}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	up := make(chan int, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- d.Forward(ctx, Target{Namespace: "pgrr", Pod: "mgr-0", Port: port}, func(p int) { up <- p })
	}()

	var local int
	select {
	case local = <-up:
	case err := <-errc:
		t.Fatalf("forward ended before it was up: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("forward never came up")
	}

	resp, err := http.Get("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(local)) + "/observe/version")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "v1") {
		t.Fatalf("got %q through the forward", body)
	}
	if attempts == 0 {
		t.Fatal("the client should have tried the WebSocket tunnel before falling back")
	}

	// The local end listens on loopback only.
	ln, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)))
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("a cancelled forward ends cleanly, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forward did not stop on cancel")
	}
}

func TestKubeDialerReportsMissingPortforwardPermission(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer api.Close()
	cfg := &rest.Config{Host: api.URL}
	kube, _ := kubernetes.NewForConfig(cfg)
	d := &KubeDialer{Config: cfg, Kube: kube}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := d.Forward(ctx, Target{Namespace: "pgrr", Pod: "mgr-0", Port: 8083}, func(int) { t.Error("must not come up") })
	if err == nil || !strings.Contains(err.Error(), "pods/portforward") {
		t.Fatalf("the error should name the permission, got %v", err)
	}
}
