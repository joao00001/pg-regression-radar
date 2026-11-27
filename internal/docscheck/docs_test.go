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

package docscheck

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/reason"
)

func readDoc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "troubleshooting", name))
	if err != nil {
		t.Fatalf("the troubleshooting docs must have %s: %v", name, err)
	}
	return string(b)
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func diff(a, b map[string]bool) (onlyA, onlyB []string) {
	for k := range a {
		if !b[k] {
			onlyA = append(onlyA, k)
		}
	}
	for k := range b {
		if !a[k] {
			onlyB = append(onlyB, k)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return
}

var entryHeading = regexp.MustCompile("(?m)^### `(\\w+)`\\s*$")

// sections splits reasons.md into the body of each "### `Code`" entry.
func sections(doc string) map[string]string {
	locs := entryHeading.FindAllStringSubmatchIndex(doc, -1)
	out := map[string]string{}
	for i, l := range locs {
		end := len(doc)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		// an entry ends at the next "## " heading too
		body := doc[l[1]:end]
		if j := strings.Index(body, "\n## "); j >= 0 {
			body = body[:j]
		}
		out[doc[l[2]:l[3]]] = body
	}
	return out
}

// Every reason code has an entry, every entry has a code, and the entry says
// what the registry says.
func TestEveryReasonIsDocumented(t *testing.T) {
	doc := readDoc(t, "reasons.md")
	secs := sections(doc)

	have := map[string]bool{}
	for c := range secs {
		have[c] = true
	}
	want := map[string]bool{}
	for _, e := range reason.All() {
		want[string(e.Code)] = true
	}
	missing, stale := diff(want, have)
	if len(missing) > 0 {
		t.Errorf("reason codes with no entry in docs/troubleshooting/reasons.md: %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("entries in reasons.md for codes that no longer exist: %v", stale)
	}

	for _, e := range reason.All() {
		body, ok := secs[string(e.Code)]
		if !ok {
			continue
		}
		if !strings.Contains(body, e.Summary) {
			t.Errorf("%s: the entry must open with the registry's summary, word for word:\n  %s", e.Code, e.Summary)
		}
		if !strings.Contains(body, "**Where you see it.**") {
			t.Errorf("%s: say where it shows up (**Where you see it.**)", e.Code)
		}
		if strings.Contains(string(e.Code), "(") {
			continue
		}
		if e.Severity == reason.Warn || e.Severity == reason.Error {
			for _, must := range []string{"**Causes.**", "*Confirm:*", "*Fix:*"} {
				if !strings.Contains(body, must) {
					t.Errorf("%s is a %s: its entry needs %s (causes, how to confirm each, the fix)", e.Code, e.Severity, must)
				}
			}
		} else if !strings.Contains(body, "**What it means.**") && !strings.Contains(body, "**Causes.**") {
			t.Errorf("%s: say what it means (**What it means.**) or list its causes", e.Code)
		}
		if !strings.Contains(doc, "](#"+reason.Anchor(e.Code)+")") {
			t.Errorf("%s is missing from the index at the top of the page", e.Code)
		}
	}
}

// A code that no log line or condition uses is a promise the radar does not keep.
func TestEveryReasonIsUsed(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	// constant name -> code, read from the registry's own const block
	f := parse(t, fset, filepath.Join(root, "internal", "reason", "reason.go"))
	names := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
			return true
		}
		if v, _, ok := literal(vs.Values[0]); ok && vs.Type != nil && exprText(vs.Type) == "Code" {
			names[vs.Names[0].Name] = v
		}
		return true
	})
	if len(names) == 0 {
		t.Fatal("found no reason constants; the scanner is broken")
	}

	used := map[string]bool{}
	for _, path := range goFiles(t) {
		if strings.HasSuffix(path, filepath.Join("reason", "reason.go")) {
			continue
		}
		file := parse(t, fset, path)
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || exprText(sel.X) != "reason" {
				return true
			}
			if code, ok := names[sel.Sel.Name]; ok {
				used[code] = true
			}
			return true
		})
	}
	for _, e := range reason.All() {
		if !used[string(e.Code)] {
			t.Errorf("reason %s is in the registry but no log line or condition uses it", e.Code)
		}
	}
	// and every constant is in the registry
	inRegistry := map[string]bool{}
	for _, e := range reason.All() {
		inRegistry[string(e.Code)] = true
	}
	for name, code := range names {
		if !inRegistry[code] {
			t.Errorf("constant %s (%q) is not in the registry table, so it has no summary and no docs entry", name, code)
		}
	}
}

var catalogRow = regexp.MustCompile("^\\|\\s*(debug|info|warn|error)\\s*\\|\\s*`([^`]+)`\\s*\\|([^|]*)\\|(.*)\\|\\s*$")

type catalogEntry struct {
	level, msg string
	fields     map[string]bool
	meaning    string
	partial    bool
}

func readCatalog(t *testing.T) []catalogEntry {
	t.Helper()
	var out []catalogEntry
	for _, line := range strings.Split(readDoc(t, "log-catalog.md"), "\n") {
		m := catalogRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		e := catalogEntry{level: m[1], msg: m[2], fields: map[string]bool{}, meaning: strings.TrimSpace(m[4])}
		if strings.HasSuffix(e.msg, "…") {
			e.partial = true
			e.msg = strings.TrimSpace(strings.TrimSuffix(e.msg, "…"))
		}
		for _, f := range regexp.MustCompile("`([A-Za-z][\\w.-]*)`").FindAllStringSubmatch(m[3], -1) {
			e.fields[f[1]] = true
		}
		out = append(out, e)
	}
	return out
}

// Every message the radar logs is in the catalog with its level and its fields,
// and every catalog row is a message the radar still logs.
func TestEveryLogMessageIsInTheCatalog(t *testing.T) {
	code := logMessages(t)
	if len(code) < 100 {
		t.Fatalf("found only %d log calls; the scanner is broken", len(code))
	}
	type key struct{ level, msg string }
	inCode := map[key]map[string]bool{}
	for _, m := range code {
		text := m.Text
		if !m.Complete {
			text = strings.TrimSpace(text)
		}
		k := key{m.Level, text}
		if inCode[k] == nil {
			inCode[k] = map[string]bool{}
		}
		for _, f := range m.Keys {
			inCode[k][f] = true
		}
	}

	docRows := map[key]catalogEntry{}
	for _, e := range readCatalog(t) {
		if len(e.meaning) < 12 {
			t.Errorf("catalog row %q: say what it means and what to do", e.msg)
		}
		docRows[key{e.level, e.msg}] = e
	}

	for k, fields := range inCode {
		row, ok := docRows[k]
		if !ok {
			t.Errorf("log message not in docs/troubleshooting/log-catalog.md: %s %q", k.level, k.msg)
			continue
		}
		onlyCode, onlyDoc := diff(fields, row.fields)
		if len(onlyCode) > 0 {
			t.Errorf("%q: fields the code logs and the catalog does not list: %v", k.msg, onlyCode)
		}
		if len(onlyDoc) > 0 {
			t.Errorf("%q: fields the catalog lists and the code does not log: %v", k.msg, onlyDoc)
		}
	}
	for k := range docRows {
		if _, ok := inCode[k]; !ok {
			t.Errorf("catalog row for a message that is no longer logged: %s %q", k.level, k.msg)
		}
	}
}

var metricRow = regexp.MustCompile("^\\|\\s*`(pg_regression_radar_\\w+)`\\s*\\|\\s*(\\w+)\\s*\\|([^|]*)\\|(.*)\\|\\s*$")

func TestEveryMetricIsDocumented(t *testing.T) {
	code := map[string]Metric{}
	for _, m := range metrics(t) {
		code[m.Name] = m
	}
	if len(code) < 15 {
		t.Fatalf("found only %d metrics; the scanner is broken", len(code))
	}
	doc := map[string]struct {
		kind   string
		labels map[string]bool
		text   string
	}{}
	for _, line := range strings.Split(readDoc(t, "metrics.md"), "\n") {
		m := metricRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		labels := map[string]bool{}
		for _, l := range regexp.MustCompile("`(\\w+)`").FindAllStringSubmatch(m[3], -1) {
			labels[l[1]] = true
		}
		doc[m[1]] = struct {
			kind   string
			labels map[string]bool
			text   string
		}{m[2], labels, strings.TrimSpace(m[4])}
	}
	for name, m := range code {
		d, ok := doc[name]
		if !ok {
			t.Errorf("metric not in docs/troubleshooting/metrics.md: %s", name)
			continue
		}
		if d.kind != m.Kind {
			t.Errorf("%s: the docs say %s, the code says %s", name, d.kind, m.Kind)
		}
		want := map[string]bool{}
		for _, l := range m.Labels {
			want[l] = true
		}
		onlyCode, onlyDoc := diff(want, d.labels)
		if len(onlyCode)+len(onlyDoc) > 0 {
			t.Errorf("%s: labels differ; only in code %v, only in docs %v", name, onlyCode, onlyDoc)
		}
		if len(d.text) < 12 {
			t.Errorf("%s: say what it measures and when to worry", name)
		}
	}
	for name := range doc {
		if _, ok := code[name]; !ok {
			t.Errorf("documented metric that the code does not define: %s", name)
		}
	}
	_ = keysOf[bool]
}
