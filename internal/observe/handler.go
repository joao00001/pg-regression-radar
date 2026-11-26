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

package observe

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Handler serves the Observe API over a Source. It is read-only: every route is
// a GET and nothing it calls can change cluster state.
type Handler struct {
	Source Source
	// AllowQueries lets a client see the normalised SQL text, by asking for it
	// with ?queries=1. Off by default: the text is stripped here, at the single
	// place everything leaves the API, even if a Source filled it. Whoever runs
	// the manager decides; a client cannot switch it on.
	AllowQueries bool
	// Now is the clock used to resolve relative times (now-6h); time.Now if nil.
	Now func() time.Time
	// Heartbeat is how often an idle event stream says it is alive; 15s if zero.
	Heartbeat time.Duration
}

// Routes registers the API on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /observe/version", h.version)
	mux.HandleFunc("GET /observe/v1/overview", h.overview)
	mux.HandleFunc("GET /observe/v1/deploys", h.deploys)
	mux.HandleFunc("GET /observe/v1/deploys/{id}", h.deployCase)
	mux.HandleFunc("GET /observe/v1/deploys/{id}/series", h.series)
	mux.HandleFunc("GET /observe/v1/actions", h.actions)
	mux.HandleFunc("GET /observe/v1/events", h.events)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": Version, "supported": []string{Version}})
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.Source.Overview(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	o.Version = Version
	if o.GeneratedAt.IsZero() {
		o.GeneratedAt = h.now()
	}
	writeJSON(w, http.StatusOK, o)
}

func (h *Handler) deploys(w http.ResponseWriter, r *http.Request) {
	rg, err := h.rng(r)
	if err != nil {
		writeBad(w, err)
		return
	}
	list, err := h.Source.Deploys(r.Context(), rg)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		kept := list[:0:0]
		for _, d := range list {
			if d.Namespace == ns {
				kept = append(kept, d)
			}
		}
		list = kept
	}
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && len(list) > n {
		list = list[:n]
	}
	if list == nil {
		list = []DeploySummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deploys": list})
}

func (h *Handler) deployCase(w http.ResponseWriter, r *http.Request) {
	c, ok, err := h.Source.Case(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeNotFound(w, "deploy")
		return
	}
	// The header says what the client got, so a UI that asked for the text and
	// did not get it can tell the person how to turn it on.
	if h.AllowQueries && r.URL.Query().Get("queries") == "1" {
		w.Header().Set("X-Observe-Queries", "shown")
	} else {
		w.Header().Set("X-Observe-Queries", "hidden")
		c = withoutText(c)
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) series(w http.ResponseWriter, r *http.Request) {
	unit := r.URL.Query().Get("unit")
	if unit == "" {
		writeBad(w, errors.New("unit is required"))
		return
	}
	s, ok, err := h.Source.Series(r.Context(), r.PathValue("id"), unit)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeNotFound(w, "series")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) actions(w http.ResponseWriter, r *http.Request) {
	rg, err := h.rng(r)
	if err != nil {
		writeBad(w, err)
		return
	}
	list, err := h.Source.Actions(r.Context(), rg)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []Action{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": list})
}

// events streams changes as Server-Sent Events. Each event names its type and
// carries its sequence number as the SSE id; a client that sees a gap, or
// reconnects, refetches the snapshot.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("streaming is not supported by this connection"))
		return
	}
	ch, cancel := h.Source.Subscribe()
	defer cancel()
	// The servers here set a write timeout to bound slow clients; a stream is
	// meant to outlive it, and its heartbeat is what proves the client is there.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "retry: 3000\nevent: hello\ndata: {\"version\":%q}\n\n", Version)
	fl.Flush()

	hb := h.Heartbeat
	if hb <= 0 {
		hb = 15 * time.Second
	}
	tick := time.NewTicker(hb)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-ch:
			if !open {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, b)
			fl.Flush()
		case <-tick.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// rng reads from and to. Each is RFC 3339, "now", or "now-<duration>" (6h, 90m,
// 7d). Absent means open.
func (h *Handler) rng(r *http.Request) (Range, error) {
	var out Range
	var err error
	if v := r.URL.Query().Get("from"); v != "" {
		if out.From, err = h.parseTime(v); err != nil {
			return out, fmt.Errorf("from: %w", err)
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if out.To, err = h.parseTime(v); err != nil {
			return out, fmt.Errorf("to: %w", err)
		}
	}
	if !out.From.IsZero() && !out.To.IsZero() && out.To.Before(out.From) {
		return out, errors.New("to is before from")
	}
	return out, nil
}

func (h *Handler) parseTime(v string) (time.Time, error) {
	if v == "now" {
		return h.now(), nil
	}
	if rest, ok := strings.CutPrefix(v, "now-"); ok {
		d, err := parseDuration(rest)
		if err != nil {
			return time.Time{}, err
		}
		return h.now().Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not RFC 3339, now, or now-<duration>", v)
	}
	return t, nil
}

// parseDuration reads 90m, 6h, 7d and 2w (Go's own parser has no days).
func parseDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		v, err := strconv.Atoi(n)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return time.Duration(v) * 24 * time.Hour, nil
	}
	if n, ok := strings.CutSuffix(s, "w"); ok {
		v, err := strconv.Atoi(n)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return time.Duration(v) * 7 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	return d, nil
}

// StripText returns c with every query's normalised text removed, without
// touching the original: for what is stored or shown when text is not allowed.
func StripText(c Case) Case { return withoutText(c) }

// withoutText is StripText's body.
func withoutText(c Case) Case {
	hist := make([]Evaluation, len(c.History))
	for i, e := range c.History {
		units := make([]UnitEvidence, len(e.Units))
		for j, u := range e.Units {
			u.Text = ""
			units[j] = u
		}
		e.Units = units
		hist[i] = e
	}
	c.History = hist
	return c
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func writeBad(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func writeNotFound(w http.ResponseWriter, what string) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": what + " not found"})
}
