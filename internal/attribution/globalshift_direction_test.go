package attribution_test

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
)

// A slow query that throttles a closed-loop workload makes the other queries
// faster. That co-movement runs against the regression, so it is no evidence of
// a cause shared with it, and the regression must still reach grade B.
// Reproduced from a real run: after dropping an index, the other queries of the
// cluster got 10-35% faster at the deploy.
func TestOpposingCoMovementDoesNotCapTheGrade(t *testing.T) {
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	rng := rand.New(rand.NewSource(7))
	mk := func(key string, base, postFactor float64) attribution.Unit {
		u := attribution.Unit{Key: key, Text: "select " + key, Tables: []string{"t_" + key}}
		for k := -180; k < 60; k++ { // 90 minutes before, 30 after
			m := base * math.Exp(rng.NormFloat64()*0.08)
			if k >= 0 {
				m *= postFactor
			}
			u.Obs = append(u.Obs, attribution.Obs{At: at.Add(time.Duration(k) * step), MeanMs: m, Calls: 200})
		}
		return u
	}
	units := []attribution.Unit{mk("target", 0.05, 100)}
	for i := 0; i < 8; i++ {
		units = append(units, mk(string(rune('a'+i)), 1, 1))
	}
	for i := 0; i < 6; i++ {
		units = append(units, mk(string(rune('p'+i)), 1, 0.8)) // 20% faster after the deploy
	}
	a := attribution.Assess(attribution.Config{}, units, nil, attribution.Deploy{ID: "d", At: at, Reversible: true, HasRollbackTarget: true})
	v := a.Units[0]
	for _, u := range a.Units {
		if u.Key == "target" {
			v = u
		}
	}
	if v.Grade != attribution.GradeB || !v.Rejected {
		t.Fatalf("target: grade %s rejected=%v flags=%v, want a rejected grade B", v.Grade, v.Rejected, v.Flags)
	}
}
