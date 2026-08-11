package postgres

import (
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/storage"
)

// Compile-time interface assertions — no database required. This is the
// cheap, always-run check that the concrete types actually satisfy the
// shared storage.SampleStore / storage.EventStore contract; the real
// round-trip behaviour is covered by the integration tests in
// integration_test.go (build tag "integration", needs a live Postgres).
var (
	_ storage.SampleStore = (*SampleStore)(nil)
	_ storage.EventStore  = (*EventStore)(nil)
	_ storage.Pruner      = (*EventStore)(nil) // extra capability, see event_store.go
)

func TestFirstLine(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"  \n  select 1":                      "select 1",
		"\n\nCREATE TABLE foo (\n  id int\n)": "CREATE TABLE foo (",
		"single line":                         "single line",
	}
	for in, want := range cases {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}
