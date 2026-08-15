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

package testlogger

import (
	"strings"
	"sync"
	"testing"
)

// fakeFailLogger is a minimal, directly-inspectable stand-in for
// *testing.T's Failed()/Log() so dump's branching can be exercised for
// every combination (failed/not failed, empty/non-empty capture) without
// needing an already-failed real *testing.T, which the test binary itself
// can't easily construct mid-run.
type fakeFailLogger struct {
	mu     sync.Mutex
	failed bool
	logs   []string
}

func (f *fakeFailLogger) Failed() bool { return f.failed }

func (f *fakeFailLogger) Log(args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range args {
		if s, ok := a.(string); ok {
			f.logs = append(f.logs, s)
		}
	}
}

func TestDump_PassingTest_LogsNothing(t *testing.T) {
	fake := &fakeFailLogger{failed: false}
	dump(fake, "some captured application log line")
	if len(fake.logs) != 0 {
		t.Errorf("expected no t.Log calls for a passing test, got %v", fake.logs)
	}
}

func TestDump_FailedTest_NothingCaptured_LogsNothing(t *testing.T) {
	fake := &fakeFailLogger{failed: true}
	dump(fake, "")
	if len(fake.logs) != 0 {
		t.Errorf("expected no t.Log calls when nothing was captured, got %v", fake.logs)
	}
}

func TestDump_FailedTest_WithCapturedLogs_DumpsThem(t *testing.T) {
	fake := &fakeFailLogger{failed: true}
	dump(fake, "level=INFO msg=\"reconcile started\" watch=watch-x")

	if len(fake.logs) != 2 {
		t.Fatalf("expected exactly 2 t.Log calls (banner + captured content), got %d: %v", len(fake.logs), fake.logs)
	}
	if !strings.Contains(fake.logs[0], "captured application logs") {
		t.Errorf("expected the first Log call to be the banner, got %q", fake.logs[0])
	}
	if !strings.Contains(fake.logs[1], "reconcile started") {
		t.Errorf("expected the second Log call to contain the captured content, got %q", fake.logs[1])
	}
}

// TestBuild_LoggerWritesReachTheCleanupDump verifies the real wiring behind
// New end to end — that what's logged through the returned *slog.Logger is
// exactly what the returned cleanup func later dumps via tb.Log — without
// needing a real, already-failed *testing.T (see build's doc comment for
// why deliberately failing a real subtest to test this would itself fail
// this package's test run every time).
func TestBuild_LoggerWritesReachTheCleanupDump(t *testing.T) {
	fake := &fakeFailLogger{failed: true}
	logger, cleanup := build(fake)

	logger.Info("reconcile started", "watch", "watch-x")
	logger.Error("secret consent check failed", "secret", "default/remote-dsn")

	cleanup()

	if len(fake.logs) != 2 {
		t.Fatalf("expected exactly 2 t.Log calls (banner + captured content), got %d: %v", len(fake.logs), fake.logs)
	}
	if !strings.Contains(fake.logs[1], "reconcile started") || !strings.Contains(fake.logs[1], "secret consent check failed") {
		t.Errorf("expected the dumped content to contain both log lines actually written through the logger, got %q", fake.logs[1])
	}
}

// TestBuild_PassingTest_CleanupLogsNothing mirrors
// TestBuild_LoggerWritesReachTheCleanupDump but for the common case: even
// with real content logged, a not-failed tb must not have anything dumped
// to it — the whole point of this package is silence on success.
func TestBuild_PassingTest_CleanupLogsNothing(t *testing.T) {
	fake := &fakeFailLogger{failed: false}
	logger, cleanup := build(fake)

	logger.Info("this must never be printed for a passing test")
	cleanup()

	if len(fake.logs) != 0 {
		t.Errorf("expected no t.Log calls for a passing test, got %v", fake.logs)
	}
}

func TestSyncBuffer_ConcurrentWritesDoNotRace(t *testing.T) {
	buf := &syncBuffer{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = buf.Write([]byte("x"))
		}()
	}
	wg.Wait()
	if got := len(buf.String()); got != 50 {
		t.Errorf("expected 50 bytes written across 50 goroutines, got %d", got)
	}
}
