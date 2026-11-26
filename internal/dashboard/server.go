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

// Package dashboard serves the live dashboard: a small web UI embedded in the
// binary, in front of whatever answers the Observe API.
//
// The UI is plain ES modules and hand-written SVG with no build step and nothing
// fetched at run time, so the binary is all there is to trust. What it reads
// comes from a Backend, which is the Observe API itself (see internal/observe):
// recorded data in a fixture, or the manager of a cluster reached through a
// port-forward.
package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/joao00001/pg-regression-radar/internal/dashboard/layout"
)

//go:embed ui
var uiFiles embed.FS

func init() {
	// The font type is not in Go's built-in table, and the system's may lack it.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

// Config is what the UI is told about how it was started, at /dashboard/config.
type Config struct {
	// Origin is "fixture" or "cluster"; the UI says so on screen, so recorded
	// data is never mistaken for a live system.
	Origin string `json:"origin"`
	// Note is a short sentence the UI shows beside the origin.
	Note string `json:"note,omitempty"`
	// ShowQueries asks the Backend for the normalised SQL text.
	ShowQueries bool `json:"showQueries"`
}

// Server is the dashboard's HTTP handler.
type Server struct {
	// Backend answers /observe/... (the Observe API).
	Backend http.Handler
	Config  Config
	// Port is the port the server listens on; the Host header must name it.
	Port string
	// AnyHost accepts any Host name on that port. Only for --insecure-listen,
	// where the person chose to expose the dashboard beyond this machine.
	AnyHost bool
	// Status, when set, is served at /dashboard/status: how the connection to
	// the manager stands. Fixture runs have none.
	Status func() any
	// Layout answers /dashboard/layout: the boards to show, as the JSON bundle of
	// package layout, or the problems in the person's file. Nil serves the default
	// dashboard. It is called on every request, so editing the file and
	// reloading the page is enough.
	Layout LayoutProvider
	// Profile, when set, keeps the person's edits between runs at /dashboard/profile.
	// Nil turns saving off: the page then keeps them in the browser only.
	Profile *Profile
}

// LayoutProvider returns the JSON bundle to serve, or the problems that keep a
// dashboard file from being used.
type LayoutProvider func() (bundle []byte, problems []layout.Problem)

// FileLayout reads a dashboard file each time it is asked, so a person can edit
// it and reload. A file that does not pass is reported, never half used.
func FileLayout(path string) LayoutProvider {
	return func() ([]byte, []layout.Problem) {
		data, err := readLimited(path)
		if err != nil {
			return nil, []layout.Problem{{Msg: err.Error()}}
		}
		d, ps := layout.Parse(data)
		if len(ps) > 0 {
			return nil, ps
		}
		b, err := layout.NewBundle(d, path).JSON()
		if err != nil {
			return nil, []layout.Problem{{Msg: err.Error()}}
		}
		return b, nil
	}
}

// maxLayoutFile is far more than a dashboard needs.
const maxLayoutFile = 256 << 10

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- the path is the person's own --config flag
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxLayoutFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLayoutFile {
		return nil, fmt.Errorf("%s is larger than %d KiB; a dashboard file is a few KiB", path, maxLayoutFile>>10)
	}
	return data, nil
}

func defaultBundle() []byte {
	b, err := layout.NewBundle(layout.Default(), "default").JSON()
	if err != nil {
		panic(err) // encodes fixed data
	}
	return b
}

// Handler returns the handler to serve. It refuses anything that is not a GET (the
// profile's own PUT and DELETE, from this page only, aside),
// and any request whose Host is not this machine's loopback: a page on another
// site cannot read the dashboard by pointing a name it controls at 127.0.0.1
// (DNS rebinding).
func (s *Server) Handler() http.Handler {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err) // the embedded tree is fixed at build time
	}
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.Handle("/observe/", s.Backend)
	mux.HandleFunc("/dashboard/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s.Config)
	})
	mux.HandleFunc("/dashboard/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if s.Status == nil {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "connected"})
			return
		}
		_ = json.NewEncoder(w).Encode(s.Status())
	})
	def := defaultBundle()
	mux.HandleFunc("/dashboard/layout", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if s.Layout == nil {
			_, _ = w.Write(def)
			return
		}
		body, problems := s.Layout()
		if len(problems) > 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "the dashboard file has problems", "problems": problems})
			return
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/dashboard/profile", s.profile)
	mux.Handle("/", files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostOK(r.Host) {
			http.Error(w, "unexpected Host header", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// The one thing the page may change is its own profile file.
			if r.URL.Path != "/dashboard/profile" || (r.Method != http.MethodPut && r.Method != http.MethodDelete) {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "read-only", http.StatusMethodNotAllowed)
				return
			}
			if msg := sameOriginWrite(r); msg != "" {
				http.Error(w, msg, http.StatusForbidden)
				return
			}
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/observe/") {
			h.Set("Cache-Control", "no-cache")
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) hostOK(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	if s.Port != "" && p != s.Port {
		return false
	}
	if s.AnyHost {
		return h != ""
	}
	return h == "127.0.0.1" || h == "localhost" || h == "[::1]" || h == "::1"
}

// writeHeader is sent by the page on every save. A header that is not a CORS
// "simple" one makes a browser ask first, and this server never answers that
// question, so a page on another site cannot write.
const writeHeader = "X-Requested-By"
const writeHeaderValue = "pgrr-dashboard"

// sameOriginWrite is the second lock: even a request that got past the browser's
// rules must come from this page. It returns what is wrong, or "".
func sameOriginWrite(r *http.Request) string {
	if r.Header.Get(writeHeader) != writeHeaderValue {
		return "missing " + writeHeader
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return "cross-site request"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			return "cross-origin request"
		}
	}
	return ""
}

// profile serves /dashboard/profile: GET what is saved, PUT to save it, DELETE to
// forget it.
func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fail := func(code int, msg string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	if s.Profile == nil {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			_ = json.NewEncoder(w).Encode(map[string]any{"saved": false})
			return
		}
		fail(http.StatusNotFound, "saving is turned off (--no-save, or no data directory could be used)")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		out := map[string]any{"saved": true, "where": s.Profile.Where(), "profile": nil}
		data, err := s.Profile.Read()
		switch {
		case err != nil:
			out["problem"] = err.Error()
		case data != nil:
			if err := ValidateProfile(data); err != nil {
				// Left alone on disk: the next save replaces it, and nothing is lost by saying so.
				out["problem"] = "the saved profile could not be used (" + err.Error() + ")"
			} else {
				out["profile"] = json.RawMessage(data)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPut:
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			fail(http.StatusUnsupportedMediaType, "send application/json")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxProfile+1))
		if err != nil {
			fail(http.StatusRequestEntityTooLarge, fmt.Sprintf("a profile is a few KiB; at most %d KiB", MaxProfile>>10))
			return
		}
		if err := ValidateProfile(body); err != nil {
			fail(http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err := s.Profile.Write(body); err != nil {
			fail(http.StatusInternalServerError, "could not write "+s.Profile.Where()+": "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := s.Profile.Delete(); err != nil {
			fail(http.StatusInternalServerError, "could not remove "+s.Profile.Where()+": "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
