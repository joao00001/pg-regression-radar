package adapter_test

import (
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution/adapter"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func sample(id int64, sql string, at time.Duration, calls int64, total float64) pgstat.QuerySample {
	return pgstat.QuerySample{QueryID: id, QueryText: sql, RecordedAt: t0.Add(at), IntervalKnown: true, IntervalComputed: true, IntervalCalls: calls, IntervalTotalMs: total}
}

func TestUnitsGroupByFingerprintNotQueryID(t *testing.T) {
	// the same statement under two queryids (an OID change) is one unit
	us := adapter.Units([]pgstat.QuerySample{
		sample(1, "SELECT * FROM orders WHERE id = $1", 0, 10, 50),
		sample(2, "SELECT * FROM orders WHERE id = $1", time.Minute, 10, 60),
		sample(3, "select * from orders where id = $1", 2*time.Minute, 5, 20),
	}, collector.FingerprintQuery, nil)
	if len(us) != 1 {
		t.Fatalf("got %d units, want 1", len(us))
	}
	if len(us[0].QueryIDs) != 3 || len(us[0].Obs) != 3 {
		t.Fatalf("unit %+v", us[0])
	}
	if us[0].Obs[0].MeanMs != 5 || us[0].Obs[1].MeanMs != 6 || us[0].Obs[2].MeanMs != 4 {
		t.Fatalf("means %+v", us[0].Obs)
	}
}

func TestUnitsSumSameScrapeAndSkipEmpty(t *testing.T) {
	us := adapter.Units([]pgstat.QuerySample{
		sample(1, "SELECT 1 FROM t", 0, 10, 100),
		sample(2, "SELECT 1 FROM t", 0, 30, 100), // same scrape, second row of the same fingerprint
		sample(1, "SELECT 1 FROM t", time.Minute, 0, 0),
	}, collector.FingerprintQuery, nil)
	if len(us) != 1 || len(us[0].Obs) != 1 {
		t.Fatalf("got %+v", us)
	}
	if o := us[0].Obs[0]; o.Calls != 40 || o.MeanMs != 5 {
		t.Fatalf("obs %+v", o)
	}
}

func TestUnitsMeanOnlyAndTouched(t *testing.T) {
	us := adapter.Units([]pgstat.QuerySample{
		{QueryID: 1, QueryText: "UPDATE accounts SET x = $1", RecordedAt: t0, MeanExecTimeMs: 7},
	}, collector.FingerprintQuery, []string{"accounts"})
	if len(us) != 1 || !us[0].Touched || us[0].Obs[0].Calls != 0 || us[0].Obs[0].MeanMs != 7 {
		t.Fatalf("got %+v", us)
	}
}

func TestUnitsDeterministicOrder(t *testing.T) {
	var in []pgstat.QuerySample
	for i := 0; i < 20; i++ {
		in = append(in, sample(int64(i), "SELECT "+string(rune('a'+i))+" FROM t"+string(rune('a'+i)), 0, 1, 1))
	}
	a := adapter.Units(in, collector.FingerprintQuery, nil)
	b := adapter.Units(in, collector.FingerprintQuery, nil)
	for i := range a {
		if a[i].Key != b[i].Key {
			t.Fatal("order not deterministic")
		}
	}
}
