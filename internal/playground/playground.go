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

// Package playground adapts snapshots of pg_stat_statements, taken by any
// client, into the correlation engine's input and runs the real engine over
// them. It exists for the docs playground, which compiles it to WebAssembly,
// but it has no build constraints so it is tested natively.
//
// The package depends only on pgstat, correlation and the API types, so the
// resulting WebAssembly module stays small.
package playground

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// Row is one pg_stat_statements row. QueryID is a string because queryid is a
// signed 64-bit integer, which a JavaScript number cannot hold exactly.
type Row struct {
	QueryID       string  `json:"queryid"`
	Query         string  `json:"query"`
	Calls         int64   `json:"calls"`
	TotalExecTime float64 `json:"total_exec_time"`
	// StatsSince is pg_stat_statements.stats_since in Unix milliseconds
	// (PostgreSQL 17+); zero or absent when the server does not report it.
	StatsSince int64 `json:"stats_since,omitempty"`
}

// Snapshot is every row read from pg_stat_statements at one instant.
type Snapshot struct {
	// At is the scrape time in Unix milliseconds.
	At   int64 `json:"at"`
	Rows []Row `json:"rows"`
}

// Config mirrors the operator flags that influence detection. Zero values
// select the engine defaults.
type Config struct {
	WindowMinutes          int     `json:"windowMinutes,omitempty"`
	MinSamples             int64   `json:"minSamples,omitempty"`
	MinExecutions          int64   `json:"minExecutions,omitempty"`
	LatencyChangeThreshold float64 `json:"latencyChangeThreshold,omitempty"`
	PValueThreshold        float64 `json:"pValueThreshold,omitempty"`
}

// Request is the playground's input.
type Request struct {
	Snapshots []Snapshot `json:"snapshots"`
	// DeployAt is the deploy time in Unix milliseconds.
	DeployAt int64  `json:"deployAt"`
	Config   Config `json:"config"`
}

// Point is one scrape of one query, as the collector would store it.
type Point struct {
	At int64 `json:"at"`
	// CumulativeMeanMs is what pg_stat_statements.mean_exec_time shows: the
	// mean over every call since the last reset.
	CumulativeMeanMs float64 `json:"cumulativeMeanMs"`
	// IntervalMeanMs is the mean latency of only the calls made since the
	// previous scrape, which is what the engine analyses. It is meaningful
	// only when IntervalKnown is true and IntervalCalls is positive.
	IntervalMeanMs float64 `json:"intervalMeanMs"`
	IntervalCalls  int64   `json:"intervalCalls"`
	IntervalKnown  bool    `json:"intervalKnown"`
	// Reset is true when the collector detected a counter reset at this scrape.
	Reset bool `json:"reset"`
}

// Series is the sampled history of one query.
type Series struct {
	QueryID string  `json:"queryid"`
	Query   string  `json:"query"`
	Points  []Point `json:"points"`
}

// Result is the engine's verdict for one query.
type Result struct {
	QueryID             string  `json:"queryid"`
	Query               string  `json:"query"`
	Status              string  `json:"status"`
	ConfidenceScore     float64 `json:"confidenceScore"`
	MeanLatencyBefore   float64 `json:"meanLatencyBefore"`
	MeanLatencyAfter    float64 `json:"meanLatencyAfter"`
	LatencyChangeFactor float64 `json:"latencyChangeFactor"`
	// DetectedChangeAt is the located change point in Unix milliseconds, or
	// zero unless Status is Detected.
	DetectedChangeAt int64 `json:"detectedChangeAt,omitempty"`
}

// Response is the playground's output. Error is set instead of the other
// fields when the request cannot be analysed.
type Response struct {
	Results []Result `json:"results"`
	Series  []Series `json:"series"`
	Error   string   `json:"error,omitempty"`
}

// Run analyses req with the real correlation engine.
func Run(req Request) Response {
	if len(req.Snapshots) == 0 {
		return Response{Error: "no snapshots"}
	}
	src, series, err := buildSource(req.Snapshots)
	if err != nil {
		return Response{Error: err.Error()}
	}

	eng := correlation.New(correlation.Config{
		WindowMinutes:          req.Config.WindowMinutes,
		MinSamples:             req.Config.MinSamples,
		MinExecutions:          req.Config.MinExecutions,
		LatencyChangeThreshold: req.Config.LatencyChangeThreshold,
		PValueThreshold:        req.Config.PValueThreshold,
	}, src, slog.New(slog.NewTextHandler(io.Discard, nil)))

	regs := eng.Analyse(v1alpha1.DeployEvent{
		ID:        "playground",
		App:       "playground",
		Namespace: "playground",
		Revision:  "new",
		Timestamp: time.UnixMilli(req.DeployAt),
	})

	resp := Response{Series: series, Results: make([]Result, 0, len(regs))}
	for _, r := range regs {
		res := Result{
			QueryID:             strconv.FormatInt(r.QueryID, 10),
			Query:               r.QueryText,
			Status:              string(r.Status),
			ConfidenceScore:     r.ConfidenceScore,
			MeanLatencyBefore:   r.MeanLatencyBefore,
			MeanLatencyAfter:    r.MeanLatencyAfter,
			LatencyChangeFactor: r.LatencyChangeFactor,
		}
		if !r.DetectedChangeAt.IsZero() {
			res.DetectedChangeAt = r.DetectedChangeAt.UnixMilli()
		}
		resp.Results = append(resp.Results, res)
	}
	return resp
}

// RunJSON is Run over JSON text, the form the WebAssembly entry point uses.
func RunJSON(in string) string {
	var req Request
	var resp Response
	if err := json.Unmarshal([]byte(in), &req); err != nil {
		resp = Response{Error: "invalid request: " + err.Error()}
	} else {
		resp = Run(req)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return `{"error":"cannot encode response"}`
	}
	return string(out)
}

// memSource is an in-memory correlation.SampleSource.
type memSource struct {
	samples map[int64][]pgstat.QuerySample
}

func (m *memSource) AllQueryIDs() []int64 {
	ids := make([]int64, 0, len(m.samples))
	for id := range m.samples {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (m *memSource) SamplesInRange(queryID int64, from, to time.Time) []pgstat.QuerySample {
	var out []pgstat.QuerySample
	for _, s := range m.samples[queryID] {
		if !s.RecordedAt.Before(from) && !s.RecordedAt.After(to) {
			out = append(out, s)
		}
	}
	return out
}

// buildSource feeds the snapshots to the same interval derivation the
// collector applies on every scrape.
func buildSource(snaps []Snapshot) (*memSource, []Series, error) {
	ordered := append([]Snapshot(nil), snaps...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At < ordered[j].At })

	src := &memSource{samples: make(map[int64][]pgstat.QuerySample)}
	texts := make(map[int64]string)
	for _, snap := range ordered {
		at := time.UnixMilli(snap.At)
		for _, row := range snap.Rows {
			id, err := strconv.ParseInt(row.QueryID, 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid queryid %q: %w", row.QueryID, err)
			}
			s := pgstat.QuerySample{
				QueryID:         id,
				QueryText:       row.Query,
				Calls:           row.Calls,
				TotalExecTimeMs: row.TotalExecTime,
				RecordedAt:      at,
			}
			if row.Calls > 0 {
				s.MeanExecTimeMs = row.TotalExecTime / float64(row.Calls)
			}
			if row.StatsSince != 0 {
				s.StatsSince = time.UnixMilli(row.StatsSince)
			}
			hist := src.samples[id]
			if len(hist) > 0 {
				s = pgstat.DeriveInterval(hist[len(hist)-1], s)
			}
			src.samples[id] = append(hist, s)
			texts[id] = row.Query
		}
	}

	ids := src.AllQueryIDs()
	series := make([]Series, 0, len(ids))
	for _, id := range ids {
		ser := Series{QueryID: strconv.FormatInt(id, 10), Query: texts[id]}
		for _, s := range src.samples[id] {
			ser.Points = append(ser.Points, Point{
				At:               s.RecordedAt.UnixMilli(),
				CumulativeMeanMs: s.MeanExecTimeMs,
				IntervalMeanMs:   s.IntervalMeanMs,
				IntervalCalls:    s.IntervalCalls,
				IntervalKnown:    s.IntervalKnown,
				Reset:            s.ResetDetected,
			})
		}
		series = append(series, ser)
	}
	return src, series, nil
}
