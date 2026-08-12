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

package collector

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// FingerprintQuery derives a stable identifier for the *shape* of a SQL
// statement, independent of literal values, whitespace, comments, and casing.
//
// Why this exists: per the PostgreSQL documentation for pg_stat_statements,
// "it is not safe to assume that queryid will be stable across major versions
// of PostgreSQL" and queryid additionally changes "if they reference for
// example a function that was dropped and recreated between the executions
// of the two queries" (https://www.postgresql.org/docs/current/pgstatstatements.html,
// section F.32.1, paragraph on queryid stability guarantees). A CloudNativePG
// rolling deploy that ships a migration (DROP/CREATE on a referenced object)
// can therefore change queryid for a query whose *text* never changed, right
// in the middle of the pre/post deploy analysis window. FingerprintQuery lets
// the collector recognize "this is the same query under a new queryid" so the
// correlation engine doesn't see a false "no data" gap.
//
// Design notes (researched, not guessed):
//
//   - pg_stat_statements itself does NOT fingerprint by re-parsing the query
//     text; it computes queryid from the post-parse-analysis tree during
//     planning (see contrib/pg_stat_statements/pg_stat_statements.c,
//     JumbleQuery()/JumbleExpr() in the PostgreSQL source, and the "Query
//     Normalization" note in the docs above). That's precisely why it is
//     sensitive to catalog OIDs and can't be used as a cross-version stable
//     key. We instead fingerprint the *displayed* query text the same way
//     tools like Percona Toolkit's pt-fingerprint do: textual normalization,
//     not AST hashing. This is a deliberate, cheaper trade-off: it can be
//     fooled by semantically-different queries that render identically after
//     normalization (rare) but does not require a SQL parser dependency.
//
//   - Known textual-normalization pitfalls (verified against the Postgres
//     lexical-structure docs, https://www.postgresql.org/docs/current/sql-syntax-lexical.html):
//
//     1. "--" starts a line comment, but only outside of string/identifier
//     literals; a naive regexp run over the whole text would truncate a
//     string literal that happens to contain "--".
//     2. "/* ... */" block comments nest in PostgreSQL ("SQL comment syntax
//     additionally allows comments to be nested" - lexical structure
//     docs, 4.1.5). A regexp using a non-greedy match on the first "*/"
//     mishandles nested comments.
//     3. Single-quoted string literals escape an embedded quote character by
//     writing it twice in a row (as in the SQL text "it" plus a
//     quote-quote pair plus "s", meaning "it's"), not (only) with a
//     backslash. Postgres only treats backslash as an escape character
//     inside "escape string syntax" (E-prefixed literals), not in ordinary
//     standard-conforming strings (on by default since 9.1) - see section
//     4.1.2.2, "String Constants with C-style Escapes", in the lexical
//     structure docs. Blindly special-casing backslash for every quoted
//     string causes the scanner to run past the real closing quote when
//     standard_conforming_strings is on.
//     4. Dollar-quoted strings ($$...$$ or $tag$...$tag$) don't use quote
//     characters at all and can appear in captured query text (e.g.
//     function bodies); they must be recognized so their content isn't
//     misinterpreted as SQL tokens.
//     5. Double-quoted identifiers must be preserved (not normalized/
//     lowercased) since PostgreSQL identifiers are case-sensitive when
//     quoted; but the scanner still has to walk over them correctly (with
//     their own doubled-quote escaping) so a stray '/*' or "'" inside one
//     doesn't desynchronize the rest of the scan.
//
//     For these reasons the normalizer is a small hand-written single-pass
//     scanner (see stripCommentsAndLiterals below) rather than a chain of
//     regexes applied to raw text, which is the same pitfall pt-fingerprint's
//     own documentation warns about for comment/string handling.
//
//   - Hash choice: FNV-1a 64-bit, not SHA-256. This is an internal grouping
//     key, not a security boundary - nothing untrusted is trying to engineer
//     a collision, we just need a fast, well-distributed digest to use as a
//     map key. FNV-1a is a single non-cryptographic pass with a fixed 8-byte
//     state (vs. SHA-256's much larger internal state and multiple rounds
//     per block) and is what several observability tools (and pg_stat_statements'
//     own queryid, which is also a non-cryptographic hash of the jumbled
//     query tree) use for exactly this kind of purpose. Even truncated,
//     SHA-256 costs meaningfully more CPU per scrape for a property (
//     cryptographic collision resistance) this use case does not need.
func FingerprintQuery(sql string) string {
	normalized := normalizeQueryText(sql)
	h := fnv.New64a()
	_, _ = h.Write([]byte(normalized))
	return strconv.FormatUint(h.Sum64(), 16)
}

// placeholder replaces any literal value (string, numeric, or an existing
// pg_stat_statements "$N" parameter marker) in the normalized text.
const placeholder = "?"

var (
	// dollarParamRe matches pg_stat_statements' own "$1", "$2", ... markers.
	// These are re-normalized to a single placeholder because their numbering
	// is not guaranteed to line up between two otherwise-identical queries:
	// per the docs, "there may be hidden parameter symbols that affect this
	// numbering" (e.g. PL/pgSQL substituting local variables), so two
	// call sites of the same statement shape can legitimately show up as
	// "$1" vs "$2" for the first literal.
	dollarParamRe = regexp.MustCompile(`\$\d+`)

	// numericLiteralRe matches integer/decimal/exponent numeric literals as
	// stand-alone tokens. \b prevents it from matching digits embedded in an
	// identifier (e.g. the "1" in "table1"), because \b only fires at a
	// transition between a word character and a non-word character, and
	// letters/digits are both word characters.
	numericLiteralRe = regexp.MustCompile(`\b\d+(\.\d+)?([eE][+-]?\d+)?\b`)

	// whitespaceRe collapses any run of whitespace (including newlines left
	// behind by stripped comments) into a single space.
	whitespaceRe = regexp.MustCompile(`\s+`)

	// placeholderListRe collapses a run of two-or-more placeholders (e.g. from
	// an "IN (1, 2, 3)" list) into a single placeholder, so that the same
	// query executed with a different number of IN-list elements fingerprints
	// identically. PostgreSQL 14+ already does something similar for its own
	// queryid/query text ("the list will get squashed down to a single
	// element", pg_stat_statements docs); this closes the same gap for
	// versions/paths where that squashing didn't happen or the queryid still
	// differs across the elided run.
	placeholderListRe = regexp.MustCompile(`(?:\?\s*,\s*)+\?`)

	// punctuationSpacingRe removes whitespace immediately adjacent to
	// punctuation/operator characters, so that "id = 99" and "id=99" (or
	// "a,b" and "a, b") normalize identically. This is intentionally applied
	// after literals/comments have already been stripped, so it can never
	// alter the contents of a string; it only affects formatting-only
	// differences in the surrounding SQL, which is exactly what a
	// fingerprint should be insensitive to.
	punctuationSpacingRe = regexp.MustCompile(`\s*([(),;=<>!])\s*`)
)

// normalizeQueryText lowercases, strips comments, collapses literals to a
// single placeholder token, and collapses whitespace, so that two queries
// that differ only in formatting/casing/literal values normalize identically.
func normalizeQueryText(sql string) string {
	cleaned := stripCommentsAndLiterals(sql)
	cleaned = strings.ToLower(cleaned)
	cleaned = dollarParamRe.ReplaceAllString(cleaned, placeholder)
	cleaned = numericLiteralRe.ReplaceAllString(cleaned, placeholder)
	cleaned = placeholderListRe.ReplaceAllString(cleaned, placeholder)
	cleaned = whitespaceRe.ReplaceAllString(cleaned, " ")
	cleaned = punctuationSpacingRe.ReplaceAllString(cleaned, "$1")
	return strings.TrimSpace(cleaned)
}

// isIdentChar reports whether r can appear inside an unquoted SQL identifier
// or keyword; used to decide whether a preceding "e"/"E" is a standalone
// escape-string prefix (E'...') or just the tail of a longer identifier.
func isIdentChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// stripCommentsAndLiterals performs a single left-to-right scan of sql and
// returns a copy with:
//   - line comments ("-- ...") and block comments ("/* ... */", nesting
//     supported) removed (replaced by a single space so tokens don't fuse),
//   - single-quoted string literals and dollar-quoted string literals
//     replaced by a single placeholder token,
//   - double-quoted identifiers copied through verbatim (case preserved,
//     internal doubled-quote escaping respected) since they are not literals,
//
// A single pass (rather than sequential regexps for comments, then strings,
// then numbers) is required so that a "--" or "/*" inside a string literal,
// or a quote character inside a comment, is never misinterpreted - each
// character is only ever considered in the context the scanner currently
// believes it is in.
func stripCommentsAndLiterals(s string) string {
	r := []rune(s)
	n := len(r)
	var b strings.Builder
	b.Grow(n)

	for i := 0; i < n; {
		c := r[i]
		switch {
		case c == '-' && i+1 < n && r[i+1] == '-':
			i += 2
			for i < n && r[i] != '\n' {
				i++
			}
			b.WriteByte(' ')

		case c == '/' && i+1 < n && r[i+1] == '*':
			depth := 1
			i += 2
			for i < n && depth > 0 {
				switch {
				case r[i] == '/' && i+1 < n && r[i+1] == '*':
					depth++
					i += 2
				case r[i] == '*' && i+1 < n && r[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
			b.WriteByte(' ')

		case c == '\'':
			// Standard string literal. Only honor backslash-escaping if this
			// literal is prefixed by a standalone "E"/"e" (escape string
			// syntax); otherwise, per standard_conforming_strings semantics
			// (the default since PostgreSQL 9.1), backslash has no special
			// meaning and must not be treated as escaping the next quote.
			escapeSyntax := i > 0 && (r[i-1] == 'E' || r[i-1] == 'e') &&
				(i < 2 || !isIdentChar(r[i-2]))
			i++
			for i < n {
				if r[i] == '\'' {
					if i+1 < n && r[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				if escapeSyntax && r[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			b.WriteString(placeholder)

		case c == '"':
			b.WriteRune(c)
			i++
			for i < n {
				b.WriteRune(r[i])
				if r[i] == '"' {
					i++
					if i < n && r[i] == '"' {
						b.WriteRune(r[i])
						i++
						continue
					}
					break
				}
				i++
			}

		case c == '$':
			if tag, tagEnd, ok := dollarQuoteTag(r, i); ok {
				closer := []rune("$" + tag + "$")
				end := findDollarQuoteEnd(r, tagEnd, closer)
				if end >= 0 {
					i = end
					b.WriteString(placeholder)
					continue
				}
			}
			b.WriteRune(c)
			i++

		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}

// dollarQuoteTag checks whether sql[i:] begins a dollar-quote opening
// delimiter ("$$" or "$tag$", tag being letters/digits/underscore) and, if
// so, returns the tag and the index right after the opening delimiter.
func dollarQuoteTag(r []rune, i int) (tag string, tagEnd int, ok bool) {
	n := len(r)
	if i >= n || r[i] != '$' {
		return "", 0, false
	}
	j := i + 1
	start := j
	for j < n && isIdentChar(r[j]) {
		j++
	}
	if j < n && r[j] == '$' {
		return string(r[start:j]), j + 1, true
	}
	return "", 0, false
}

// findDollarQuoteEnd returns the index right after the first occurrence of
// closer at or after from, or -1 if not found (unterminated dollar-quote;
// the caller falls back to treating "$" as an ordinary character).
func findDollarQuoteEnd(r []rune, from int, closer []rune) int {
	n, m := len(r), len(closer)
	for i := from; i+m <= n; i++ {
		match := true
		for k := 0; k < m; k++ {
			if r[i+k] != closer[k] {
				match = false
				break
			}
		}
		if match {
			return i + m
		}
	}
	return -1
}
