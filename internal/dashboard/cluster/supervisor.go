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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"sync"
	"time"
)

// Status is where the connection to the manager stands, in words the dashboard
// shows as they are.
type Status string

const (
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusReconnecting Status = "reconnecting"
	// StatusIncompatible: the manager speaks an API version this dashboard does not.
	StatusIncompatible Status = "incompatible"
)

// State is what /dashboard/status reports.
type State struct {
	Status Status `json:"status"`
	// Pod is the manager pod the forward goes to, when there is one.
	Pod   string    `json:"pod,omitempty"`
	Since time.Time `json:"since"`
	// Error says why the connection is down, or why it is incompatible.
	Error string `json:"error,omitempty"`
}

// Supervisor keeps one forward to the leader alive. It finds the leader, forwards
// to it, and on a failure or a change of leader finds it again, backing off
// between attempts. While there is no forward, requests are answered with a clear
// "reconnecting" instead of hanging or showing stale data without saying so.
type Supervisor struct {
	Find Finder
	Dial Dialer
	// Verify checks the API version once a forward is up; nil skips it.
	Verify func(ctx context.Context, hostport string) error
	Log    *slog.Logger
	// MinBackoff and MaxBackoff bound the wait between attempts (1s and 15s).
	MinBackoff, MaxBackoff time.Duration
	// Recheck is how often the leader is looked up while connected (10s).
	Recheck time.Duration
	// Static, when set, replaces the forward: requests go to this URL as they are
	// (for --url, tests, or an API the user exposed some other way).
	Static *url.URL
	// Now is the clock; time.Now if nil.
	Now func() time.Time

	mu    sync.Mutex
	local string // 127.0.0.1:port of the live forward
	state State
	proxy *httputil.ReverseProxy
	once  sync.Once
}

func (s *Supervisor) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Supervisor) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// State returns the current state.
func (s *Supervisor) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Static != nil {
		return State{Status: StatusConnected, Pod: s.Static.Host, Since: s.state.Since}
	}
	return s.state
}

func (s *Supervisor) set(st Status, pod string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Status != st || s.state.Pod != pod || (err != nil && s.state.Error != err.Error()) {
		s.state.Since = s.now()
	}
	s.state.Status, s.state.Pod, s.state.Error = st, pod, ""
	if err != nil {
		s.state.Error = err.Error()
	}
	if st != StatusConnected && st != StatusIncompatible {
		s.local = ""
	}
}

// Run keeps the forward alive until ctx ends.
func (s *Supervisor) Run(ctx context.Context) {
	if s.Static != nil {
		s.mu.Lock()
		s.state = State{Status: StatusConnected, Since: s.now()}
		s.mu.Unlock()
		<-ctx.Done()
		return
	}
	lo, hi := s.MinBackoff, s.MaxBackoff
	if lo <= 0 {
		lo = time.Second
	}
	if hi <= 0 {
		hi = 15 * time.Second
	}
	recheck := s.Recheck
	if recheck <= 0 {
		recheck = 10 * time.Second
	}
	s.set(StatusConnecting, "", nil)
	wait := lo
	everUp := false // until the first forward works, trouble is "connecting", not "reconnecting"
	for ctx.Err() == nil {
		t, err := s.Find(ctx)
		if err != nil {
			s.fail(!everUp, "", err)
			if !sleep(ctx, wait) {
				return
			}
			wait = min(wait*2, hi)
			continue
		}
		reached := s.session(ctx, t, recheck, !everUp)
		if reached {
			everUp = true
			wait = lo
		}
		if ctx.Err() != nil {
			return
		}
		if !sleep(ctx, wait) {
			return
		}
		wait = min(wait*2, hi)
	}
}

// fail records an error. Before the first connection it is still "connecting".
func (s *Supervisor) fail(first bool, pod string, err error) {
	prev := s.State().Error
	if first {
		s.set(StatusConnecting, pod, err)
	} else {
		s.set(StatusReconnecting, pod, err)
	}
	// Retries repeat the same error; say it once, not every few seconds.
	if prev != err.Error() {
		s.log().Warn("dashboard: cannot reach the manager", "err", err)
	}
}

// session runs one forward to t until it breaks, the leader changes or ctx ends.
// It reports whether the forward ever came up.
func (s *Supervisor) session(ctx context.Context, t Target, recheck time.Duration, first bool) (reached bool) {
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	upc := make(chan struct{}, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- s.Dial.Forward(fctx, t, func(port int) {
			hostport := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
			s.mu.Lock()
			s.local = hostport
			s.mu.Unlock()
			s.set(StatusConnected, t.String(), nil)
			s.log().Info("dashboard: connected to the manager", "pod", t.String())
			if s.Verify != nil {
				if err := s.Verify(fctx, hostport); err != nil {
					s.set(StatusIncompatible, t.String(), err)
				}
			}
			upc <- struct{}{}
		})
	}()
	tick := time.NewTicker(recheck)
	defer tick.Stop()
	for {
		select {
		case <-upc:
			reached = true
		case err := <-errc:
			if err == nil {
				err = errors.New("the port-forward ended")
			}
			s.fail(first && !reached, t.String(), err)
			return reached
		case <-tick.C:
			// The leader may have moved even though this forward still works.
			if nt, err := s.Find(ctx); err == nil && nt != t {
				s.log().Info("dashboard: the leader changed", "from", t.String(), "to", nt.String())
				cancel()
				<-errc
				s.set(StatusReconnecting, "", fmt.Errorf("the leader moved from %s to %s", t.Pod, nt.Pod))
				return reached
			}
		case <-ctx.Done():
			cancel()
			<-errc
			return reached
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// backend is where a request goes now, if anywhere.
func (s *Supervisor) backend() (*url.URL, bool) {
	if s.Static != nil {
		return s.Static, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.local == "" || s.state.Status == StatusIncompatible {
		return nil, false
	}
	return &url.URL{Scheme: "http", Host: s.local}, true
}

// Handler proxies requests to the manager. The event stream passes through
// unbuffered. When there is no forward it answers 503 and says why.
func (s *Supervisor) Handler() http.Handler {
	s.once.Do(func() {
		s.proxy = &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				u, _ := s.backend()
				r.SetURL(u)
				r.Out.Host = u.Host
				r.Out.Header.Del("Cookie")
			},
			FlushInterval: -1,
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				ResponseHeaderTimeout: 20 * time.Second,
				IdleConnTimeout:       30 * time.Second,
				MaxIdleConnsPerHost:   4,
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				writeProblem(w, http.StatusBadGateway, "The manager did not answer through the port-forward.", err.Error())
			},
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.backend(); !ok {
			st := s.State()
			msg := "Not connected to the manager yet."
			if st.Status == StatusReconnecting {
				msg = "Lost the connection to the manager; reconnecting."
			}
			if st.Status == StatusIncompatible {
				msg = "This dashboard cannot talk to that manager."
			}
			w.Header().Set("Retry-After", "2")
			writeProblem(w, http.StatusServiceUnavailable, msg, st.Error)
			return
		}
		s.proxy.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, code int, msg, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	body := map[string]string{"error": msg}
	if detail != "" {
		body["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(body)
}

// CheckVersion asks a manager's /observe/version and returns an error unless it
// offers a version in supported.
func CheckVersion(ctx context.Context, hostport string, supported []string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostport+"/observe/version", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("asking the manager for its API version: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("the manager has no Observe API (a release older than the dashboard, or --observe-addr is empty)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the manager answered %s to its version request", resp.Status)
	}
	var v struct {
		Supported []string `json:"supported"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<16)).Decode(&v); err != nil {
		return fmt.Errorf("reading the manager's API version: %w", err)
	}
	for _, s := range v.Supported {
		if slices.Contains(supported, s) {
			return nil
		}
	}
	return fmt.Errorf("this dashboard understands API %v; the manager offers %v. Use a dashboard of the same release as the manager", supported, v.Supported)
}
