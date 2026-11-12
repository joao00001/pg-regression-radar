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

// Package adapter turns pg_stat_statements samples into the engine's panel of
// units. It is the only place that knows pgstat; the engine itself stays pure.
package adapter

import (
	"sort"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
)

// Fingerprint maps query text to its stable identity. Pass
// collector.FingerprintQuery; it is a parameter so this package does not depend
// on the collector.
type Fingerprint func(sql string) string

// Units groups samples by the fingerprint of their normalised text, never by
// queryid (which changes with object OIDs, search_path and the server version),
// and returns one unit per fingerprint with its observations in time order.
//
// Several queryids that share a fingerprint are one unit: their interval calls
// and times are summed per scrape. A sample whose interval is known but empty
// (no calls) is not an observation. A sample from a source that only reports a
// mean has no interval; it is kept with its mean and an unknown weight (Calls
// 0), which the engine treats as a typical weight.
//
// declared are the relations the deploy says it changes; units that read one are
// marked Touched so they are never used as donors.
func Units(samples []pgstat.QuerySample, fp Fingerprint, declared []string) []attribution.Unit {
	type acc struct {
		calls int64
		total float64
		mean  float64
		n     int
		known bool
	}
	decl := map[string]bool{}
	for _, t := range declared {
		decl[t] = true
	}
	units := map[string]*attribution.Unit{}
	per := map[string]map[time.Time]*acc{}
	for _, s := range samples {
		if s.QueryText == "" || s.RecordedAt.IsZero() {
			continue
		}
		key := fp(s.QueryText)
		u := units[key]
		if u == nil {
			kind, tables := attribution.Structure(s.QueryText)
			u = &attribution.Unit{Key: key, Text: s.QueryText, Tables: tables, Family: kind + ":" + join(tables)}
			for _, t := range tables {
				if decl[t] {
					u.Touched = true
				}
			}
			units[key] = u
			per[key] = map[time.Time]*acc{}
		}
		u.QueryIDs = appendUnique(u.QueryIDs, s.QueryID)
		a := per[key][s.RecordedAt]
		if a == nil {
			a = &acc{}
			per[key][s.RecordedAt] = a
		}
		switch {
		case s.IntervalKnown:
			if s.IntervalCalls <= 0 {
				continue
			}
			a.calls += s.IntervalCalls
			a.total += s.IntervalTotalMs
			a.known = true
		case !s.IntervalComputed && s.MeanExecTimeMs > 0:
			a.mean += s.MeanExecTimeMs
			a.n++
		}
	}
	out := make([]attribution.Unit, 0, len(units))
	for key, u := range units {
		for at, a := range per[key] {
			switch {
			case a.known:
				u.Obs = append(u.Obs, attribution.Obs{At: at, MeanMs: a.total / float64(a.calls), Calls: a.calls})
			case a.n > 0:
				u.Obs = append(u.Obs, attribution.Obs{At: at, MeanMs: a.mean / float64(a.n)})
			}
		}
		sort.Slice(u.Obs, func(i, j int) bool { return u.Obs[i].At.Before(u.Obs[j].At) })
		if len(u.Obs) > 0 {
			out = append(out, *u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func join(s []string) string {
	r := ""
	for i, x := range s {
		if i > 0 {
			r += ","
		}
		r += x
	}
	return r
}

func appendUnique(s []int64, v int64) []int64 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
