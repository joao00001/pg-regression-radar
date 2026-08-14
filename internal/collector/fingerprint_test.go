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

// FingerprintQuery itself (the exported entry point) already has black-box
// coverage in collector_test.go (TestFingerprintQuery_*): literal/whitespace/
// case normalization, comment handling, escaped/doubled quotes, IN-list
// collapsing, and structurally-different queries diverging. This file
// covers what that black-box coverage can't reach directly: the unexported
// scanner helpers stripCommentsAndLiterals/isIdentChar/dollarQuoteTag/
// findDollarQuoteEnd, exercised white-box against the specific pitfalls
// documented in stripCommentsAndLiterals's own doc comment.
package collector

import "testing"

// TestStripCommentsAndLiterals covers the documented pitfalls in
// stripCommentsAndLiterals's own doc comment one at a time: each is a real
// bug class a naive sequence of regexps (rather than this single left-to-
// right scanner) would get wrong.
func TestStripCommentsAndLiterals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// The '\n' terminating the "--" comment is the loop's stopping
			// condition, not something it consumes: the scanner writes one
			// space for the comment, then the outer loop's default case
			// copies the '\n' itself through verbatim on the next
			// iteration — so both the replacement space AND the original
			// newline appear in the output.
			name: "line comment containing a string-literal-looking dash pair is still just a comment",
			in:   "SELECT 1 -- this -- is a comment\nFROM t",
			want: "SELECT 1  \nFROM t",
		},
		{
			name: "block comment nests",
			in:   "SELECT /* outer /* inner */ still commented */ 1",
			want: "SELECT   1",
		},
		{
			name: "standard string literal doubles an embedded quote, not backslash-escapes it",
			in:   "SELECT 'it''s fine'",
			want: "SELECT ?",
		},
		{
			name: "a '--' inside a string literal is not treated as a comment",
			in:   "SELECT 'a--b'",
			want: "SELECT ?",
		},
		{
			// The leading "E" is copied through by the scanner's default
			// case before it ever reaches the quote character; only the
			// quoted part itself becomes the placeholder.
			name: "a standalone E-prefixed literal DOES honor backslash escaping",
			in:   `SELECT E'a\'b'`,
			want: "SELECT E?",
		},
		{
			name: "a non-E identifier ending in e right before a quote is NOT escape syntax",
			in:   "SELECT some_table.e'x'",
			want: "SELECT some_table.e?",
		},
		{
			name: "double-quoted identifier is preserved verbatim, including doubled-quote escaping",
			in:   `SELECT "Weird""Column" FROM t`,
			want: `SELECT "Weird""Column" FROM t`,
		},
		{
			name: "dollar-quoted string with an explicit tag is replaced by the placeholder",
			in:   "SELECT $tag$hello -- not a comment ' not a string$tag$ FROM t",
			want: "SELECT ? FROM t",
		},
		{
			name: "bare $$ dollar-quoted string",
			in:   "SELECT $$hello world$$",
			want: "SELECT ?",
		},
		{
			name: "unterminated dollar-quote falls back to a literal '$'",
			in:   "SELECT $tag$hello",
			want: "SELECT $tag$hello",
		},
		{
			name: "a lone '$' not starting a valid dollar-quote is passed through",
			in:   "SELECT price$ FROM t",
			want: "SELECT price$ FROM t",
		},
	}
	for _, c := range cases {
		if got := stripCommentsAndLiterals(c.in); got != c.want {
			t.Errorf("%s:\n  stripCommentsAndLiterals(%q)\n  =    %q\n  want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestIsIdentChar(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{'a', 'Z', '0', '9', '_'} {
		if !isIdentChar(r) {
			t.Errorf("isIdentChar(%q) = false, want true", r)
		}
	}
	for _, r := range []rune{' ', '\'', '$', '-', '.', '('} {
		if isIdentChar(r) {
			t.Errorf("isIdentChar(%q) = true, want false", r)
		}
	}
}

func TestDollarQuoteTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		sql        string
		i          int
		wantTag    string
		wantTagEnd int
		wantOK     bool
	}{
		{"bare $$", "$$body$$", 0, "", 2, true},
		{"tagged $foo$", "$foo$body$foo$", 0, "foo", 5, true},
		{"tagged with digits/underscore", "$a_1$body$a_1$", 0, "a_1", 5, true},
		{"not a dollar at i", "xyz", 0, "", 0, false},
		{"unterminated tag (no closing $)", "$foo bar", 0, "", 0, false},
		{"i out of range", "$$", 5, "", 0, false},
	}
	for _, c := range cases {
		gotTag, gotTagEnd, gotOK := dollarQuoteTag([]rune(c.sql), c.i)
		if gotTag != c.wantTag || gotTagEnd != c.wantTagEnd || gotOK != c.wantOK {
			t.Errorf("%s: dollarQuoteTag(%q, %d) = (%q, %d, %v), want (%q, %d, %v)",
				c.name, c.sql, c.i, gotTag, gotTagEnd, gotOK, c.wantTag, c.wantTagEnd, c.wantOK)
		}
	}
}

func TestFindDollarQuoteEnd(t *testing.T) {
	t.Parallel()
	r := []rune("hello$$ world$$ trailing")
	closer := []rune("$$")

	// The first "$$" closer starting the search from index 0 is found
	// immediately at the run beginning at index 5; findDollarQuoteEnd
	// returns the index right after it.
	if got, want := findDollarQuoteEnd(r, 0, closer), 7; got != want {
		t.Errorf("findDollarQuoteEnd(from=0) = %d, want %d", got, want)
	}

	// Searching from just after the first closer finds the second one.
	if got, want := findDollarQuoteEnd(r, 7, closer), 15; got != want {
		t.Errorf("findDollarQuoteEnd(from=7) = %d, want %d", got, want)
	}

	// No closer at all past this point.
	if got := findDollarQuoteEnd(r, 16, closer); got != -1 {
		t.Errorf("findDollarQuoteEnd(from=16) = %d, want -1 (no closer found)", got)
	}
}
