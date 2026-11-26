# ADR-0002: A live dashboard, opened with one command

**Status:** Accepted (implemented; amended 2026-11-26, see "Amendments")
**Date:** 2026-11-26
**Deciders:** Joao

## Context

The radar now decides and acts on its own: it assesses a deploy, grades the evidence (A to D), pauses or aborts a Rollout, verifies the abort and trips a circuit breaker. All of that is visible only in manager logs, a verdict endpoint and a ledger endpoint. A person watching a rollout, or arriving after one, cannot see *what the radar saw, what it concluded, why, and what it did*. The project says in its README that it "does not ship a dashboard"; that is now the biggest gap between what the engine does and what a user can trust.

What the repo already has and this builds on:

| Reusable | Where | Used for |
|---|---|---|
| Per-deploy assessments, decisions, action ledger, breaker state | `internal/attribution/shadow` (`Records`, `Actions`, `BreakerOpen`), `verdictapi` | the "what was decided and done" half |
| Per-query samples and deploy events over HTTP | `internal/dashboardapi` | history and charts |
| Log-arm source (canary vs stable) | `internal/pglog`, `attribution.arms` | the canary/stable chart and source health |
| Chart code, design tokens, copy for the engine's concepts | `docs/assets/playground/*` | visual language; the playground already explains the engine in a browser |
| A CLI entry point with subcommands | `cmd/pg-regression-radar`, `internal/cli` | the one command |

Decisions made with the owner before this ADR (recorded, not re-litigated):

- **Data origin in v1:** the real cluster. Demo and direct-Postgres origins come later through the same interface.
- **First screen:** "what is happening now" (live), not post-mortem or long-term trend.
- **Flexibility:** editable panels, saved and loaded as a declarative file. Not a user-written query language in v1.
- **Query text:** hidden by default (`--show-queries` to reveal), as proposed.
- **History:** kept the way Grafana does, read here as: retention is a configurable property of the data layer, the data lives outside the UI and the process, and every board has a time-range picker (last 15 minutes to 7 days, or an absolute range). See "History" below.
- **Scope:** the Observe API respects the namespace allowlist (`attribution.actuate.namespaces`). See "Scope" below.

## Decision

Ship a read-only, local web dashboard as `pg-regression-radar dashboard`, in three layers that can each be tested alone.

```
 browser (vanilla ES modules, SVG)       your laptop                      cluster
┌───────────────────────────────┐   ┌──────────────────────┐   SPDY    ┌────────────────────────┐
│ board = layout (grid) of      │   │ pg-regression-radar  │ port-fwd  │ manager (leader pod)   │
│ panels; panels read named     │◀─▶│ dashboard            │──────────▶│ observe API, bound to  │
│ datasets from the Observe API │   │  - embedded UI       │ (kube RBAC│ 127.0.0.1 in the pod   │
│ and live events (SSE)         │   │  - port-forward      │  pods/    │  - snapshot + SSE      │
│ layout saved in a data dir    │   │  - /api proxy        │  portfwd) │  - read-only           │
└───────────────────────────────┘   └──────────────────────┘           └────────────────────────┘
```

### 1. The Observe API (in the manager)

One versioned, read-only contract, `/observe/v1`, which the UI and any other client consume. Nothing in it can change cluster state.

| Endpoint | Returns |
|---|---|
| `GET /observe/v1/overview` | per watch: mode (`shadow`/`enforce`), actuation on/off, breaker state, kill-switch annotation state, allowlist; collector health (last scrape age, `pg_stat_statements` reachable, reset detected); arms health (log flowing, sample rate, settings issues) |
| `GET /observe/v1/deploys` | deploys the radar tracks, newest first: workload, revision, time, phase (`observing`, `decided`, `acted`, `verified`), next evaluation time, latest verdict, grade, impact, attribution, action wanted/taken |
| `GET /observe/v1/deploys/{id}` | the whole case: every evaluation in order, each with its decision and reasons, per-query verdicts (grade, ratio, interval, flags, cap reasons), actions and verification |
| `GET /observe/v1/deploys/{id}/series?unit=…` | the chart data for one query: treatment and control (canary/stable) series, the pre-deploy baseline and the engine's placebo band, the deploy marker |
| `GET /observe/v1/actions` | the ledger |
| `GET /observe/v1/events` | Server-Sent Events: `deploy.registered`, `evaluation`, `decision`, `action`, `verification`, `health` — what the live view subscribes to, so nothing polls |

Every list endpoint takes `from` and `to` (RFC 3339 or a relative `now-6h`), which is what the time-range picker sends; the case, series and ledger endpoints are addressed by id and also honour them for what they return.

Rules for the contract:

- **Same data the engine used.** A chart is drawn from the series the engine assessed, not recomputed by the UI, so the picture cannot disagree with the verdict. Each series carries its own sample counts so the UI can show how thin the evidence was.
- **Explanations are data, not UI copy.** Decisions already carry `Reasons`; flags and cap reasons gain stable machine codes plus a short plain-language text, so a panel can say "capped at grade C: too few donors (7 queries)" and a user can search the docs for the code.
- **No query parameters, no SQL text by default.** Queries are shown by fingerprint and table; the normalized text appears only when the dashboard is started with `--show-queries`.
- **Additive and versioned.** `/observe/v1` never removes a field; the dashboard refuses to talk to a manager whose `GET /observe/version` it does not understand and says which versions it supports.
- **Bounded memory.** The in-process view is bounded (the evaluator's record list, today unbounded, gets a cap) and the live series are the ones the collector window and the log store already hold.

### History

The Observe API reads history through one seam, `HistoryStore`, with a retention that is a setting and not a constant:

- **Memory (default).** A bounded ring of deploys, evaluations, actions and verifications. It is lost on restart, and the dashboard says so in the status strip ("history since 02:14, in memory") instead of showing a short list as if it were complete.
- **Postgres (opt-in).** The same optional state backend the operator already has (`--state-backend=postgres`, the `regression_radar` schema, the existing prune loop and `--state-retention`), extended with `attribution_deploys`, `attribution_evaluations` (the assessment as JSONB, versioned), `attribution_actions` and a compact `attribution_series` written at each evaluation (the points the engine used, downsampled, so a chart of a past deploy needs neither the collector window nor the log store). Retention defaults to 30 days. Writing history is best effort and never blocks or delays a decision: if the store is down the radar keeps acting and the dashboard shows "history not recorded since …".

The time-range picker therefore means the same thing in both modes: it asks the store for a range, and the API answers with what exists, saying what is missing at either end.

### Scope

The Observe API serves only deploys in the namespaces of `attribution.actuate.namespaces` when that list is set (empty means every namespace the deploy sources report, as it does for actuation). The filter is applied inside the API, on every endpoint including the event stream and the history, not in the UI, so a client cannot ask around it. The status strip names the scope ("scoped to: checkout, payments") so a missing deploy is explained rather than mysterious. The cost is deliberate: a deploy outside the allowlist is still judged and logged by the manager but invisible here; a separate visibility list would be a different decision.

### 2. Reaching it: one command, no new exposure

`pg-regression-radar dashboard [--context CTX] [--namespace NS] [--port 8765] [--open] [--config FILE] [--show-queries]`

- It finds the **leader** manager pod (the Lease holder, since only the leader runs the evaluators), opens a port-forward to it with the user's own kubeconfig, serves the UI on `127.0.0.1`, and proxies `/api` to the forward.
- The observe listener in the manager is bound to **127.0.0.1 inside the pod**. No Service, no Ingress, no token to mint or leak: the only way in is the Kubernetes `pods/portforward` permission the user already has or does not have. This is also why the chart needs no new Service.
- `--url http://host:port` skips the port-forward, for tests and for a user who exposed the API some other way.
- If the leader changes mid-session the command re-resolves it and the UI shows a reconnecting state rather than stale data without saying so.
- The dashboard binds to loopback only and refuses a non-loopback `--listen` unless `--insecure-listen` is passed, because the page shows an operator's view of production.

### 3. The UI: boards, panels, datasets

Plain ES modules and hand-written SVG, embedded in the binary with `go:embed`. No Node, no bundler, no runtime dependency, nothing downloaded at run time: the same constraint the docs playground already lives under, and the right one for a tool that runs next to production.

Three concepts, kept apart so each can be changed without touching the others:

- **Dataset**: a named, typed slice of the Observe API (`deploys`, `decisions`, `actions`, `source-health`, `unit-series`, …). Datasets are the only thing panels read; they hide transport (snapshot then SSE patches).
- **Panel**: a component with a type, a small schema of options and a dataset it binds to. Types for v1: `deploy-cards`, `decision-feed`, `evidence-table`, `canary-chart`, `ratio-chart`, `safety`, `source-health`, `kpi`.
- **Board**: a grid of panels. Presets ship in the binary; a board is just data.

```yaml
apiVersion: pgrr.io/v1
kind: Dashboard
boards:
  - id: now
    title: Now
    refresh: live
    panels:
      - {id: active,   type: deploy-cards,  at: [0, 0, 8, 4], options: {states: [observing, decided, acted]}}
      - {id: safety,   type: safety,        at: [8, 0, 4, 4]}
      - {id: feed,     type: decision-feed, at: [0, 4, 5, 6], options: {limit: 50}}
      - {id: evidence, type: evidence-table, at: [5, 4, 7, 6], options: {follow: active}}
      - {id: health,   type: source-health, at: [0, 10, 12, 2]}
```

Flexibility, in the order a user meets it: drag, resize, hide and re-add panels in the browser (saved in the command's data directory; see the amendment *Saved edits*); *Export* writes the YAML above; `--config` loads a file; `pg-regression-radar dashboard config validate FILE` checks it against the schema with line-numbered errors; `… config dump` prints the presets as files to start from. A panel's options are validated against its schema, an unknown panel type renders a clear placeholder instead of breaking the board, and a layout from an older version is migrated or reported, never silently dropped.

### 4. What the first board ("Now") shows, and why

A person watching a rollout asks four questions in order, so the board is laid out in that order:

1. **Is the radar alive and trustworthy right now?** A top strip: data freshness, mode, actuation on/off, breaker, kill switch, log-arm health. If the data is stale or the arms are unusable, say so first, because everything below depends on it.
2. **What deploys is it watching, and where is each?** One card per active deploy: workload and revision, phase and "next evaluation in 3m", verdict, **grade as a badge**, impact, attribution, and the action wanted vs taken. A card is not a number: it ends with one plain sentence ("Grade A: the canary is 2.3x slower than the stable pods on 3 queries; aborted at 02:19").
3. **Why?** Selecting a card shows the evidence: the per-query table (grade, ratio, interval, flags) and the **canary vs stable** chart with the deploy marker and the pre-deploy placebo band. The grade is explained where it appears (what A/B/C/D mean here, and what caps this one).
4. **What did it do, and was it right?** A live feed of evaluations and actions, and the verification result of each abort ("regression persisted after abort"), because trust in an agent that acts comes from seeing its mistakes as plainly as its saves.

Colour carries one meaning only (state: ok, watching, regressed, acted), never decoration; every colour has a text or shape twin so the board reads without colour; light and dark follow the system; the board is usable at laptop width without scrolling sideways.

A second preset, **Case**, is the same evidence full-screen for one deploy (the investigation view), and a third, **Source health**, is for debugging the radar itself.

### 5. How it is tested

- **Contract:** Go tests for each endpoint with golden JSON, a test that every field the UI reads exists in the golden, and an SSE test (ordering, resume after reconnect, slow-consumer drop).
- **Spec:** table tests for schema validation and migration of config files, including bad files.
- **UI logic:** the dataset layer and the layout engine run in Node without a DOM (the pattern `playground-core.js` already uses).
- **Visual:** Playwright against a **fixture source** that replays recorded observe responses (from the real e2e runs in this repo, so the fixtures are real shapes, not invented ones). Screenshots at three widths in light and dark are reviewed by a person, together, before each milestone is called done.
- **End to end:** the real-cluster harness already used for the arms test (real Postgres, API server, Rollout CRD, manager) with a canary regression, an A/A and an abort, checking that the dashboard shows the same verdict the ledger recorded. The port-forward itself cannot run in that harness (no kubelet), so it is tested against a recorded SPDY exchange and `--url` covers the rest; a real cluster run is a manual checklist item.

### 6. Milestones

Each ends with something that runs, is tested and is looked at before the next starts.

1. **Observe API + fixture source**: contract, golden files, SSE, `--url` mode. No UI yet; `curl` shows the case.
2. **Shell and "Now" board, read-only**: embedded UI, datasets, `deploy-cards`, `decision-feed`, `safety`, `source-health`.
3. **Evidence**: `evidence-table`, `canary-chart`, `ratio-chart`, grade/flag explanations, the **Case** board.
4. **History store**: the `HistoryStore` seam, the memory ring and the Postgres backend with retention, in step with the API.
5. **The command**: leader discovery, port-forward, reconnect, version handshake, `--open`.
6. **Flexibility**: edit/drag/resize/hide, export, `--config`, `config validate|dump`, migration.
7. **Hardening and docs**: accessibility and width checks, loopback guard, docs page and screenshots, an "add a panel type" guide.

## Amendments (as built)

- **Grid model.** Boards are a fixed-row-height grid, as Grafana's: `at: [x, y, w, h]` in cells, 12 columns, rows 40 px, panels compacted upward. The earlier sketch (`refresh: live`, `follow:` options) is dropped: every board is live through the event stream, and panels follow the picked deploy.
- **Layout bundle.** `GET /dashboard/layout` serves `{layout, source, catalog, grid}`: the layout (default preset or the `--config` file, re-read per request, 422 with line-numbered problems when invalid), the panel catalog and the grid constants, so the page and the validator cannot disagree. The UI's *Export* produces the same YAML as `dashboard config dump`; golden files pin it.
- **Preset boards** are Now, Case and Source health. Panel types differ from the first list: `summary`, `radar-state`, `deploy-cards`, `deploy-head`, `canary-chart`, `figures`, `why`, `evidence-table`, `ratio-chart`, `evaluations`, `ledger`, `decision-feed`, `safety`, `source-health`.
- **History DSN.** Resolves the first open question: the manager reads the Postgres DSN from a file (`--observe-history-dsn-file`), never from a flag, so it does not show in the pod spec. History is its own store (`--observe-history=memory|postgres`), independent of `--state-backend`.
- **Series.** Series are recorded per evaluation for the units the engine keeps, downsampled; other units answer 404 and the UI says no series was recorded. Resolves the second open question as "only the units the engine keeps".
- **Info button.** Each panel carries an info button (hover or focus shows, click pins) with a few sentences from the catalog (`help`), or the panel's own `description` from the file. It is how a board stays readable after someone else rearranged it.
- **Focus view.** Clicking a panel anywhere that is not a button, a list, a link or the info button, pressing `V` over it, or its expand button opens it alone, large, with a settings pane on the right, as Grafana's *view panel* does. The address becomes `#/<board>?view=<panel>` (so it can be shared, and Back closes it); Escape and *Back to the board* leave. What we took from Grafana: the pane is organised in collapsible groups with a search box; the visualization is chosen first; each setting has a help sentence and a way back to its default; a changed setting is marked. What we did differently: a change applies at once and is kept like any other layout edit (it is the same `localStorage` entry, exported as the same YAML), with *Discard changes* back to how the panel was when the view opened, rather than Grafana's Save/Discard pair, because nothing here is saved on a server.
- **Settings are catalog data.** Each option in the catalog carries `label`, `group`, `doc`, optional `labels` for enum values and an optional `when: "viz=table"` that hides it while another option has a different value. The pane is drawn from the catalog the server sends, so a new option on the Go side needs no change in the pane, only in the panel that reads it. The option named `viz` is the type's choice of visualization (deploy list as cards or compact list, canary as chart or table, evidence as table or ratio chart, activity as feed or table) and is drawn as a picker. Types that read one deploy carry `followsDeploy`, and the view then offers a deploy picker beside the panel. A panel is rebuilt when its title, description or settings change, so a panel never has to apply a change in place.
- **Panel queries.** A panel in focus has a *Query* field under it (Grafana's place for it) holding a filter language, saved as `query:` in the layout. It is not SQL and cannot become it: the dashboard stays read-only over what the Observe API serves, which is what keeps `GET`-only, the allowlist and the hidden query text intact. Grammar: `field:value` terms, bare or quoted values, same field OR, different fields AND, `*` in free-text fields, at most 12 terms and 200 characters; fields `app`, `namespace`, `state`, `grade`, `deploy`, `query`, `statement`, `table`. The catalog lists which fields each panel type takes (`query` on the type, `queryFields` on the bundle carries each field's help and closed values), so the field's suggestions are drawn from the catalog and from the live view (values with counts, refreshed on every event). Go validates a layout's queries (`ParseQuery`, `ValidateQuery`, errors that name the column) and the page evaluates them; `testdata/query-cases.json` is a hand-written contract both sides run, so they cannot drift. Panels that show one deploy resolve it from the query (`deploy:` names one; other deploy fields pick the newest match) and otherwise from the board's pick; a pinned deploy fetches its own series through a small per-session cache. We chose a constrained language over free-form expressions because a card's data is already shaped by the API and the cards would otherwise each need their own interpreter; the cost is that a query cannot reach data the API does not serve, which is the intent. Radar state, safety and source health take no query because they have nothing to select.
- **Saved edits.** Edits (layout, panel settings, queries, board ranges) are kept between runs in the command's own data directory, as one small JSON profile per dashboard source: `dashboard-default.json`, or `dashboard-file-<digest of the --config path>.json`. The directory is `--data-dir`, defaulting to `pg-regression-radar/dashboard` under the user's configuration directory (a mounted volume works), mode 0700, files 0600, written atomically. This replaces `localStorage` as the home of the profile; the browser's storage is now only the fallback (`--no-save`, or a directory that cannot be written, reported at start), and an earlier browser-only profile moves to disk once. The page reads `GET /dashboard/profile` at start and writes through `PUT`/`DELETE` about 350 ms after a change (and when the page is hidden). **This is the dashboard's first write.** "Read-only" now means toward the cluster and the radar; what makes the one write safe: the path is fixed and no request names a file; the Host check of the read paths applies; a write must carry `X-Requested-By: pgrr-dashboard` (a non-simple header, so a browser asks first and the server never answers the CORS question), a same-origin `Origin` and `Sec-Fetch-Site` when sent, and `application/json`; the body is at most 256 KiB and is validated as a profile (known fields, version, a layout that passes the dashboard-file validator, plain ranges) before anything is written; every other path stays `GET`/`HEAD` only. We did not write back into the `--config` file: that file is the person's, possibly in version control, and a page rewriting it would be surprising; *Export* remains the way to turn a profile into a file. A `--config` file that changed since the edits were made wins, as before (the profile stores the fingerprint of what it was edited on). With `--insecure-listen` saving is off, because with no sign-in anyone who can reach the page could rewrite the file; the page falls back to the browser. The profile holds no credentials and no Observe data, only names the user typed or picked (so topology metadata), and the file is 0600 in a 0700 directory; the threat table lives in `docs/security-model.md` (*What the dashboard saves*). Residual risks accepted: an existing directory keeps its own permissions, and anyone who can write the user's configuration directory can change what their dashboard shows.
- **Ranges.** The range picker accepts 1 minute to 90 days; the page reports when history is shorter than the range.
- **Permissions of the command.** `get leases`, `get pods`, `create pods/portforward`. The port-forward uses a WebSocket tunnel and falls back to SPDY.

## Consequences

- **A real trust surface for an agent that acts.** The decision, its evidence and its consequence are one click apart, which is what makes enforce mode adoptable.
- **The manager grows a second HTTP surface**, on loopback only. It is read-only and carries no query text by default, but it is new API to keep stable: hence the versioned contract and goldens.
- **Only the leader can answer**, so the command depends on Lease inspection and a re-resolve on failover. A lost leader shows as "reconnecting", not as an empty board.
- **History is as long as the chosen store.** In memory, a restart empties the case list and the dashboard says so; with the Postgres backend it survives restarts for the retention period, at the cost of a small write per evaluation and a schema to migrate. The manager does not yet have a state backend, so this adds its configuration.
- **Allowlist as visibility** keeps the dashboard's reach equal to the radar's authority, at the price of hiding judged-but-out-of-scope deploys.
- **No JavaScript toolchain**: smaller attack surface and an easy audit, at the price of writing the grid, the charts and the form controls ourselves and testing them well. The panel registry keeps that cost in one place.
- **Customisation is bounded on purpose.** Users can rearrange, hide and configure panels and share the file; they cannot define new queries over the data in v1. The schema leaves room for it (a dataset can gain parameters) without breaking files written today.

## Alternatives considered

- **Grafana dashboards over Prometheus metrics.** Familiar and cheap, but the interesting objects (a deploy's case, per-query evidence, canary vs stable series, an action and its verification) are not metrics, and "one command, no infrastructure" is lost. Metrics remain useful and remain exported; they are the wrong primary surface.
- **A React/Vite single-page app.** Faster to a rich UI, but it brings a build toolchain, a large dependency tree and a bundle to audit into a security-sensitive operator tool, and breaks the "embed and ship one binary" simplicity. Revisit only if the panel registry becomes the bottleneck.
- **An in-cluster dashboard behind a Service/Ingress.** Convenient for teams, but it needs authentication and authorisation the project does not have, and widens the exposure of an operator's view of production. A loopback port-forward reuses Kubernetes' own authentication; a shared deployment can be a later, explicitly secured option.
- **A user-written query language for panels now.** Maximum flexibility, but a large surface to design, secure and test before the built-in panels have proven what users actually need. Deferred, with the dataset parameter as the extension point.
- **Direct Postgres or a demo world as the first origin.** Both are cheap to add behind the same dataset interface and are worth having (onboarding, docs); they do not show the thing this ADR exists for, the radar's own decisions, so they follow the cluster origin.

## Open questions

- A shared, authenticated dashboard (in-cluster) remains a later, explicitly secured option.
- User-defined datasets: the dataset parameter is the extension point; nothing is designed yet.
