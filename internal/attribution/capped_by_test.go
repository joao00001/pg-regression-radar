package attribution_test

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
)

// A deploy stuck at an alert has to say what holds the evidence back, or there
// is nothing to fix. Here most of the untouched queries slow down with the
// target, so the shift is database-wide and the grade is capped at C.
func TestDecisionSaysWhatCapsTheGrade(t *testing.T) {
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(11))
	mk := func(key string, base, postFactor float64) attribution.Unit {
		u := attribution.Unit{Key: key, Text: "select " + key, Tables: []string{"t_" + key}}
		for k := -180; k < 60; k++ {
			m := base * math.Exp(rng.NormFloat64()*0.08)
			if k >= 0 {
				m *= postFactor
			}
			u.Obs = append(u.Obs, attribution.Obs{At: at.Add(time.Duration(k) * 30 * time.Second), MeanMs: m, Calls: 200})
		}
		return u
	}
	units := []attribution.Unit{mk("target", 0.05, 100)}
	for i := 0; i < 4; i++ {
		units = append(units, mk(string(rune('a'+i)), 1, 1))
	}
	for i := 0; i < 6; i++ {
		units = append(units, mk(string(rune('p'+i)), 1, 1.3)) // slower with the target
	}
	a := attribution.Assess(attribution.Config{}, units, nil, attribution.Deploy{ID: "d", At: at, Reversible: true, HasRollbackTarget: true})
	d := attribution.NewPolicy(attribution.PolicyConfig{}).Decide(at.Add(10*time.Minute), a)
	if d.Grade != attribution.GradeC {
		t.Fatalf("grade %s, want C (reasons %v)", d.Grade, d.Reasons)
	}
	if !strings.Contains(strings.Join(d.Reasons, "; "), "capped at grade C by: global-shift") {
		t.Fatalf("the decision does not say why it is capped: %v", d.Reasons)
	}
}
