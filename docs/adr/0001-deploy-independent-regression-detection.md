# ADR-0001: Deploy-independent regression detection

**Status:** Proposed
**Date:** 2026-08-13
**Deciders:** Joao

## Context

pg-regression-radar's Correlation Engine (`internal/correlation`) only ever runs in response to a `DeployEvent`: `Engine.Analyse(ev)` builds a window symmetric around `ev.Timestamp`, and a detected regression is only confirmed when the E-divisive change point it finds lies within `ChangePointTolerance` of that timestamp. `PendingSet` exists purely to retry `Analyse` against the same event until its post-deploy window closes.

This hard-couples the tool's actual value (statistically confirming a real, significant latency shift) to one specific cause (a tracked Kubernetes deploy). Most real-world Postgres regressions have no deploy behind them at all — autovacuum falling behind, index/table bloat, stale planner statistics, organic data growth, a connection-pool/traffic-pattern change. None of those are detectable today, and the requirement to wire up a `DeploySource` (ArgoCD/Flux/Rollouts/native-K8s/generic webhook) is also the main reason the tool reads as Kubernetes-specific and narrow, even though the standalone `operator`/`collector` CLI binaries already work against any reachable Postgres DSN, no Kubernetes required.

This ADR is the first of three follow-ups from a broader "how do we make this tool matter to more people" discussion (see conversation history); the other two (a friction-free non-Kubernetes onboarding path, and prescriptive causes alongside detection) are out of scope here and will get their own ADRs.

Decisions on the three open questions below were made via direct discussion before writing this ADR (not re-litigated here, just recorded):

- **Baseline strategy:** split-window (recent half vs. previous half of a single rolling window), not a longer trailing baseline or a time-of-day/day-of-week model.
- **Alert suppression:** a re-arm state machine per query (alert once, stay "in regression" until latency recovers below threshold, then re-arm), not a fixed cooldown.
- **Scope:** both the CLI (`cmd/operator`) and the CRD (`cmd/manager`, `PostgresWatch`) get this in the same effort, not staged.

## Decision

Add a second, independent trigger path — periodic detection — that runs the existing E-divisive + Welch's t-test machinery on a rolling schedule, per tracked query, with no `DeployEvent` involved. It is **additive, not a replacement**: deploy-correlated detection (`Analyse`/`PendingSet`) keeps working exactly as today, unaffected by whether periodic detection is enabled. A `PostgresWatch` (or the CLI) can have either, both, or neither active.

Concretely:

1. **Engine refactor.** Extract the part of `evaluateQuery` that is genuinely deploy-agnostic (stage-0 mean pre-filter, stage-1 E-divisive, stage-2 Welch's t-test) from the part that is not (the `ChangePointTolerance`-to-`ev.Timestamp` check, and populating `DeployEventID`/`Namespace` from `ev`). Deploy-triggered `Analyse` keeps the tolerance check; a new `AnalysePeriodic` skips it entirely — for a self-contained window with no external anchor, the change point *is* the finding, wherever it falls.

   ```go
   // engine.go — shape, not final code
   func (e *Engine) Analyse(ev v1alpha1.DeployEvent) []v1alpha1.PerformanceRegression {
       // ... unchanged: builds window around ev.Timestamp, calls evaluateSeries,
       // then rejects if the change point isn't within ChangePointTolerance of ev.Timestamp.
   }

   func (e *Engine) AnalysePeriodic(now time.Time) []v1alpha1.PerformanceRegression {
       // window = [now - 2*PeriodicWindowMinutes, now], split at the midpoint
       // into "before"/"after" halves; same evaluateSeries core; no
       // deploy-timestamp proximity check at all.
   }
   ```

2. **New config knob:** `PeriodicWindowMinutes` (default 60 — deliberately double the deploy-mode default of 30, since there is no deploy prior narrowing where to look, so a shorter window would be noisier). Independent from `WindowMinutes` so tuning one never silently affects the other.

3. **Re-arm state machine.** Deploy-correlated detection's dedup (`PendingSet.notified map[int64]struct{}`) is scoped to one deploy event's lifetime — it doesn't need to survive across events. Periodic detection has no such natural boundary, so it needs its own per-query state, kept for the life of the watch:

   ```go
   // correlation package — shape, not final code
   type periodicState struct {
       inRegression map[int64]bool // queryID -> currently alerting (won't re-fire until recovered)
   }
   ```

   A query transitions `false -> true` (fires once) when `AnalysePeriodic` reports `Detected`; it stays `true` (suppressed) on every subsequent tick where the *recent* half is still significantly worse than a fresh baseline; it transitions back to `false` once a tick shows recent-half latency has recovered to within `LatencyChangeThreshold` of baseline, so a future regression can fire again. This needs a lightweight recovery check on every tick regardless of whether a new regression fires — effectively "is the query still bad" as well as "did it just become bad."

4. **CRD additions** (`PostgresWatchSpec`):

   ```yaml
   periodicDetection:
     enabled: false          # default off; opt-in per watch, mirrors autoAbort's own opt-in pattern
     windowMinutes: 60        # optional; falls back to a 60m default
     intervalMinutes: 15      # how often a full pass over tracked queries runs
   ```

5. **CLI additions** (`cmd/operator`): `--periodic-detection` (bool), `--periodic-window-minutes`, `--periodic-interval-minutes` — same shape as the existing `--window-minutes`/`--min-executions` flags.

6. **New poll path**, separate from the existing deploy-triggered one in both `internal/cli/operator.go` and `internal/controller/postgreswatch_controller.go`: a ticker firing every `intervalMinutes`, calling `AnalysePeriodic(time.Now())`, running results through the re-arm state machine, then through the *same* `alerting.Notifier`/`recordRegression` paths already in place — no changes needed to alerting or to `PerformanceRegression` persistence beyond one new field (see below).

7. **`PerformanceRegression` gets one new field:** `TriggerType` (`"deploy"` | `"periodic"`), so alerts/CRs make it obvious which detection path fired, and `DeployEventID` is simply empty for a periodic-triggered regression (it's already an optional-shaped string field, not a required one — no schema break).

## Options Considered

### Baseline strategy

#### Option A: Split-window (chosen)

| Dimension | Assessment |
|-----------|------------|
| Complexity | Low — reuses `evaluateQuery`'s existing core almost unchanged |
| False-positive risk | Real, but bounded by using a longer default window (60m) than deploy mode and a conservative `LatencyChangeThreshold`/`PValueThreshold` to start |
| Data/retention cost | None beyond what deploy-mode already needs |
| Time to ship | Fastest of the three |

**Pros:** minimal new code, no new retention requirements, directly reuses the statistically-validated E-divisive/Welch's-t-test core as-is.
**Cons:** genuinely vulnerable to ordinary daily/weekly traffic-pattern shifts producing a "detected" result with nothing wrong — start-of-business-hours load ramps are the obvious failure case. Mitigated only by tuning (window size, thresholds), not eliminated.

#### Option B: Recent vs. longer trailing baseline

| Dimension | Assessment |
|-----------|------------|
| Complexity | Medium — needs a second, longer-lived sample store (hours, not the ~180m default retention) |
| False-positive risk | Lower — a real trailing average/stddev absorbs some daily variation |
| Data/retention cost | Meaningful — likely forces the postgres state backend rather than memory-only for anyone using this |
| Time to ship | Slower |

**Pros:** meaningfully more robust to daily variation than Option A without the full complexity of Option C.
**Cons:** retention-cost implications ripple into `internal/storage`, `--retention-minutes` defaults, and the memory-vs-postgres backend story — a bigger blast radius than this ADR's scope.

#### Option C: Time-of-day/day-of-week baseline

| Dimension | Assessment |
|-----------|------------|
| Complexity | High — needs weeks of retained history and a seasonality model |
| False-positive risk | Lowest of the three, by design |
| Data/retention cost | Highest — weeks of samples, not hours |
| Time to ship | Slowest — realistically its own multi-ADR effort |

**Pros:** the only option that actually solves seasonality rather than working around it.
**Cons:** scope of a standalone project, not a follow-up ADR item.

### Alert suppression

#### Option A: Fixed cooldown

**Pros:** trivial to implement and reason about (a timestamp + duration check).
**Cons:** semantically wrong for a persisting condition — either re-alerts on a schedule while nothing changed (noisy), or, if the cooldown is long, stays silent well past the point a human already fixed and then broke it again.

#### Option B: Re-arm state machine (chosen)

**Pros:** matches what an operator actually wants — one alert per distinct episode of regression, re-firing only for a genuinely new episode, not a timer.
**Cons:** needs an explicit recovery check every tick (not just a "did it get worse" check), and per-query state that must survive restarts as gracefully as the rest of this project's in-memory-by-default state does (i.e., accepted to reset on restart, matching `PendingSet`'s and the Collector's own existing behavior, unless the postgres state backend is in use).

### Scope

#### Option A: CLI first, CRD later

**Pros:** smaller first PR, faster feedback loop, matches the "low-friction non-K8s onboarding" thread from the same discussion.
**Cons:** temporary feature gap between the two run modes.

#### Option B: CLI + CRD together (chosen)

**Pros:** no temporary gap; the re-arm state machine and engine refactor are shared code regardless, so the CRD wiring on top is comparatively small once the CLI side works.
**Cons:** larger single PR/review; `PostgresWatchSpec` schema, deepcopy, Helm chart, and RBAC (none needed here, unlike auto-abort — periodic detection touches nothing outside Postgres/CRD-status) all need updating in the same pass.

## Trade-off Analysis

The central trade-off is Option A vs. B/C on baseline strategy: A ships fastest and reuses the most existing, already-trusted code, but is the most exposed to false positives from ordinary traffic variation — exactly the failure mode that would erode trust in the tool fastest if it fires on "the store opened for the day" instead of "someone shipped a bad plan." B and C reduce that risk but pull in retention/storage changes this ADR deliberately keeps out of scope. Given this is genuinely new, unvalidated territory (unlike the alerting/actuation work, there's no existing production usage pattern to lean on yet), shipping A first — with conservative defaults and this ADR's own re-arm suppression softening the blast radius of a false positive to "one alert, then silence until it actually changes again" — is the right sequencing: validate the core mechanic against real traffic before paying for B/C's added retention cost.

## Consequences

- Detecting a regression no longer requires wiring up a `DeploySource` at all — the tool's addressable use case grows from "Postgres + Kubernetes + a tracked deploy tool" to "any Postgres with `pg_stat_statements`."
- `PerformanceRegression.TriggerType` becomes a new, permanent field every consumer (Slack/Teams/PagerDuty/custom formatters, `kubectl get performanceregression -o yaml`, the docs) needs to account for, even though none of the alerting work needs to change to support it (it's just one more piece of context in the same payload).
- Periodic detection's false-positive risk is real and will need real tuning against real traffic once this ships — the initial defaults (60m window, existing `LatencyChangeThreshold`/`PValueThreshold`) are a starting point, not a validated answer, and should be called out as such in the docs.
- A future ADR (Option B or C above) becomes the natural place to revisit baseline strategy if false positives prove to be a real adoption blocker — this ADR does not close that door, it deliberately defers it.
- The re-arm state (`periodicState`) is in-memory by default, same as the rest of this project's runtime state (`Collector`, `PendingSet`) — it resets on restart, which means a regression already "in progress" at restart time will re-fire once rather than staying silently suppressed. This matches the project's existing "memory is the default, postgres backend is opt-in for durability" pattern (`internal/storage`) rather than introducing a new one.

## Action Items

1. [ ] `internal/correlation/engine.go`: extract the deploy-agnostic core of `evaluateQuery` into a shared helper; add `AnalysePeriodic(now time.Time)` and `PeriodicWindowMinutes` to `Config`.
2. [ ] `internal/correlation`: new re-arm state type (`periodicState` or similar) with a recovery check, unit-tested for both the "fire once" and "recover, then re-fire" transitions.
3. [ ] `pkg/apis/v1alpha1` + `api/v1alpha1`: add `PerformanceRegression.TriggerType`; add `PostgresWatchSpec.PeriodicDetection` (`Enabled`, `WindowMinutes`, `IntervalMinutes`) with hand-edited deepcopy, mirrored CRD YAML (`config/crd/bases` + Helm copy).
4. [ ] `internal/cli/operator.go`: `--periodic-detection`/`--periodic-window-minutes`/`--periodic-interval-minutes` flags, a new ticker-driven poll loop parallel to the existing deploy-triggered one, `--dry-run` validation.
5. [ ] `internal/controller/postgreswatch_controller.go`: mirror the same poll loop into `WatchRuntime`/`pollLoop`, gated on `spec.periodicDetection.enabled`.
6. [ ] Helm chart: `analysis.periodicDetection.*` values wired into both operator and manager mode, matching the `autoAbort`/`alerting` precedent.
7. [ ] Docs: new `docs/periodic-detection.md` (mirroring `docs/auto-abort.md`'s structure) explicitly documenting the false-positive caveat from Consequences above; `docs/configuration.md` field/flag tables; `mkdocs.yml` nav entry.
8. [ ] Tests: engine-level (split-window detection, no-tolerance-check behavior), re-arm state machine (fire/suppress/recover/re-fire), CLI dry-run validation, controller reconcile wiring — following this session's existing pattern of fake-client/httptest-based unit tests plus real-Postgres integration coverage where the existing `internal/e2e` suite already does so for deploy-triggered detection.
9. [ ] `changelog.d/<PR>.feat.md` fragment once a PR number is known.
