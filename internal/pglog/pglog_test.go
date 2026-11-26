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

package pglog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var arrival = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

func TestParseLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Record
		ok   bool
	}{
		{"simple statement", `{"timestamp":"2026-11-25 21:35:46.681 -03","message":"duration: 0.549 ms  statement: select 1","application_name":"web-abc","query_id":-3776451877005378142}`,
			Record{At: time.Date(2026, 11, 26, 0, 35, 46, 681e6, time.UTC), App: "web-abc", QueryID: -3776451877005378142, DurationMs: 0.549}, true},
		{"extended protocol execute", `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 12.5 ms  execute <unnamed>: select * from t where id = $1","detail":"parameters: $1 = 'secret'","application_name":"a","query_id":7}`,
			Record{At: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), App: "a", QueryID: 7, DurationMs: 12.5}, true},
		{"named execute", `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 1 ms  execute S_1: select 1","application_name":"a","query_id":7}`,
			Record{At: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), App: "a", QueryID: 7, DurationMs: 1}, true},
		{"parse step is not an execution", `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 0.1 ms  parse S_1: select 1","application_name":"a","query_id":7}`, Record{}, false},
		{"bind step is not an execution", `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 0.1 ms  bind S_1: select 1","application_name":"a","query_id":7}`, Record{}, false},
		{"plan is not an execution", `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 3 ms  plan:\n{...}","application_name":"a","query_id":7}`, Record{}, false},
		{"no query id", `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 3 ms  statement: vacuum t","application_name":"a"}`, Record{}, false},
		{"not a duration", `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"connection authorized","application_name":"a","query_id":7}`, Record{}, false},
		{"garbage", `not json duration: `, Record{}, false},
		{"unknown zone falls back to arrival", `{"timestamp":"2026-11-25 10:00:00.000 CET","message":"duration: 2 ms  statement: select 1","application_name":"a","query_id":9}`,
			Record{At: arrival, App: "a", QueryID: 9, DurationMs: 2}, true},
		{"cloudnativepg wrapper", `{"level":"info","logger":"postgres","msg":"record","record":{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 4 ms  statement: select 1","application_name":"pod-1","query_id":5}}`,
			Record{At: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), App: "pod-1", QueryID: 5, DurationMs: 4}, true},
	}
	for _, c := range cases {
		got, ok := ParseLine([]byte(c.line), arrival)
		if ok != c.ok {
			t.Fatalf("%s: ok=%v want %v", c.name, ok, c.ok)
		}
		if ok && (!got.At.Equal(c.want.At) || got.App != c.want.App || got.QueryID != c.want.QueryID || got.DurationMs != c.want.DurationMs) {
			t.Fatalf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestParseLineNeverKeepsParametersOrText(t *testing.T) {
	line := `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 1 ms  execute S_1: select ssn from people where id = $1","detail":"parameters: $1 = '123-45-6789'","application_name":"a","query_id":1}`
	rec, ok := ParseLine([]byte(line), arrival)
	if !ok {
		t.Fatal("not parsed")
	}
	if s := fmt.Sprintf("%+v", rec); strings.Contains(s, "123-45") || strings.Contains(s, "ssn") {
		t.Fatalf("record leaks %s", s)
	}
}

func TestStoreBucketsByIntervalAndApp(t *testing.T) {
	s := NewStore(time.Minute, time.Hour, 0)
	t0 := time.Date(2026, 11, 25, 10, 0, 10, 0, time.UTC)
	for i, ms := range []float64{10, 20, 30} {
		s.Add(Record{At: t0.Add(time.Duration(i) * 5 * time.Second), App: "stable", QueryID: 1, DurationMs: ms})
	}
	s.Add(Record{At: t0, App: "canary", QueryID: 1, DurationMs: 90})
	s.Add(Record{At: t0.Add(70 * time.Second), App: "stable", QueryID: 1, DurationMs: 40})

	got := s.Range(t0.Add(-time.Hour), t0.Add(time.Hour))
	if len(got) != 3 {
		t.Fatalf("got %d buckets: %+v", len(got), got)
	}
	var stable0 Bucket
	for _, b := range got {
		if b.App == "stable" && b.At.Equal(time.Date(2026, 11, 25, 10, 1, 0, 0, time.UTC)) {
			stable0 = b
		}
	}
	if stable0.N != 3 || stable0.Mean() != 20 {
		t.Fatalf("stable first bucket %+v", stable0)
	}
}

func TestStoreOutOfOrderAndPrune(t *testing.T) {
	s := NewStore(time.Minute, 10*time.Minute, 0)
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	s.Add(Record{At: t0.Add(5 * time.Minute), App: "a", QueryID: 1, DurationMs: 1})
	s.Add(Record{At: t0, App: "a", QueryID: 1, DurationMs: 1}) // late
	s.Add(Record{At: t0.Add(30 * time.Minute), App: "a", QueryID: 2, DurationMs: 1})
	s.Prune()
	got := s.Range(t0.Add(-time.Hour), t0.Add(time.Hour))
	if len(got) != 1 || got[0].QueryID != 2 {
		t.Fatalf("after prune: %+v", got)
	}
	if s.Stats().Pairs != 1 {
		t.Fatalf("pairs: %+v", s.Stats())
	}
}

func TestStoreBoundsDistinctPairs(t *testing.T) {
	s := NewStore(time.Minute, time.Hour, 3)
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		s.Add(Record{At: t0, App: fmt.Sprintf("req-%d", i), QueryID: 1, DurationMs: 1})
	}
	st := s.Stats()
	if st.Pairs != 3 || st.Dropped != 7 {
		t.Fatalf("stats %+v", st)
	}
	// a pair already held is still accepted
	s.Add(Record{At: t0, App: "req-0", QueryID: 1, DurationMs: 1})
	if s.Stats().Dropped != 7 {
		t.Fatal("a held pair was dropped")
	}
}

func TestConsumeSkipsOverlongLinesAndKeepsGoing(t *testing.T) {
	good := `{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 1 ms  statement: select 1","application_name":"a","query_id":1}`
	huge := `{"message":"duration: 1 ms  statement: select ` + strings.Repeat("x", maxLine+10) + `","query_id":2}`
	in := good + "\n" + huge + "\n" + good + "\n" + "junk\n"
	var n int
	skipped, err := Consume(context.Background(), strings.NewReader(in), func(Record) { n++ })
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || skipped != 1 {
		t.Fatalf("records %d skipped %d", n, skipped)
	}
}

func TestFollowFileReadsNewLinesAndSurvivesRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.json")
	line := func(q int) string {
		return fmt.Sprintf(`{"timestamp":"2026-11-25 10:00:00.000 UTC","message":"duration: 1 ms  statement: select 1","application_name":"a","query_id":%d}`+"\n", q)
	}
	if err := os.WriteFile(path, []byte(line(100)), 0o600); err != nil { // already there: not replayed
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = FollowFile(ctx, path, 10*time.Millisecond, func(r Record) { mu.Lock(); seen = append(seen, r.QueryID); mu.Unlock() })
		close(done)
	}()
	waitFor := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(seen)
			mu.Unlock()
			if n >= want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("saw %v, want %d records", seen, want)
	}
	time.Sleep(100 * time.Millisecond)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(line(1))
	_ = f.Close()
	waitFor(1)

	// rotate: the old file is renamed and a new one starts
	_ = os.Rename(path, path+".1")
	if err := os.WriteFile(path, []byte(line(2)+line(3)), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(3)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	for _, q := range seen {
		if q == 100 {
			t.Fatalf("replayed what was there before following: %v", seen)
		}
	}
}

func TestSamplingWeightsKeepTheMeanUnbiasedWhenSlowStatementsAreAlwaysLogged(t *testing.T) {
	// 1000 calls: 900 at 1 ms, 100 at 100 ms; true mean = 10.9 ms. The server
	// samples 10% of statements but logs everything >= 50 ms.
	smp := Sampling{Rate: 0.1, AlwaysAtOrAboveMs: 50}
	s := NewStore(time.Minute, time.Hour, 0)
	s.SetSampling(smp)
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 90; i++ { // a tenth of the fast ones were logged
		s.Add(Record{At: at, App: "a", QueryID: 1, DurationMs: 1})
	}
	for i := 0; i < 100; i++ { // all the slow ones were
		s.Add(Record{At: at, App: "a", QueryID: 1, DurationMs: 100})
	}
	b := s.Range(at.Add(-time.Hour), at.Add(time.Hour))[0]
	if got := b.Mean(); got < 10.8 || got > 11.0 {
		t.Fatalf("weighted mean %.3f, want about 10.9 (unweighted would be %.1f)", got, (90.0+10000)/190)
	}
	if b.N != 190 {
		t.Fatalf("N=%d: the weight of the mean is the sample count", b.N)
	}
}

func TestSamplingWeight(t *testing.T) {
	if (Sampling{}).Weight(5) != 1 || (Sampling{Rate: 1}).Weight(5) != 1 {
		t.Fatal("unsampled log must weigh 1")
	}
	if w := (Sampling{Rate: 0.25, AlwaysAtOrAboveMs: -1}).Weight(5); w != 4 {
		t.Fatalf("w=%v", w)
	}
}

func TestValidFilePath(t *testing.T) {
	for _, ok := range []string{"/var/log/postgresql/postgresql.json", "/pg.json"} {
		if err := ValidFilePath(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "pg.json", "./pg.json", "/var/log/../etc/passwd", "/var//log/pg.json", "/var/log/", "/var/log/./pg.json"} {
		if ValidFilePath(bad) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestFollowFileIgnoresWhatIsNotARegularFile(t *testing.T) {
	dir := t.TempDir() // a directory, not a file
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	var n int
	if err := FollowFile(ctx, dir, 10*time.Millisecond, func(Record) { n++ }); err != nil || n != 0 {
		t.Fatalf("err %v records %d", err, n)
	}
}
