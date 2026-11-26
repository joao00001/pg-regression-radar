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

package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/observe"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	f, err := observe.LoadFixture("../observe/testdata/recorded-sim.json")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&observe.Handler{Source: observe.Scoped(observe.NewFixtureSource(f), observe.Scope{All: true})}).Routes(mux)
	s := &Server{Backend: mux, Config: Config{Origin: "fixture"}}
	ts := httptest.NewServer(s.Handler())
	s.Port = strings.TrimPrefix(ts.URL[strings.LastIndex(ts.URL, ":"):], ":")
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, host string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestServesTheUIAndTheAPIAndTheConfig(t *testing.T) {
	ts := testServer(t)
	for path, want := range map[string]string{
		"/":                    "text/html",
		"/observe/v1/overview": "application/json",
		"/dashboard/config":    "application/json",
		"/js/main.js":          "text/javascript",
		"/app.css":             "text/css",
	} {
		resp := do(t, "GET", ts.URL+path, "")
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), want) {
			t.Errorf("%s: %d %q, want 200 %s", path, resp.StatusCode, resp.Header.Get("Content-Type"), want)
		}
	}
}

func TestRefusesAForeignHostAndAnyWrite(t *testing.T) {
	ts := testServer(t)
	// DNS rebinding: the browser sends the attacker's name as Host.
	if r := do(t, "GET", ts.URL+"/observe/v1/overview", "evil.example:"+ts.Listener.Addr().String()[strings.LastIndex(ts.Listener.Addr().String(), ":")+1:]); r.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("foreign host answered %d", r.StatusCode)
	}
	if r := do(t, "GET", ts.URL+"/", "localhost:1"); r.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("wrong port answered %d", r.StatusCode)
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if r := do(t, m, ts.URL+"/observe/v1/actions", ""); r.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s answered %d", m, r.StatusCode)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	ts := testServer(t)
	h := do(t, "GET", ts.URL+"/", "").Header
	csp := h.Get("Content-Security-Policy")
	for _, must := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "font-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, must) {
			t.Errorf("CSP lacks %q: %s", must, csp)
		}
	}
	if strings.Contains(csp, "unsafe") {
		t.Errorf("CSP allows unsafe: %s", csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
}

// The page needs its fonts and logo from the binary itself (the CSP lets it read
// nothing else), and a font must be served as one.
func TestServesEmbeddedFontsAndBrand(t *testing.T) {
	ts := testServer(t)
	for path, want := range map[string]string{
		"/fonts/roboto-latin-wght-normal.woff2":      "font/woff2",
		"/fonts/roboto-mono-latin-wght-normal.woff2": "font/woff2",
		"/brand/logo-on-dark.svg":                    "image/svg+xml",
		"/brand/logo-on-light.svg":                   "image/svg+xml",
		"/brand/favicon.svg":                         "image/svg+xml",
		"/js/theme.js":                               "text/javascript",
	} {
		r := do(t, "GET", ts.URL+path, "")
		if r.StatusCode != http.StatusOK {
			t.Errorf("%s answered %d", path, r.StatusCode)
			continue
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("%s is served as %q, want %q", path, got, want)
		}
	}
}

func TestStatusEndpoint(t *testing.T) {
	s := &Server{
		Backend: http.NotFoundHandler(),
		Status:  func() any { return map[string]string{"status": "reconnecting"} },
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	s.Port = strings.TrimPrefix(ts.URL[strings.LastIndex(ts.URL, ":"):], ":")
	resp := do(t, http.MethodGet, ts.URL+"/dashboard/status", "")
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(b.String(), "reconnecting") {
		t.Fatalf("status: %d %s", resp.StatusCode, b)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status must not be cached: %v", resp.Header)
	}
}

func layoutBody(t *testing.T, s *Server) (int, string) {
	t.Helper()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	s.Port = strings.TrimPrefix(ts.URL[strings.LastIndex(ts.URL, ":"):], ":")
	resp := do(t, http.MethodGet, ts.URL+"/dashboard/layout", "")
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	return resp.StatusCode, b.String()
}

func TestLayoutDefaultsToThePreset(t *testing.T) {
	code, body := layoutBody(t, &Server{Backend: http.NotFoundHandler()})
	if code != 200 || !strings.Contains(body, `"source":"default"`) || !strings.Contains(body, `"catalog"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestLayoutFromAFileIsReadOnEveryRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.yaml")
	good := "apiVersion: pgrr.io/v1\nkind: Dashboard\nboards:\n  - id: only\n    panels:\n      - {id: s, type: summary, at: [0, 0, 12, 3]}\n"
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{Backend: http.NotFoundHandler(), Layout: FileLayout(path)}
	if code, body := layoutBody(t, s); code != 200 || !strings.Contains(body, `"id":"only"`) || !strings.Contains(body, path) {
		t.Fatalf("%d %s", code, body)
	}
	// Edit it into something invalid: the next request says so, with the line.
	bad := strings.Replace(good, "[0, 0, 12, 3]", "[0, 0, 13, 3]", 1)
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	code, body := layoutBody(t, s)
	if code != 422 || !strings.Contains(body, `"line":6`) || !strings.Contains(body, "ends at column 13") {
		t.Fatalf("%d %s", code, body)
	}
	// And a missing file is a problem too, not a crash.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if code, _ := layoutBody(t, s); code != 422 {
		t.Fatalf("missing file: %d", code)
	}
}

func TestLayoutFileTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.yaml")
	if err := os.WriteFile(path, []byte(strings.Repeat("# x\n", 80000)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ps := FileLayout(path)()
	if len(ps) != 1 || !strings.Contains(ps[0].Msg, "larger than") {
		t.Fatalf("%v", ps)
	}
}
