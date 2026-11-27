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

// Package logging gives every command one log format and one level switch.
//
// Before this package the manager wrote three kinds of line to the same
// stream: JSON from the radar's own code, human-readable zap lines from
// controller-runtime, and plain "2026/01/02 15:04:05 ERROR ..." text from
// anything that fell back to slog.Default(), including start-up errors. That
// made "grep the manager log" harder than it needed to be, and gave an
// operator no way to ask for more detail. Now there is one slog handler: the
// radar's own logger, the controller-runtime logger and slog.Default() all
// write through it.
package logging

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
)

// Format is how a line is written.
type Format string

const (
	// FormatJSON writes one JSON object per line. It is the default: it is what
	// log shippers and `jq` expect.
	FormatJSON Format = "json"
	// FormatText writes `key=value` pairs, easier to read in a terminal.
	FormatText Format = "text"
)

// Flags holds the values of the logging flags of one command.
type Flags struct {
	level  string
	format string
}

// Bind registers --log-level and --log-format on fs.
func Bind(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.level, "log-level", "info",
		"Log level: debug, info, warn or error. debug adds the evaluation and scrape details "+
			"that are normally left out.")
	fs.StringVar(&f.format, "log-format", string(FormatJSON),
		"Log format: json (one object per line, the default) or text (key=value).")
	return f
}

// Legacy is the value of an old controller-runtime zap flag, kept so that an
// existing deployment that passes one still starts.
type Legacy struct {
	// Name is the flag, without the leading dashes, e.g. "zap-log-level".
	Name  string
	Value string
}

// Options is the resolved configuration.
type Options struct {
	Level  slog.Level
	Format Format
}

// Resolve turns the flag values into Options. legacy lists the deprecated
// --zap-* flags that were set on the command line; an explicit --log-level or
// --log-format always wins over them. The returned notes say what was mapped or
// ignored, for the caller to log once the logger exists.
func (f *Flags) Resolve(explicit func(name string) bool, legacy []Legacy) (Options, []string, error) {
	var notes []string
	levelStr, formatStr := f.level, f.format

	for _, l := range legacy {
		switch l.Name {
		case "zap-log-level":
			if explicit("log-level") {
				notes = append(notes, "--zap-log-level is ignored because --log-level is set")
				continue
			}
			if lv, ok := zapLevel(l.Value); ok {
				levelStr = lv
				notes = append(notes, fmt.Sprintf("--zap-log-level is deprecated, use --log-level=%s", lv))
			} else {
				notes = append(notes, fmt.Sprintf("--zap-log-level=%s is not understood and was ignored", l.Value))
			}
		case "zap-encoder":
			if explicit("log-format") {
				notes = append(notes, "--zap-encoder is ignored because --log-format is set")
				continue
			}
			switch strings.ToLower(l.Value) {
			case "console":
				formatStr = string(FormatText)
				notes = append(notes, "--zap-encoder=console is deprecated, use --log-format=text")
			case "json":
				formatStr = string(FormatJSON)
				notes = append(notes, "--zap-encoder=json is deprecated, JSON is now the default")
			default:
				notes = append(notes, fmt.Sprintf("--zap-encoder=%s is not understood and was ignored", l.Value))
			}
		case "zap-devel":
			dev, _ := strconv.ParseBool(l.Value)
			if dev {
				if !explicit("log-level") && !explicitLegacy(legacy, "zap-log-level") {
					levelStr = "debug"
				}
				if !explicit("log-format") && !explicitLegacy(legacy, "zap-encoder") {
					formatStr = string(FormatText)
				}
				notes = append(notes, "--zap-devel is deprecated, use --log-level=debug --log-format=text")
			}
		default:
			notes = append(notes, fmt.Sprintf("--%s is deprecated and has no effect", l.Name))
		}
	}

	lvl, err := ParseLevel(levelStr)
	if err != nil {
		return Options{}, nil, err
	}
	format, err := ParseFormat(formatStr)
	if err != nil {
		return Options{}, nil, err
	}
	return Options{Level: lvl, Format: format}, notes, nil
}

func explicitLegacy(legacy []Legacy, name string) bool {
	for _, l := range legacy {
		if l.Name == name {
			return true
		}
	}
	return false
}

// zapLevel maps a --zap-log-level value to one of ours. zap takes debug, info,
// error, or an integer (a higher number is more verbose).
func zapLevel(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return "debug", true
	case "info":
		return "info", true
	case "error", "panic", "dpanic":
		return "error", true
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return "debug", true
	}
	return "", false
}

// ParseLevel accepts debug, info, warn (or warning) and error.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid --log-level %q: use debug, info, warn or error", s)
}

// ParseFormat accepts json and text.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(s))) {
	case FormatJSON, "":
		return FormatJSON, nil
	case FormatText:
		return FormatText, nil
	}
	return "", fmt.Errorf("invalid --log-format %q: use json or text", s)
}

// New builds the logger for o, writing to out.
func New(out io.Writer, o Options) *slog.Logger {
	return slog.New(handler(out, o))
}

func handler(out io.Writer, o Options) slog.Handler {
	ho := &slog.HandlerOptions{Level: o.Level}
	if o.Format == FormatText {
		return slog.NewTextHandler(out, ho)
	}
	return slog.NewJSONHandler(out, ho)
}

// Install makes l the process-wide slog default, so that a component built
// without a logger (and every start-up error) writes through the same handler,
// and returns the logr.Logger to hand to controller-runtime.
func Install(l *slog.Logger) logr.Logger {
	slog.SetDefault(l)
	return logr.FromSlogHandler(l.Handler())
}

// Early returns a JSON logger at info level on out, for the lines written before
// the flags are parsed (and for a flag that does not parse).
func Early(out io.Writer) *slog.Logger {
	return New(out, Options{Level: slog.LevelInfo, Format: FormatJSON})
}
