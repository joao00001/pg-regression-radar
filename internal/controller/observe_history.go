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
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/observe"
	"github.com/joao00001/pg-regression-radar/internal/reason"
)

const (
	// seriesPoints bounds a stored series: the shape and the sample counts
	// survive, the cost does not grow with the collector's cadence.
	seriesPoints = 240
	// seriesUnits bounds the queries whose series are stored per deploy: the ones
	// the verdict rests on come first.
	seriesUnits   = 10
	flushEvery    = 2 * time.Second
	sweepEvery    = time.Minute
	storeDeadline = 10 * time.Second
)

// recorder is the writer's state: whether the last write worked, and what has
// been stored, so an unchanged deploy is not written again.
type recorder struct {
	mu        sync.Mutex
	failSince time.Time // zero while writes succeed
	lastErr   string
	stored    map[string]int // deploy id → evaluations at the last series write
}

func (r *recorder) fail(now time.Time, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSince.IsZero() {
		r.failSince = now
	}
	r.lastErr = err.Error()
}

func (r *recorder) ok() {
	r.mu.Lock()
	r.failSince, r.lastErr = time.Time{}, ""
	r.mu.Unlock()
}

func (r *recorder) failing() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failSince, !r.failSince.IsZero()
}

// RunRecorder copies what the radar concluded into History until ctx ends. It
// runs beside the decisions, never in them: a deploy is marked when an event says
// it changed (and every minute, for events the stream dropped), and written a
// moment later. A failing store is remembered and shown, and the radar goes on.
func (s *LiveSource) RunRecorder(ctx context.Context, log *slog.Logger) {
	if s.History == nil {
		<-ctx.Done()
		return
	}
	if log == nil {
		log = slog.Default()
	}
	events, cancel := s.Registry.Events.Subscribe()
	defer cancel()
	dirty := map[string]struct{}{}
	mark := func() {
		for _, v := range s.views() {
			for _, d := range v.ev.Deploys() {
				if v.allows(d.Namespace) {
					dirty[d.ID] = struct{}{}
				}
			}
		}
	}
	mark()
	flush := time.NewTicker(flushEvery)
	sweep := time.NewTicker(sweepEvery)
	defer flush.Stop()
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			if e.DeployID != "" {
				dirty[e.DeployID] = struct{}{}
			}
		case <-sweep.C:
			mark()
		case <-flush.C:
			if len(dirty) == 0 {
				continue
			}
			ids := make([]string, 0, len(dirty))
			for id := range dirty {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			clear(dirty)
			s.record(ctx, ids, log)
		}
	}
}

// record writes the named deploys. It is exported to tests through RecordNow.
func (s *LiveSource) record(ctx context.Context, ids []string, log *slog.Logger) {
	for _, id := range ids {
		c, ok := s.liveCase(id)
		if !ok {
			continue
		}
		if !s.StoreQueryText {
			c = observe.StripText(c)
		}
		wctx, cancel := context.WithTimeout(ctx, storeDeadline)
		err := s.History.Save(wctx, c)
		cancel()
		if err != nil {
			if _, was := s.rec.failing(); !was {
				log.Warn("observe history: cannot record; the radar keeps acting, history will have a gap", "reason", reason.HistoryNotRecording, "err", err)
			}
			s.rec.fail(s.now(), err)
			continue
		}
		s.rec.ok()
		s.recordSeries(ctx, c, log)
	}
}

// recordSeries stores the points of the queries the newest evaluation rests on,
// once per evaluation.
func (s *LiveSource) recordSeries(ctx context.Context, c observe.Case, log *slog.Logger) {
	s.rec.mu.Lock()
	if s.rec.stored == nil {
		s.rec.stored = map[string]int{}
	}
	if s.rec.stored[c.ID] == c.Evaluations || len(c.History) == 0 {
		s.rec.mu.Unlock()
		return
	}
	s.rec.mu.Unlock()
	last := c.History[len(c.History)-1]
	n := 0
	for _, u := range last.Units {
		if n >= seriesUnits {
			break
		}
		ser, ok := s.liveSeries(c.ID, u.Key)
		if !ok {
			continue
		}
		ser.Treatment = observe.Downsample(ser.Treatment, seriesPoints)
		ser.Control = observe.Downsample(ser.Control, seriesPoints)
		ser.Whole = observe.Downsample(ser.Whole, seriesPoints)
		wctx, cancel := context.WithTimeout(ctx, storeDeadline)
		err := s.History.SaveSeries(wctx, ser)
		cancel()
		if err != nil {
			s.rec.fail(s.now(), err)
			log.Debug("observe history: series not stored", "deploy", c.ID, "err", err)
			return
		}
		n++
	}
	s.rec.mu.Lock()
	s.rec.stored[c.ID] = c.Evaluations
	if len(s.rec.stored) > 4000 {
		clear(s.rec.stored)
	}
	s.rec.mu.Unlock()
}

// RecordNow writes the named deploys at once; for tests, which cannot wait for
// the recorder's tick.
func (s *LiveSource) RecordNow(ctx context.Context, ids ...string) {
	s.record(ctx, ids, slog.Default())
}

// historyInfo is what the status strip says about history.
func (s *LiveSource) historyInfo(ctx context.Context, now time.Time) observe.History {
	h := observe.History{Store: "memory", Recording: true}
	since := s.Started
	h.Since = &since
	h.Note = "Kept in this manager's memory since " + since.UTC().Format("Jan 2 15:04 UTC") + "; a restart of the manager clears it."
	if s.History != nil {
		ictx, cancel := context.WithTimeout(ctx, 3*time.Second)
		info, err := s.History.Info(ictx)
		cancel()
		if err == nil {
			h.Store = info.Kind
			h.Since = info.Since
			ret := info.Retention
			if ret == 0 {
				ret = s.Retention
			}
			h.RetentionSeconds = int64(ret.Seconds())
			if info.Kind == "postgres" {
				h.Note = "Kept in Postgres"
				if ret > 0 {
					h.Note += fmt.Sprintf(" for %s", shortDuration(ret))
				}
				h.Note += "; it survives a restart of the manager."
			} else if info.Since != nil {
				h.Note = "Kept in this manager's memory since " + info.Since.UTC().Format("Jan 2 15:04 UTC") + "; a restart of the manager clears it."
			}
		} else {
			h.Note = "The history store cannot be read right now; showing only what the manager holds."
		}
	}
	if since, failing := s.rec.failing(); failing {
		h.Recording = false
		h.Note = "History not recorded since " + since.UTC().Format("Jan 2 15:04 UTC") + ": the store could not be written. The radar keeps acting; this stretch will have a gap."
	}
	_ = now
	return h
}

func shortDuration(d time.Duration) string {
	if d >= 48*time.Hour && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return d.String()
}

// ---- reading: what is live, then what is stored ---------------------------------

// Deploys implements observe.Source. A deploy the evaluators still hold is shown
// from them (they are the freshest); older ones come from the store.
func (s *LiveSource) Deploys(ctx context.Context, r observe.Range) ([]observe.DeploySummary, error) {
	out := s.liveDeploys(r)
	if s.History == nil {
		return out, nil
	}
	have := make(map[string]bool, len(out))
	for _, d := range out {
		have[d.ID] = true
	}
	stored, err := s.History.Deploys(ctx, r)
	if err != nil {
		return out, nil // history is best effort; the live view still answers
	}
	now := s.now()
	for _, d := range stored {
		if have[d.ID] || !s.mayShow(d.Namespace) {
			continue
		}
		out = append(out, settle(d, now))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// settle removes what a stored summary can no longer promise: a "next check" that
// is already in the past.
func settle(d observe.DeploySummary, now time.Time) observe.DeploySummary {
	if d.NextEvaluation != nil && d.NextEvaluation.Before(now) {
		d.NextEvaluation = nil
	}
	return d
}

// Case implements observe.Source.
func (s *LiveSource) Case(ctx context.Context, id string) (observe.Case, bool, error) {
	if c, ok := s.liveCase(id); ok {
		return c, true, nil
	}
	if s.History == nil {
		return observe.Case{}, false, nil
	}
	c, ok, err := s.History.Case(ctx, id)
	if err != nil || !ok || !s.mayShow(c.Namespace) {
		return observe.Case{}, false, nil
	}
	c.DeploySummary = settle(c.DeploySummary, s.now())
	return c, true, nil
}

// Series implements observe.Source.
func (s *LiveSource) Series(ctx context.Context, id, unit string) (observe.Series, bool, error) {
	if x, ok := s.liveSeries(id, unit); ok {
		return x, true, nil
	}
	if s.History == nil {
		return observe.Series{}, false, nil
	}
	x, ok, err := s.History.Series(ctx, id, unit)
	if err != nil || !ok || !s.mayShow(x.Namespace) {
		return observe.Series{}, false, nil
	}
	return x, true, nil
}

// Actions implements observe.Source.
func (s *LiveSource) Actions(ctx context.Context, r observe.Range) ([]observe.Action, error) {
	out := s.liveActions(r)
	if s.History == nil {
		return out, nil
	}
	type key struct {
		at                     int64
		deploy, action, result string
	}
	have := make(map[key]bool, len(out))
	for _, a := range out {
		have[key{a.At.UnixNano(), a.DeployID, a.Action, a.Result}] = true
	}
	stored, err := s.History.Actions(ctx, r)
	if err != nil {
		return out, nil
	}
	for _, a := range stored {
		if s.mayShow(a.Namespace) && !have[key{a.At.UnixNano(), a.DeployID, a.Action, a.Result}] {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}
