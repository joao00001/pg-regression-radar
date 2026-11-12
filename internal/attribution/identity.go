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

package attribution

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// A query's identity has to survive things that change pg_stat_statements'
// queryid without changing the work: the PostgreSQL documentation says queryid
// depends on object OIDs, search_path and the platform, and is not stable across
// major versions. The unit key is therefore the fingerprint of the normalised
// text (internal/collector.FingerprintQuery), never the queryid.
//
// A query whose text changed is, by construction, a different unit. It has no
// history of its own, so it cannot be judged against a baseline; it is a
// birth, and the old unit is a death. What can be said about the pair is
// weaker, and the Lineage type records it so that nothing is inferred beyond it.

var (
	relRe  = regexp.MustCompile(`(?i)\b(?:from|join|into|update)\s+(?:only\s+)?("?[a-z_][a-z0-9_$]*"?(?:\."?[a-z_][a-z0-9_$]*"?)?)`)
	kindRe = regexp.MustCompile(`(?i)^\s*(?:with\b.*?\)\s*)?(select|insert|update|delete|merge|call|copy)\b`)
	// words that follow FROM/JOIN/UPDATE/INTO without naming a relation
	notRelation = map[string]bool{
		"select": true, "lateral": true, "unnest": true, "generate_series": true, "values": true,
		"only": true, "set": true, "where": true, "table": true, "jsonb_to_recordset": true,
		"json_to_recordset": true, "jsonb_array_elements": true, "json_array_elements": true,
	}
)

// Structure extracts the statement kind and the sorted, de-duplicated relations
// a statement reads or writes. It is a lexical heuristic over the normalised
// text, deliberately cheap: an error makes two queries look related when they
// are not (they are then both ineligible as each other's donors and eligible as
// siblings) or hides a relation, never invents evidence.
func Structure(sql string) (kind string, tables []string) {
	if m := kindRe.FindStringSubmatch(sql); m != nil {
		kind = strings.ToLower(m[1])
	} else {
		kind = "other"
	}
	seen := map[string]bool{}
	for _, m := range relRe.FindAllStringSubmatch(sql, -1) {
		name := strings.ToLower(strings.Trim(m[1], `"`))
		name = strings.ReplaceAll(name, `"`, "")
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:] // schema-qualified: the relation name is what an ANALYZE names
		}
		if name == "" || notRelation[name] || seen[name] {
			continue
		}
		seen[name] = true
		tables = append(tables, name)
	}
	sort.Strings(tables)
	return kind, tables
}

// FamilyKey is the coarse structural key of a statement: kind and tables.
func FamilyKey(sql string) string {
	kind, tables := Structure(sql)
	return kind + ":" + strings.Join(tables, ",")
}

// Lineage links a unit that disappeared to units of the same family that
// appeared around the same time: the signature of a rewritten query.
type Lineage struct {
	From, To string // unit keys
	Family   string
	// Exclusive is true when the family had exactly one death and one birth, so
	// the link is unambiguous.
	Exclusive bool
}

// LineagesAt finds rewritten queries around the instant cut: units with
// observations before cut and none after (deaths) matched with units whose first
// observation is at or after cut (births) in the same family. Several
// candidates in one family are all reported with Exclusive=false, and the
// caller must not treat them as the same query.
func LineagesAt(units []Unit, cut time.Time) []Lineage {
	type span struct{ first, last time.Time }
	spans := make([]span, len(units))
	for i, u := range units {
		for j, o := range u.Obs {
			if j == 0 || o.At.Before(spans[i].first) {
				spans[i].first = o.At
			}
			if o.At.After(spans[i].last) {
				spans[i].last = o.At
			}
		}
	}
	deaths := map[string][]int{}
	births := map[string][]int{}
	for i, u := range units {
		if len(u.Obs) == 0 || u.Family == "" {
			continue
		}
		switch {
		case spans[i].last.Before(cut):
			deaths[u.Family] = append(deaths[u.Family], i)
		case !spans[i].first.Before(cut):
			births[u.Family] = append(births[u.Family], i)
		}
	}
	var out []Lineage
	fams := make([]string, 0, len(deaths))
	for f := range deaths {
		if len(births[f]) > 0 {
			fams = append(fams, f)
		}
	}
	sort.Strings(fams)
	for _, f := range fams {
		excl := len(deaths[f]) == 1 && len(births[f]) == 1
		for _, d := range deaths[f] {
			for _, b := range births[f] {
				out = append(out, Lineage{From: units[d].Key, To: units[b].Key, Family: f, Exclusive: excl})
			}
		}
	}
	return out
}
