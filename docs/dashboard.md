# Dashboard

*A local web page, read-only toward your cluster, that shows what the radar saw, what it decided, why, and what it did. One command opens it.*

## Try it without a cluster

```bash
pg-regression-radar dashboard --fixture internal/observe/testdata/recorded-sim.json --open
```

The page says **Recorded sample, not a live cluster** whenever it shows recorded data, so a sample is never mistaken for your system.

The recording is fixed: it answers with what was captured and nothing new arrives, so the page does not change by itself. The time range, the boards, the focus view with its settings and queries, and layout editing all work on it. Live updates and real history need a cluster (next section).

## Open it on your cluster

Before you start:

- **The manager is installed and running** in the cluster (the chart's manager mode, `cmd/manager`). The dashboard does not install or start anything: it reads the manager's Observe API, which is on by default (`--observe-addr`, `127.0.0.1:8083`; an empty value turns it off) and is served only by the manager's leader.
- **Your Kubernetes user has the permissions listed below**, in the manager's namespace.
- **The radar has deploys to show.** They come from the deploy sources you configured (see [Deploy Sources & Webhooks](webhooks.md)), and the radar judges one about ten minutes after it is registered. Until then the panels say *No deploys in this time range*; widen the range or wait for the next deploy.

```bash
pg-regression-radar dashboard --namespace <manager-namespace> --open
# another cluster:
pg-regression-radar dashboard --context <kube-context> --namespace <manager-namespace> --open
```

What the command does:

1. Reads the manager's leader-election Lease (`pg-regression-radar-manager.radar.pgregressionradar.io`) to find the **leader** pod, because only the leader runs the evaluators.
2. Opens a port-forward to that pod with **your own kubeconfig**. The manager's Observe API listens on `127.0.0.1` inside the pod (`--observe-addr`, default `127.0.0.1:8083`), so there is no Service, Ingress or token to leak.
3. Serves the page on `127.0.0.1:8765` (`--port`).
4. If the leader changes, it finds the new one and the page shows *Reconnecting*; if the manager's API version is not one the dashboard understands, it says *Incompatible* and names the versions it supports.

Your Kubernetes user needs: `get` on `leases` (coordination.k8s.io), `get` on `pods`, and `create` on `pods/portforward`. Without `--namespace` the command looks for the Lease in every namespace you may list.

| Flag | Meaning |
|---|---|
| `--namespace`, `--context`, `--kubeconfig`, `--lease-name` | Where the manager is and how to reach it |
| `--url http://host:port` | Skip the port-forward and read an Observe API you exposed yourself |
| `--port`, `--listen`, `--open` | Where the page is served; `--listen` must be a loopback address unless you add `--insecure-listen` |
| `--config FILE` | Boards and panels from a file (below) |
| `--data-dir DIR`, `--no-save` | Where your edits are kept between runs (default: `pg-regression-radar/dashboard` in your user configuration directory), or `--no-save` to keep them in the browser only. See [Your edits are kept between runs](#your-edits-are-kept-between-runs) |
| `--show-queries` | Show normalised SQL text. The manager must allow it too (`--observe-allow-queries`) |

The page has no sign-in. Anyone who can reach it sees your deploys, which is why it refuses a non-loopback `--listen` unless you pass `--insecure-listen`, and why it rejects requests whose `Host` is not this machine (DNS rebinding).

## What the page shows

- **Now**: is the radar watching and fresh, which deploys it is judging, the picked deploy's grade, the canary-against-stable chart, why it decided that, the evidence per query, and what it did.
- **Case**: the same evidence for one deploy, full width, with every evaluation, a ratio chart of all queries and the activity feed beside the deploy list. Open it from a deploy with *Open as a case*; the address (`#/case/<id>`) can be shared.
- **Source health**: whether each source the radar reads is answering, what the radar may do (mode, actuation, breaker, namespaces) and the activity feed.

Every panel has an **info button** at its top right. Hover it, or focus it with the keyboard, to read what the panel shows and how to read it; click to keep the text open (Escape or a click elsewhere closes it). A panel's own `description` in the file replaces the built-in text.

### Look at one panel

Click a panel (on its text or its chart, not on a button or list inside it), press **V** while the pointer is over it, or use the expand button that appears beside its info button. The panel opens alone, large, with its settings on the right. **Esc**, *Back to the board* or the browser's Back button return to the board. The address (`#/now?view=canary`) can be shared.

The settings pane is organised in groups you can fold, with a search box:

- **Visualization**: how the panel is drawn. The deploy list is cards or a compact list; the canary chart is a line chart or a table; evidence is a table or a ratio chart; activity is a feed or a table.
- **Data**: which rows (all, only problems, only flagged queries…), their order and how many.
- **Display**: what the picture carries (the baseline, the sample bars, the axis from zero, the columns, the figures).
- **Panel**: the panel's own title and description, which the info button shows.

A setting that does not apply to the chosen visualization is hidden. A changed setting is marked and has its own *Reset*. Changes apply at once and are saved like any other layout edit; *Discard changes* goes back to how the panel was when you opened it, *Reset to defaults* clears the panel's settings, and *Copy as YAML* gives the panel as the lines the dashboard file holds. A panel that shows one deploy has a deploy picker beside it, and choosing there chooses for the whole board.

### Change what a panel shows: the query

Under a panel in focus there is a **Query** field, as in Grafana's panel editor. It is not SQL: the dashboard only reads what the Observe API already serves, so the field takes a short filter language over that data. Click it and it lists the fields it understands; type `app:` and it lists the values that exist right now, with how many deploys or queries each matches, and it keeps up as the data changes. Under the field the panel says what it now shows ("Shows checkout · payments · 09:00, the newest deploy that matches"), or why the query cannot run (the column, what is wrong, what to write). A query that cannot run is never applied, so the panel keeps showing what it showed.

```text
app:checkout                         # one application
app:checkout app:search              # same field twice: either one
app:checkout state:regressed         # different fields: both must hold
statement:UPDATE table:orders        # evidence for one kind of statement on one table
deploy:checkout-7f9c query:"SELECT*" # one deploy, queries whose name starts with SELECT
```

- A term is `field:value`; a value with spaces is `"quoted"` (`\"` and `\\` inside). At most 12 terms and 200 characters. Text is matched without regard to case; `*` matches any text in the free-text fields (`app`, `namespace`, `query`, `statement`, `table`, `deploy`).
- Fields: `app`, `namespace`, `state` (`regressed`, `acted`, `watching`, `healthy`, `inconclusive`), `grade` (`A` to `D`), `deploy` (one deploy, by its id), `query` (a query by its name on the panel, its base name or the start of its fingerprint), `statement` (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) and `table`.
- Each panel type takes the fields that make sense for it. List panels (deploys, activity, summary) are only filtered. Panels that show one deploy (deploy, canary, evidence, ratio chart, evaluations, figures, why, ledger) show the picked deploy of the board unless their query names one: `deploy:` chooses that deploy, and `app:`, `namespace:`, `state:` or `grade:` choose the newest deploy that matches. The deploy picker is disabled on such a panel, since its query decides. Panels that draw one query's measurements (canary, evidence, ratio chart, evaluations) also filter by `query`, `statement` and `table`. The radar state, safety and source health panels have nothing to filter and show no field. `dashboard config dump --catalog` prints the fields of each panel type.
- A panel with a query says so in its info text, and the query is saved with the panel as `query:` in the layout file, so it is part of what *Copy as YAML* and *Export* give.
- Because a query can name a deploy that is not on the board, such a panel fetches that deploy's own series. This only works for deploys the page already holds (the newest 30 in the time range and the picked one); outside those, the panel says the deploy is not in the range.

Queries are named by statement and table (`SELECT on orders`; two that would read the same are numbered), because the SQL text is hidden unless you ask for it.

### Your edits are kept between runs

Everything you change in the page (a panel's settings and visualization, its title and description, its query, positions and sizes, the time range of each board) is saved on its own, a moment after the change, in the dashboard's **data directory**. Close the dashboard, run the command again and the page opens as you left it, in any browser.

- The directory is `pg-regression-radar/dashboard` inside your user configuration directory (`~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows). Choose another with `--data-dir DIR`, which can be a mounted volume. The command prints the file it uses when it starts.
- One file per dashboard: `dashboard-default.json` for the built-in boards, and `dashboard-file-<id>.json` for each `--config` file (the id comes from the file's path, so two files do not share edits). The directory is private to you (mode 0700, file 0600). The file is small JSON that the page reads and writes; to carry a dashboard to another machine or into version control, use *Export* and `--config`.
- If the `--config` file changes after you made edits, your edits are dropped and the page says so: the file wins. *Undo my edits* removes them from disk too.
- `--no-save` writes nothing to disk and keeps edits in the browser only (as earlier versions did). The same happens, with a message at start, if the directory cannot be written.
- The dashboard is still read-only toward your cluster and the radar. The one thing it writes is this file, only from this page's own requests (the server refuses writes from any other site), only at this fixed path, at most 256 KiB, and only a layout that passes the same checks as a dashboard file.

**What is in the file, and what to do about it.** It holds your layout, each panel's settings, titles, descriptions and queries, and the time range of each board. Those are names you typed or picked, so they can name your applications, namespaces and tables: treat the file as you would your deploy list. It never holds credentials, kubeconfig data, DSNs, deploy or evaluation data, or SQL text, so it is safe to leave on your disk, but do not type secrets into a title or description, because they are stored in clear text.

- The file is 0600 in a directory created 0700. A directory that already existed keeps its own permissions, so if you point `--data-dir` at a shared or synced folder, make sure that is what you want. Exclude it from file sync or backups, or use `--no-save`, if those names must not leave your machine.
- With `--insecure-listen` the dashboard **does not save**: the page has no sign-in, so anyone who could reach it could overwrite your edits. The command says so, and edits stay in the browser.
- A file that was edited by hand, or damaged, is checked like a dashboard file before the page uses it. If it cannot be used, the page says why and shows the standard layout; the file is left alone until your next change replaces it.
- To forget everything: *Undo my edits* removes the layout from the file; deleting the file (or the data directory) removes it all. Nothing expires on its own.
- The full threat table is in the [security model](security-model.md#what-the-dashboard-saves-and-how-it-is-protected).

Every board has a **time range** (15 minutes to 90 days, or an absolute range). It is remembered per board. If the manager keeps less history than the range, the page says so rather than showing a short list as complete.

### History

The manager keeps history behind `--observe-history`:

| Setting | Behaviour |
|---|---|
| `memory` (default) | A bounded ring (`--observe-history-max-deploys`, 500). Lost on restart; the dashboard says so. |
| `postgres` | Survives restarts. Needs `--observe-history-dsn-file` (a mounted Secret holding the DSN; a DSN on the command line would show in the pod spec). |

`--observe-history-retention` (default 30 days) applies to both. Writing history never delays a decision: if the store is down the radar keeps acting and the dashboard says history is not being recorded. Only deploys in `attribution.actuate.namespaces` are served, on every endpoint including the event stream.

## Make it yours

Boards are a grid of panels, 12 columns wide, rows 40 px high.

- **Edit layout** (top right, wide screens): drag a panel by its bar, resize from the corner, move with the arrow keys (Shift+arrows to resize), hide with ×, add from the list. *Undo my edits* returns to the served layout. Edits are saved to your data directory (see *Your edits are kept between runs*).
- **Export YAML** in edit mode writes the file below. It is byte-for-byte what `config dump` prints for the same layout.
- Start from a file:

```bash
pg-regression-radar dashboard config dump > my-dashboard.yaml
pg-regression-radar dashboard config validate my-dashboard.yaml
pg-regression-radar dashboard --config my-dashboard.yaml
```

The file is read again on every page load, so editing it and reloading is enough. A file with problems is never half used: the page lists them, with line and column.

```yaml
apiVersion: pgrr.io/v1
kind: Dashboard
boards:
  - id: now
    title: Now
    range: 6h              # 1m..90d, for example 15m, 6h, 7d
    panels:
      - id: summary
        type: summary
        at: [0, 0, 12, 4]  # x, y, width, height in cells
      - id: canary
        type: canary-chart
        at: [0, 4, 8, 10]
        description: "Canary against stable for the picked deploy."   # shown by the info button, 300 characters at most
        options: {viz: chart, unit: first, zero: false}
      - id: checkout-latency
        type: canary-chart
        at: [8, 4, 4, 10]
        title: "Checkout"
        query: "app:checkout namespace:payments"   # fields per panel type: dashboard config dump --catalog
```

Rules the validator enforces: at most 12 boards and 40 panels per board; unique ids; panels may not overlap or leave the 12 columns; each panel type has a minimum size; a panel's `title` is at most 80 characters and its `description` 300; unknown fields are reported with a suggestion (`unnit` → `unit`); a `query` must parse and use only fields its panel type takes (the error gives the column), an option's value must be one the panel type lists (`dashboard config dump --catalog` prints each option's label, group, values, default and, when it applies only to one visualization, `when`). `dashboard config dump --catalog` lists the panel types, their options and sizes. A file written for an older format is migrated; one written for a newer format is refused with the version it needs.

On narrow screens (under 900 px) panels stack in order and editing is hidden.

## Add a panel type

1. **Go:** add an entry to the catalog in `internal/dashboard/layout/catalog.go`: type name, title, default and minimum size, options (each with its kind and limits, a `Label`, a `Group` and a `Doc` sentence that the settings pane shows; `viz` is the choice of visualization, `.when("viz=table")` limits an option to one of them) and `Help`, the text its info button shows (a few sentences on what it shows and how to read it).
2. **UI:** add `internal/dashboard/ui/js/panels/<type>.js` exporting `build(type, host, ctx)` that returns `{render(view)}`, and register it in `panels/registry.js`. A panel only reads the `view` it is given; it never fetches. Read its settings from `ctx.options` (defaults filled in) when it is built: a panel is built again whenever its title, description or settings change, so it never has to react to them. The settings pane needs no change.
3. Add the panel to a preset in `presets.go` if it should ship by default, then regenerate the golden files:

```bash
go test ./internal/dashboard/layout -run Golden -update
```

4. Add a case to `internal/dashboard/uitest/panels-model.test.mjs` for any logic, and check it by eye with the smoke test (`DASHBOARD_URL=… SHOTS=dir node internal/dashboard/uitest/smoke.playwright.cjs`).

An unknown type in a user's file is a validation error, not a crash; the UI shows a placeholder if the server and the page ever disagree.

## Limits

- Read-only toward the cluster: nothing in the page or the Observe API changes cluster state. The dashboard writes one thing, its own profile file in the data directory.
- The real port-forward needs a real cluster; it is covered in tests by a fake API server, and by `--url` for everything else.
- No user-written queries: panels read the datasets the Observe API serves.

## See also

- [ADR-0002: A live dashboard](adr/0002-live-dashboard.md) — why it is local, read-only toward the cluster and opened with one command.
- [Security model](security-model.md) — the dashboard's trust boundary, and how the file it saves is protected.
- [Persistence](persistence.md) — the radar's optional Postgres state store, which is separate from the dashboard's own edits file.
