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

package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"strings"
	"testing"
)

func lines(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q (%v)", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestParse(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "": slog.LevelInfo, "warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil || !strings.Contains(err.Error(), "debug, info, warn or error") {
		t.Errorf("ParseLevel(loud): want an error that lists the choices, got %v", err)
	}
	if f, err := ParseFormat("TEXT"); err != nil || f != FormatText {
		t.Errorf("ParseFormat(TEXT) = %v, %v", f, err)
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("ParseFormat(yaml): want an error")
	}
}

func newFlags(t *testing.T, args ...string) (*Flags, func(string) bool) {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	f := Bind(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	return f, func(n string) bool { return set[n] }
}

func TestResolveDefaults(t *testing.T) {
	f, ex := newFlags(t)
	o, notes, err := f.Resolve(ex, nil)
	if err != nil || o.Level != slog.LevelInfo || o.Format != FormatJSON || len(notes) != 0 {
		t.Fatalf("defaults: %+v %v %v", o, notes, err)
	}
}

func TestResolveLegacyZapFlags(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		legacy []Legacy
		level  slog.Level
		format Format
		note   string
	}{
		{"zap level debug", nil, []Legacy{{"zap-log-level", "debug"}}, slog.LevelDebug, FormatJSON, "use --log-level=debug"},
		{"zap level integer", nil, []Legacy{{"zap-log-level", "2"}}, slog.LevelDebug, FormatJSON, "use --log-level=debug"},
		{"zap level error", nil, []Legacy{{"zap-log-level", "error"}}, slog.LevelError, FormatJSON, "use --log-level=error"},
		{"zap console", nil, []Legacy{{"zap-encoder", "console"}}, slog.LevelInfo, FormatText, "--log-format=text"},
		{"zap devel", nil, []Legacy{{"zap-devel", "true"}}, slog.LevelDebug, FormatText, "--zap-devel is deprecated"},
		{"zap devel false is silent", nil, []Legacy{{"zap-devel", "false"}}, slog.LevelInfo, FormatJSON, ""},
		{"devel does not override explicit zap level", nil, []Legacy{{"zap-devel", "true"}, {"zap-log-level", "error"}}, slog.LevelError, FormatText, ""},
		{"explicit level wins", []string{"--log-level=warn"}, []Legacy{{"zap-log-level", "debug"}}, slog.LevelWarn, FormatJSON, "ignored because --log-level is set"},
		{"explicit format wins", []string{"--log-format=json"}, []Legacy{{"zap-encoder", "console"}}, slog.LevelInfo, FormatJSON, "ignored because --log-format is set"},
		{"stack trace flag has no effect", nil, []Legacy{{"zap-stacktrace-level", "error"}}, slog.LevelInfo, FormatJSON, "has no effect"},
		{"unknown value is ignored, not fatal", nil, []Legacy{{"zap-log-level", "loudest"}}, slog.LevelInfo, FormatJSON, "not understood"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, ex := newFlags(t, c.args...)
			o, notes, err := f.Resolve(ex, c.legacy)
			if err != nil {
				t.Fatal(err)
			}
			if o.Level != c.level || o.Format != c.format {
				t.Errorf("got %v/%v, want %v/%v", o.Level, o.Format, c.level, c.format)
			}
			if c.note != "" && !strings.Contains(strings.Join(notes, "|"), c.note) {
				t.Errorf("notes %q lack %q", notes, c.note)
			}
		})
	}
}

func TestResolveRejectsBadValues(t *testing.T) {
	f, ex := newFlags(t, "--log-level=loud")
	if _, _, err := f.Resolve(ex, nil); err == nil {
		t.Error("want an error for --log-level=loud")
	}
	f, ex = newFlags(t, "--log-format=yaml")
	if _, _, err := f.Resolve(ex, nil); err == nil {
		t.Error("want an error for --log-format=yaml")
	}
}

func TestLevelFiltersOutput(t *testing.T) {
	var b bytes.Buffer
	l := New(&b, Options{Level: slog.LevelWarn, Format: FormatJSON})
	l.Info("hidden")
	l.Debug("hidden too")
	l.Warn("shown", "k", "v")
	got := lines(t, &b)
	if len(got) != 1 || got[0]["msg"] != "shown" || got[0]["level"] != "WARN" || got[0]["k"] != "v" {
		t.Fatalf("got %v", got)
	}
}

func TestTextFormat(t *testing.T) {
	var b bytes.Buffer
	New(&b, Options{Level: slog.LevelInfo, Format: FormatText}).Info("hello", "k", "v")
	if s := b.String(); !strings.Contains(s, "msg=hello") || !strings.Contains(s, "k=v") || strings.HasPrefix(s, "{") {
		t.Fatalf("not key=value: %q", s)
	}
}

// The point of the package: the radar's logger, slog.Default() and the
// controller-runtime (logr) logger write the same kind of line.
func TestInstallRoutesEverythingThroughOneHandler(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var b bytes.Buffer
	l := New(&b, Options{Level: slog.LevelInfo, Format: FormatJSON})
	lr := Install(l)

	l.Info("from the radar", "deploy", "d1")
	slog.Error("from slog.Default", "err", "boom")
	lr.WithName("setup").Error(errors.New("kaput"), "from controller-runtime", "controller", "PostgresWatch")
	lr.V(1).Info("verbose controller-runtime line")

	got := lines(t, &b)
	if len(got) != 3 {
		t.Fatalf("want 3 lines (the V(1) line is below info), got %d: %v", len(got), got)
	}
	for _, m := range got {
		for _, k := range []string{"time", "level", "msg"} {
			if _, ok := m[k]; !ok {
				t.Errorf("line %v lacks %q", m, k)
			}
		}
	}
	cr := got[2]
	if cr["level"] != "ERROR" || cr["msg"] != "from controller-runtime" || cr["controller"] != "PostgresWatch" {
		t.Errorf("controller-runtime line: %v", cr)
	}
	if !strings.Contains(b.String(), "kaput") {
		t.Errorf("the error value was lost: %s", b.String())
	}
	if !strings.Contains(b.String(), "setup") {
		t.Errorf("the logger name was lost: %s", b.String())
	}
}

func TestDebugShowsVerboseControllerRuntimeLines(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	var b bytes.Buffer
	lr := Install(New(&b, Options{Level: slog.LevelDebug, Format: FormatJSON}))
	lr.V(1).Info("verbose controller-runtime line")
	if got := lines(t, &b); len(got) != 1 {
		t.Fatalf("want the V(1) line at debug, got %v", got)
	}
}

func TestEarly(t *testing.T) {
	var b bytes.Buffer
	Early(&b).Error("invalid logging flag", "err", "x")
	if got := lines(t, &b); len(got) != 1 || got[0]["level"] != "ERROR" {
		t.Fatalf("got %v", got)
	}
}
