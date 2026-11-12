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

package attribution

import (
	"fmt"
	"sync"
	"time"
)

// The policy turns an Assessment into an action. It is deliberately thin: all
// the statistical judgement lives in Assess, and the policy only asks how much
// the evidence supports, how much it is worth, and whether the action is safe.
//
// The action ladder is keyed on the evidence grade, because the cost of a wrong
// action grows with the action and the reliability of the evidence falls with
// the grade:
//
//	A  concurrent control arm        -> Rollback (reversible, attributable)
//	B  synthetic control, validated  -> Hold     (stop promoting; a human decides)
//	C  own history only              -> Alert
//	D  nothing                       -> None
//
// Insufficient data never blocks a deploy: it yields ActionNone, not a hold.

// Action is what the policy decides to do about a deploy.
type Action string

const (
	ActionNone     Action = "none"
	ActionAlert    Action = "alert"
	ActionHold     Action = "hold"
	ActionRollback Action = "rollback"
)

func (a Action) Rank() int {
	switch a {
	case ActionAlert:
		return 1
	case ActionHold:
		return 2
	case ActionRollback:
		return 3
	}
	return 0
}

// PolicyConfig tunes the action policy.
type PolicyConfig struct {
	// Enforce makes decisions executable. False is shadow mode: the decision is
	// computed and recorded, and Decision.Enforced is false, so a caller never
	// acts on it. Shadow is the default so a new engine earns trust first.
	Enforce bool
	// MinImpact is the supported lower bound of added database time, as a
	// fraction of the pre-deploy total, below which a regression is not worth
	// acting on (default 0.05).
	MinImpact float64
	// MinConsecutive is how many consecutive evaluations must agree before an
	// action above Alert is taken (default 2).
	MinConsecutive int
	// MinAttribution is the probability that this deploy, not a concurrent one,
	// is the cause, required for Hold and Rollback (default 0.8).
	MinAttribution float64
	// Cooldown is the minimum time between two actions on the same deploy
	// (default 10m).
	Cooldown time.Duration
	// BreakerLimit is the number of actions that did not help, after which the
	// circuit breaker opens and only alerts are issued (default 2).
	BreakerLimit int
}

func (c PolicyConfig) withDefaults() PolicyConfig {
	if c.MinImpact <= 0 {
		c.MinImpact = 0.05
	}
	if c.MinConsecutive <= 0 {
		c.MinConsecutive = 2
	}
	if c.MinAttribution <= 0 {
		c.MinAttribution = 0.8
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 10 * time.Minute
	}
	if c.BreakerLimit <= 0 {
		c.BreakerLimit = 2
	}
	return c
}

// Decision is one recorded policy outcome, the unit of the shadow ledger.
type Decision struct {
	At       time.Time
	DeployID string
	Action   Action
	// Wanted is the action the evidence called for before the safety guards
	// capped it; it differs from Action when a guard applied.
	Wanted   Action
	Grade    Grade
	Impact   float64
	Streak   int
	Enforced bool
	Reasons  []string
}

// Verification is the outcome of checking, after an action, whether it helped.
type Verification struct {
	Effective bool
	Reason    string
}

// Policy is the stateful decision maker for a stream of assessments. It is safe
// for concurrent use.
type Policy struct {
	cfg PolicyConfig

	mu      sync.Mutex
	streak  map[string]int
	lastAct map[string]time.Time
	failed  int
	open    bool // circuit breaker
	ledger  []Decision
}

// NewPolicy returns a policy with the given configuration.
func NewPolicy(cfg PolicyConfig) *Policy {
	return &Policy{
		cfg:     cfg.withDefaults(),
		streak:  map[string]int{},
		lastAct: map[string]time.Time{},
	}
}

// Decide maps an assessment, taken at now, to an action and records it.
func (p *Policy) Decide(now time.Time, a Assessment) Decision {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := a.Deploy.ID
	d := Decision{At: now, DeployID: id, Action: ActionNone, Wanted: ActionNone, Grade: GradeD}

	grade, impact := p.supported(a)
	d.Grade, d.Impact = grade, impact

	if a.Health != HealthRegressed || grade == GradeD {
		p.streak[id] = 0
		switch a.Health {
		case HealthInsufficient:
			d.Reasons = append(d.Reasons, "insufficient data: never blocks, no action")
		case HealthRegressed:
			d.Reasons = append(d.Reasons, fmt.Sprintf("regression below the materiality threshold (%.1f%% of database time supported)", impact*100))
		default:
			d.Reasons = append(d.Reasons, "no regression beyond the margin")
		}
		return p.record(d)
	}

	p.streak[id]++
	d.Streak = p.streak[id]

	switch grade {
	case GradeA:
		d.Wanted = ActionRollback
	case GradeB:
		d.Wanted = ActionHold
	default:
		d.Wanted = ActionAlert
	}
	d.Action = d.Wanted
	d.Reasons = append(d.Reasons, fmt.Sprintf("grade %s evidence supports %.1f%% added database time", grade, impact*100))

	cap := func(to Action, why string) {
		if to.Rank() < d.Action.Rank() {
			d.Action = to
		}
		d.Reasons = append(d.Reasons, why)
	}
	if d.Action.Rank() > ActionAlert.Rank() {
		if d.Streak < p.cfg.MinConsecutive {
			cap(ActionAlert, fmt.Sprintf("seen in %d of %d consecutive evaluations required", d.Streak, p.cfg.MinConsecutive))
		}
		if a.Attribution < p.cfg.MinAttribution {
			cap(ActionAlert, fmt.Sprintf("attribution %.2f below %.2f: a concurrent change may be the cause", a.Attribution, p.cfg.MinAttribution))
		}
		if a.GlobalShift && grade != GradeA {
			cap(ActionAlert, "the shift is database-wide and there is no control arm to separate it from the deploy")
		}
		if p.open {
			cap(ActionAlert, "circuit breaker open: earlier actions on this stream did not help")
		}
	}
	if d.Action == ActionRollback && (!a.Deploy.Reversible || !a.Deploy.HasRollbackTarget) {
		why := "no rollback target (first deploy): holding instead"
		if !a.Deploy.Reversible {
			why = "deploy is not reversible: holding instead"
		}
		cap(ActionHold, why)
	}
	if d.Action.Rank() > ActionAlert.Rank() {
		if t, ok := p.lastAct[id]; ok && now.Sub(t) < p.cfg.Cooldown {
			cap(ActionAlert, "cooldown after the previous action")
		}
	}

	if d.Action.Rank() > ActionAlert.Rank() {
		p.lastAct[id] = now
	}
	return p.record(d)
}

// supported returns the strongest grade whose supported lower-bound impact
// reaches the materiality threshold, and that impact.
func (p *Policy) supported(a Assessment) (Grade, float64) {
	switch {
	case a.Impact.A >= p.cfg.MinImpact:
		return GradeA, a.Impact.A
	case a.Impact.AB >= p.cfg.MinImpact:
		return GradeB, a.Impact.AB
	case a.Impact.ABC >= p.cfg.MinImpact:
		return GradeC, a.Impact.ABC
	}
	return GradeD, a.Impact.ABC
}

func (p *Policy) record(d Decision) Decision {
	d.Enforced = p.cfg.Enforce && d.Action.Rank() > ActionAlert.Rank() && !p.open
	p.ledger = append(p.ledger, d)
	return d
}

// Verify closes the loop after an action: post is an assessment taken after the
// action had time to act. If the regression is still supported at grade B or
// better, the action did not help (the deploy was not the cause, or the action
// did not take), the failure is counted, and after BreakerLimit failures the
// circuit breaker opens and the policy only alerts.
func (p *Policy) Verify(post Assessment) Verification {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, imp := p.supported(post)
	if post.Health == HealthRegressed && (g == GradeA || g == GradeB) {
		p.failed++
		if p.failed >= p.cfg.BreakerLimit {
			p.open = true
		}
		return Verification{false, fmt.Sprintf("regression still supported after the action (grade %s, %.1f%%)", g, imp*100)}
	}
	p.failed = 0
	return Verification{true, "regression no longer supported after the action"}
}

// BreakerOpen reports whether the circuit breaker has stopped automatic actions.
func (p *Policy) BreakerOpen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.open
}

// Reset closes the circuit breaker, for an operator who has looked.
func (p *Policy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open, p.failed = false, 0
}

// Ledger returns a copy of the recorded decisions, oldest first.
func (p *Policy) Ledger() []Decision {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Decision(nil), p.ledger...)
}

// Verdict is the answer a progressive-delivery controller can act on. It is
// shaped for Argo Rollouts' analysis: a "fail" aborts the rollout, an
// "inconclusive" pauses it at its current step, and a "pass" lets it go on.
type Verdict string

const (
	VerdictPass         Verdict = "pass"
	VerdictInconclusive Verdict = "inconclusive"
	VerdictFail         Verdict = "fail"
)

// Verdict maps a decision to what a controller is told. A decision that is not
// Enforced (shadow mode, an open circuit breaker, an alert-only grade) is always
// a pass, so that nothing is ever blocked by a decision that was not meant to
// act; the action it would have taken stays visible in Decision.Wanted.
func (d Decision) Verdict() Verdict {
	if !d.Enforced {
		return VerdictPass
	}
	switch d.Action {
	case ActionRollback:
		return VerdictFail
	case ActionHold:
		return VerdictInconclusive
	}
	return VerdictPass
}
