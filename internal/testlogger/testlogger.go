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

// Package testlogger provides a *slog.Logger for tests that behaves like
// the io.Discard-backed logger it replaces when a test passes (no extra
// noise in ordinary `go test`/`go test -v` output), but prints everything
// the code under test logged during that test the moment the test fails.
//
// Before this package existed, every integration and envtest-based test in
// this repo built its reconciler/pipeline logger as
// slog.New(slog.NewTextHandler(io.Discard, nil)) — meaning every Info/Warn/
// Error log the application itself produced while a test ran was thrown
// away unconditionally, including on failure. A failing test's only
// diagnostic output was whatever single line the test's own t.Fatalf/
// t.Errorf happened to include, with no visibility into what the
// reconciler, collector, or ingester actually did leading up to that
// failure. New closes that gap without adding noise to the common (test
// passes) case.
//
// Security/logging convention: do not log full webhook payloads, raw query
// text, authentication headers, DSN credentials, or binary plan content via
// t.Logf or this package. Use synthetic/anonymized data in tests, or redact
// before logging.
package testlogger

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
)

// syncBuffer is a bytes.Buffer safe for concurrent writes, since the code
// under test (reconcile loops, poll loops, HTTP handlers under
// httptest.Server) frequently logs from goroutines other than the test's
// own, and bytes.Buffer alone is not safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// failLogger is the minimal subset of *testing.T that dump needs, factored
// out purely so dump's branching logic (log only when failed, skip when
// there's nothing captured) can be unit-tested against a fake in
// testlogger_test.go without needing a real, already-failed *testing.T to
// exercise every branch.
type failLogger interface {
	Failed() bool
	Log(args ...any)
}

// dump prints captured through tb.Log, prefixed with a one-line banner, but
// only when tb.Failed() and there is actually something captured — a
// passing test, or a test that failed before logging anything through this
// package's logger, prints nothing extra.
func dump(tb failLogger, captured string) {
	if !tb.Failed() || captured == "" {
		return
	}
	tb.Log("--- captured application logs (only shown because this test failed) ---")
	tb.Log(captured)
}

// build does the real work behind New, taking the minimal failLogger
// interface instead of *testing.T so testlogger_test.go can verify the
// logger-to-buffer-to-dump wiring end to end (log something, invoke the
// returned cleanup func directly, assert what got logged) against a fake
// that can be pre-set to failed/not-failed — without needing a real
// *testing.T that has actually failed, which a test binary can't easily
// construct mid-run, and without deliberately failing a real subtest
// (whose failure would propagate to and fail the parent test/package,
// which is exactly the noisy-on-every-run outcome this package exists to
// avoid).
func build(tb failLogger) (*slog.Logger, func()) {
	buf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, nil))
	cleanup := func() { dump(tb, buf.String()) }
	return logger, cleanup
}

// New returns a *slog.Logger that buffers everything logged through it and,
// via t.Cleanup, dumps that buffer through t.Log — so it lands in `go test
// -v` output right alongside the failing assertion — if and only if t has
// failed by the time the test function returns. A passing test stays
// exactly as quiet as the io.Discard-backed logger this is meant to
// replace.
//
// Safe to call once per test (or subtest — each gets its own buffer and
// cleanup, so t.Parallel() subtests don't interleave or race on a shared
// buffer).
func New(t *testing.T) *slog.Logger {
	t.Helper()
	logger, cleanup := build(t)
	t.Cleanup(cleanup)
	return logger
}
