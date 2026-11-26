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
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDialer "forwards" to a local test server and lets a test break it.
type fakeDialer struct {
	mu      sync.Mutex
	port    int
	breaks  map[string]chan error // per pod
	calls   []string
	failFor map[string]error
}

func newFakeDialer(port int) *fakeDialer {
	return &fakeDialer{port: port, breaks: map[string]chan error{}, failFor: map[string]error{}}
}

func (d *fakeDialer) Forward(ctx context.Context, t Target, up func(int)) error {
	d.mu.Lock()
	d.calls = append(d.calls, t.Pod)
	if err := d.failFor[t.Pod]; err != nil {
		d.mu.Unlock()
		return err
	}
	ch := make(chan error, 1)
	d.breaks[t.Pod] = ch
	d.mu.Unlock()
	up(d.port)
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (d *fakeDialer) breakPod(pod string, err error) {
	d.mu.Lock()
	ch := d.breaks[pod]
	d.mu.Unlock()
	ch <- err
}

func (d *fakeDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func backendServer(t *testing.T) (*httptest.Server, int) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/observe/v1/overview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"cookie":"`+r.Header.Get("Cookie")+`","host":"`+r.Host+`"}`)
	})
	mux.HandleFunc("/observe/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "event: hello\ndata: 1\n\n")
		fl.Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/observe/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"supported":["v1"]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(p)
	return srv, port
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", "secret=1")
	h.ServeHTTP(rec, req)
	return rec
}

func startSupervisor(t *testing.T, s *Supervisor) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestSupervisorProxiesAndStripsCookies(t *testing.T) {
	_, port := backendServer(t)
	d := newFakeDialer(port)
	s := &Supervisor{
		Find:       func(context.Context) (Target, error) { return Target{Namespace: "n", Pod: "p0", Port: 8083}, nil },
		Dial:       d,
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, Recheck: time.Hour,
	}
	startSupervisor(t, s)
	waitFor(t, "connected", func() bool { return s.State().Status == StatusConnected })
	rec := get(t, s.Handler(), "/observe/v1/overview")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("the browser's cookie reached the manager: %s", rec.Body)
	}
	if got := s.State().Pod; got != "n/p0" {
		t.Fatalf("pod = %q", got)
	}
}

func TestSupervisorAnswers503WhileNotConnected(t *testing.T) {
	s := &Supervisor{
		Find:       func(context.Context) (Target, error) { return Target{}, errors.New("no manager found") },
		Dial:       newFakeDialer(1),
		MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	}
	startSupervisor(t, s)
	waitFor(t, "error recorded", func() bool { return s.State().Error != "" })
	if st := s.State(); st.Status != StatusConnecting {
		t.Fatalf("a first failure is still connecting, got %s", st.Status)
	}
	rec := get(t, s.Handler(), "/observe/v1/overview")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "no manager found") {
		t.Fatalf("headers %v body %s", rec.Header(), rec.Body)
	}
}

func TestSupervisorReconnectsAfterBreak(t *testing.T) {
	_, port := backendServer(t)
	d := newFakeDialer(port)
	s := &Supervisor{
		Find:       func(context.Context) (Target, error) { return Target{Namespace: "n", Pod: "p0", Port: 8083}, nil },
		Dial:       d,
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, Recheck: time.Hour,
	}
	startSupervisor(t, s)
	waitFor(t, "connected", func() bool { return s.State().Status == StatusConnected })
	d.breakPod("p0", errors.New("connection reset"))
	waitFor(t, "second dial", func() bool { return d.callCount() >= 2 })
	waitFor(t, "connected again", func() bool { return s.State().Status == StatusConnected })
	if rec := get(t, s.Handler(), "/observe/v1/overview"); rec.Code != 200 {
		t.Fatalf("after reconnect: %d", rec.Code)
	}
}

func TestSupervisorShowsReconnectingNotConnecting(t *testing.T) {
	_, port := backendServer(t)
	d := newFakeDialer(port)
	var down atomic.Bool
	s := &Supervisor{
		Find: func(context.Context) (Target, error) {
			if down.Load() {
				return Target{}, errors.New("api server unreachable")
			}
			return Target{Namespace: "n", Pod: "p0", Port: 8083}, nil
		},
		Dial:       d,
		MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Recheck: time.Hour,
	}
	startSupervisor(t, s)
	waitFor(t, "connected", func() bool { return s.State().Status == StatusConnected })
	down.Store(true)
	d.breakPod("p0", errors.New("eof"))
	waitFor(t, "reconnecting", func() bool { return s.State().Status == StatusReconnecting })
	rec := get(t, s.Handler(), "/observe/v1/overview")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "reconnecting") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestSupervisorFollowsLeaderChange(t *testing.T) {
	_, port := backendServer(t)
	d := newFakeDialer(port)
	var leader atomic.Value
	leader.Store("p0")
	s := &Supervisor{
		Find: func(context.Context) (Target, error) {
			return Target{Namespace: "n", Pod: leader.Load().(string), Port: 8083}, nil
		},
		Dial:       d,
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, Recheck: 10 * time.Millisecond,
	}
	startSupervisor(t, s)
	waitFor(t, "connected to p0", func() bool { return s.State().Pod == "n/p0" && s.State().Status == StatusConnected })
	leader.Store("p1")
	waitFor(t, "connected to p1", func() bool { return s.State().Pod == "n/p1" && s.State().Status == StatusConnected })
}

func TestSupervisorIncompatibleManager(t *testing.T) {
	_, port := backendServer(t)
	s := &Supervisor{
		Find:       func(context.Context) (Target, error) { return Target{Namespace: "n", Pod: "p0", Port: 8083}, nil },
		Dial:       newFakeDialer(port),
		Verify:     func(ctx context.Context, hp string) error { return CheckVersion(ctx, hp, []string{"v9"}) },
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, Recheck: time.Hour,
	}
	startSupervisor(t, s)
	waitFor(t, "incompatible", func() bool { return s.State().Status == StatusIncompatible })
	rec := get(t, s.Handler(), "/observe/v1/overview")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "cannot talk") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(s.State().Error, "same release") {
		t.Fatalf("error should say what to do: %q", s.State().Error)
	}
}

func TestCheckVersion(t *testing.T) {
	srv, _ := backendServer(t)
	hp := strings.TrimPrefix(srv.URL, "http://")
	if err := CheckVersion(context.Background(), hp, []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckVersion(context.Background(), hp, []string{"v2"}); err == nil {
		t.Fatal("expected a mismatch")
	}
	none := httptest.NewServer(http.NotFoundHandler())
	defer none.Close()
	err := CheckVersion(context.Background(), strings.TrimPrefix(none.URL, "http://"), []string{"v1"})
	if err == nil || !strings.Contains(err.Error(), "no Observe API") {
		t.Fatalf("got %v", err)
	}
}

func TestSupervisorStreamsEventsUnbuffered(t *testing.T) {
	_, port := backendServer(t)
	s := &Supervisor{
		Find:       func(context.Context) (Target, error) { return Target{Namespace: "n", Pod: "p0", Port: 8083}, nil },
		Dial:       newFakeDialer(port),
		MinBackoff: time.Millisecond, Recheck: time.Hour,
	}
	startSupervisor(t, s)
	waitFor(t, "connected", func() bool { return s.State().Status == StatusConnected })
	front := httptest.NewServer(s.Handler())
	defer front.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, front.URL+"/observe/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The first event must arrive while the backend handler is still open.
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "event: hello") {
		t.Fatalf("line %q err %v", line, err)
	}
}

func TestSupervisorStaticURL(t *testing.T) {
	srv, _ := backendServer(t)
	u, _ := url.Parse(srv.URL)
	s := &Supervisor{Static: u}
	startSupervisor(t, s)
	if st := s.State(); st.Status != StatusConnected {
		t.Fatalf("static mode is connected from the start, got %s", st.Status)
	}
	if rec := get(t, s.Handler(), "/observe/v1/overview"); rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestBackoffGrowsAndResets(t *testing.T) {
	var n atomic.Int32
	times := make(chan time.Time, 16)
	s := &Supervisor{
		Find: func(context.Context) (Target, error) {
			n.Add(1)
			times <- time.Now()
			return Target{}, errors.New("nope")
		},
		Dial:       newFakeDialer(1),
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond,
	}
	startSupervisor(t, s)
	var last time.Time
	var gaps []time.Duration
	for i := 0; i < 5; i++ {
		select {
		case ts := <-times:
			if !last.IsZero() {
				gaps = append(gaps, ts.Sub(last))
			}
			last = ts
		case <-time.After(3 * time.Second):
			t.Fatal("not retrying")
		}
	}
	if gaps[len(gaps)-1] < 30*time.Millisecond {
		t.Fatalf("backoff should reach its cap, gaps %v", gaps)
	}
	if gaps[len(gaps)-1] > 400*time.Millisecond {
		t.Fatalf("backoff should be capped, gaps %v", gaps)
	}
}
