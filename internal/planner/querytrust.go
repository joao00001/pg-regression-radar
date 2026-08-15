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

package planner

// NormalizedQueryText is an opaque wrapper around a query string that has been
// read directly from pg_stat_statements.query. It exists solely to enforce the
// trust boundary at the package API level: callers must explicitly construct
// one via NormalizeQueryText, signalling that the string originates from
// pg_stat_statements (already normalized and stored by PostgreSQL itself)
// rather than from an externally-controlled or lightly-validated source.
//
// Because EXPLAIN does not accept its target statement as a bind parameter
// there is no parameterized alternative; CaptureGenericPlan must interpolate
// the text into the SQL string verbatim. The NormalizedQueryText type prevents
// arbitrary strings from reaching that interpolation without the caller
// consciously marking them as trusted.
type NormalizedQueryText struct {
	text string
}

// NormalizeQueryText wraps text that was read from pg_stat_statements.query
// into a NormalizedQueryText. The caller is responsible for ensuring that text
// genuinely originates from pg_stat_statements and has not been modified by
// user-controlled input.
func NormalizeQueryText(text string) NormalizedQueryText {
	return NormalizedQueryText{text: text}
}

// String returns the underlying query text.
func (n NormalizedQueryText) String() string {
	return n.text
}
