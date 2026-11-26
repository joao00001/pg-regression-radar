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

package layout

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// A panel's query says which of the data the Observe API already serves the panel
// shows: which deploys, which deploy, which database queries. It is a handful of
// `field:value` terms, not a language: nothing here can reach data the API does
// not serve, and the dashboard stays read-only.
//
//	app:checkout state:regressed
//	deploy:abc123 query:"SELECT on orders"
//	statement:UPDATE table:orders*
//
// Terms are separated by spaces. A value is bare (no spaces or quotes) or in
// double quotes, with \" and \\ inside. Different fields must all hold; the same
// field given twice holds if either does. A `*` in a free-text value matches any
// characters. Matching ignores case. The browser parses the same grammar
// (query.js); testdata/query-cases.json is the contract both are tested against.

// MaxQuery is the longest query, in characters, and MaxTerms the most terms.
const (
	MaxQuery = 200
	MaxTerms = 12
)

// Term is one field:value, with the 1-based character column where it starts.
type Term struct {
	Field string
	Value string
	Col   int
}

// QueryError is why a query does not parse, and where (1-based character column).
type QueryError struct {
	Code string
	Col  int
}

func (e *QueryError) Error() string {
	switch e.Code {
	case "bad-term":
		return fmt.Sprintf("column %d: expected field:value, such as app:checkout", e.Col)
	case "empty-value":
		return fmt.Sprintf("column %d: this field has no value", e.Col)
	case "unterminated-quote":
		return fmt.Sprintf("column %d: this quote is never closed", e.Col)
	case "stray-quote":
		return fmt.Sprintf("column %d: a value with a quote in it must be wholly in quotes", e.Col)
	case "bad-escape":
		return fmt.Sprintf("column %d: inside quotes only \\\" and \\\\ can follow a backslash", e.Col)
	case "too-long":
		return fmt.Sprintf("a query is at most %d characters", MaxQuery)
	case "too-many":
		return fmt.Sprintf("a query has at most %d terms", MaxTerms)
	}
	return e.Code
}

// ParseQuery reads the grammar above. It checks only the shape; which fields a
// panel takes, and which values they allow, is ValidateQuery.
func ParseQuery(s string) ([]Term, *QueryError) {
	if utf8.RuneCountInString(s) > MaxQuery {
		return nil, &QueryError{Code: "too-long", Col: 1}
	}
	r := []rune(s)
	var terms []Term
	i := 0
	isSpace := func(c rune) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
	for {
		for i < len(r) && isSpace(r[i]) {
			i++
		}
		if i >= len(r) {
			break
		}
		start := i
		for i < len(r) && r[i] >= 'a' && r[i] <= 'z' {
			i++
		}
		if i == start || i >= len(r) || r[i] != ':' {
			return nil, &QueryError{Code: "bad-term", Col: start + 1}
		}
		field := string(r[start:i])
		i++ // the colon
		valStart := i
		var val []rune
		switch {
		case i < len(r) && r[i] == '"':
			i++
			closed := false
			for i < len(r) {
				c := r[i]
				if c == '\\' {
					if i+1 >= len(r) || (r[i+1] != '"' && r[i+1] != '\\') {
						return nil, &QueryError{Code: "bad-escape", Col: i + 1}
					}
					val = append(val, r[i+1])
					i += 2
					continue
				}
				if c == '"' {
					closed = true
					i++
					break
				}
				val = append(val, c)
				i++
			}
			if !closed {
				return nil, &QueryError{Code: "unterminated-quote", Col: valStart + 1}
			}
			if i < len(r) && !isSpace(r[i]) {
				return nil, &QueryError{Code: "bad-term", Col: i + 1}
			}
		default:
			for i < len(r) && !isSpace(r[i]) {
				if r[i] == '"' {
					return nil, &QueryError{Code: "stray-quote", Col: i + 1}
				}
				val = append(val, r[i])
				i++
			}
		}
		if len(val) == 0 {
			return nil, &QueryError{Code: "empty-value", Col: start + 1}
		}
		terms = append(terms, Term{Field: field, Value: string(val), Col: start + 1})
		if len(terms) > MaxTerms {
			return nil, &QueryError{Code: "too-many", Col: start + 1}
		}
	}
	return terms, nil
}

// QueryField is something a query can say.
type QueryField struct {
	Name string `json:"name"`
	Doc  string `json:"doc"`
	// Values, when set, are the only values the field takes (ignoring case).
	// Otherwise it is free text and may contain * as a wildcard.
	Values []string `json:"values,omitempty"`
}

// The fields, in the order the editor suggests them.
var queryFields = []QueryField{
	{Name: "app", Doc: "The application a deploy belongs to, such as checkout."},
	{Name: "namespace", Doc: "The Kubernetes namespace of the deploy."},
	{Name: "state", Doc: "Where the radar got to with a deploy.", Values: []string{"regressed", "acted", "watching", "healthy", "inconclusive"}},
	{Name: "grade", Doc: "The evidence grade of a deploy's verdict.", Values: []string{"A", "B", "C", "D"}},
	{Name: "deploy", Doc: "One deploy, by its id. Without it the panel follows the deploy picked on the board."},
	{Name: "query", Doc: "A database query, as the panels name it, such as \"SELECT on orders\"."},
	{Name: "statement", Doc: "The kind of statement: SELECT, INSERT, UPDATE or DELETE."},
	{Name: "table", Doc: "A table the query touches."},
}

// QueryFields returns the fields a query can use.
func QueryFields() []QueryField { return append([]QueryField(nil), queryFields...) }

func queryField(name string) (QueryField, bool) {
	for _, f := range queryFields {
		if f.Name == name {
			return f, true
		}
	}
	return QueryField{}, false
}

// ValidateQuery says what is wrong with a parsed query for a panel type, as
// messages that start with the character column they are about. A type that takes
// no query says so.
func ValidateQuery(pt PanelType, terms []Term) []string {
	var out []string
	if len(pt.Query) == 0 {
		if len(terms) > 0 {
			return []string{fmt.Sprintf("column %d: a %s panel takes no query", terms[0].Col, pt.Type)}
		}
		return nil
	}
	deploys := 0
	for _, t := range terms {
		f, known := queryField(t.Field)
		if !known || !contains(pt.Query, t.Field) {
			msg := fmt.Sprintf("column %d: a %s panel has no %q field; it takes %s", t.Col, pt.Type, t.Field, strings.Join(pt.Query, ", "))
			if s := nearest(t.Field, pt.Query); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			}
			out = append(out, msg)
			continue
		}
		if len(f.Values) > 0 {
			ok := false
			for _, v := range f.Values {
				ok = ok || strings.EqualFold(v, t.Value)
			}
			if !ok {
				out = append(out, fmt.Sprintf("column %d: %s is one of %s, not %q", t.Col, f.Name, strings.Join(f.Values, ", "), t.Value))
			}
		}
		if len(f.Values) > 0 && strings.Contains(t.Value, "*") {
			out = append(out, fmt.Sprintf("column %d: %s takes exact values; * works only in free text", t.Col, f.Name))
		}
		if f.Name == "deploy" {
			deploys++
			if deploys > 1 {
				out = append(out, fmt.Sprintf("column %d: a panel shows one deploy; give deploy: once", t.Col))
			}
			if strings.Contains(t.Value, "*") {
				out = append(out, fmt.Sprintf("column %d: deploy takes an exact id", t.Col))
			}
		}
	}
	return out
}
