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
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// repoRoot is the module root, two levels above this package.
func repoRoot(t testing.TB) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("not the module root: %v", err)
	}
	return root
}

// goFiles lists the non-test Go files of the radar itself.
func goFiles(t testing.TB) []string {
	t.Helper()
	root := repoRoot(t)
	var out []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				switch info.Name() {
				case "testlogger", "testutil", "e2e", "playground", "docscheck":
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(out)
	return out
}

func parse(t testing.TB, fset *token.FileSet, path string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return exprText(v.Fun) + "()"
	case *ast.StarExpr:
		return "*" + exprText(v.X)
	case *ast.ParenExpr:
		return exprText(v.X)
	}
	return ""
}

// literal evaluates a string expression made of literals joined by "+". ok is
// false when it holds nothing literal at all; complete is false when it also
// holds something that is not a literal (the text is then the leading part).
func literal(e ast.Expr) (text string, complete, ok bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false, false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false, false
		}
		return s, true, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false, false
		}
		l, lc, lok := literal(v.X)
		if !lok {
			return "", false, false
		}
		if !lc {
			return l, false, true
		}
		r, rc, rok := literal(v.Y)
		if !rok {
			return l, false, true
		}
		return l + r, rc, true
	case *ast.ParenExpr:
		return literal(v.X)
	}
	return "", false, false
}

// LogMessage is one log call in the source.
type LogMessage struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Level    string   `json:"level"`
	Text     string   `json:"text"`
	Complete bool     `json:"complete"` // false: the message is built at run time and Text is its fixed beginning
	Keys     []string `json:"keys"`
}

// logMessages finds every call that writes a log line: a method named Info,
// Warn, Error or Debug on something whose name says it is a logger, whose
// message is a string literal (the second argument when the first is an error,
// as in logr's Error(err, msg, ...)).
func logMessages(t testing.TB) []LogMessage {
	t.Helper()
	root := repoRoot(t)
	var out []LogMessage
	fset := token.NewFileSet()
	for _, path := range goFiles(t) {
		f := parse(t, fset, path)
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := c.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Info", "Warn", "Error", "Debug":
			default:
				return true
			}
			if recv := strings.ToLower(exprText(sel.X)); !strings.Contains(recv, "log") {
				return true
			}
			args := c.Args
			idx := 0
			if _, _, isLit := literal(args[0]); !isLit && sel.Sel.Name == "Error" && len(args) > 1 {
				idx = 1
			}
			if idx >= len(args) {
				return true
			}
			text, complete, ok := literal(args[idx])
			if !ok {
				return true
			}
			var keys []string
			if idx == 1 {
				keys = append(keys, "err") // logr's Error(err, msg, ...) adds the error as "err"
			}
			for i := idx + 1; i+1 < len(args); i += 2 {
				if k, _, ok := literal(args[i]); ok {
					keys = append(keys, k)
				}
			}
			rel, _ := filepath.Rel(root, path)
			out = append(out, LogMessage{File: rel, Line: fset.Position(c.Pos()).Line, Level: strings.ToLower(sel.Sel.Name),
				Text: text, Complete: complete, Keys: keys})
			return true
		})
	}
	return out
}

// Metric is one Prometheus metric defined in the source.
type Metric struct {
	File   string   `json:"file"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Help   string   `json:"help"`
	Labels []string `json:"labels"`
}

// metrics finds every prometheus.*Opts literal with a literal name.
func metrics(t testing.TB) []Metric {
	t.Helper()
	root := repoRoot(t)
	var out []Metric
	fset := token.NewFileSet()
	for _, path := range goFiles(t) {
		f := parse(t, fset, path)
		ast.Inspect(f, func(n ast.Node) bool {
			// A metric is made by passing an Opts literal to a constructor (or to a
			// helper that wraps one), with the label names, if any, right after it.
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			optsAt, typ := -1, ""
			for i, a := range call.Args {
				if cl, ok := a.(*ast.CompositeLit); ok {
					switch tn := exprText(cl.Type); {
					case strings.HasSuffix(tn, "CounterOpts"), strings.HasSuffix(tn, "GaugeOpts"),
						strings.HasSuffix(tn, "HistogramOpts"), strings.HasSuffix(tn, "SummaryOpts"):
						optsAt, typ = i, tn
					}
				}
			}
			if optsAt < 0 {
				return true
			}
			lit := call.Args[optsAt].(*ast.CompositeLit)
			var ns, sub, name, help string
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				v, _, ok := literal(kv.Value)
				if !ok {
					continue
				}
				switch exprText(kv.Key) {
				case "Namespace":
					ns = v
				case "Subsystem":
					sub = v
				case "Name":
					name = v
				case "Help":
					help = v
				}
			}
			if name == "" {
				return true
			}
			full := strings.Join(nonEmpty(ns, sub, name), "_")
			var labels []string
			if optsAt+1 < len(call.Args) {
				if cl, ok := call.Args[optsAt+1].(*ast.CompositeLit); ok {
					for _, e := range cl.Elts {
						if l, _, ok := literal(e); ok {
							labels = append(labels, l)
						}
					}
				}
			}
			kind := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(typ, "prometheus."), "Opts"))
			rel, _ := filepath.Rel(root, path)
			out = append(out, Metric{File: rel, Name: full, Kind: kind, Help: help, Labels: labels})
			return true
		})
	}
	return out
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, x := range s {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// TestDump prints what the scanners found, as JSON, for whoever is writing the
// docs: DOCSCHECK_DUMP=logs|metrics go test ./internal/docscheck -run TestDump -v
func TestDump(t *testing.T) {
	what := os.Getenv("DOCSCHECK_DUMP")
	if what == "" {
		t.Skip("set DOCSCHECK_DUMP=logs or metrics")
	}
	var v any
	switch what {
	case "logs":
		v = logMessages(t)
	case "metrics":
		v = metrics(t)
	default:
		t.Fatalf("unknown DOCSCHECK_DUMP=%s", what)
	}
	b, _ := json.MarshalIndent(v, "", " ")
	fmt.Println(string(b))
}
