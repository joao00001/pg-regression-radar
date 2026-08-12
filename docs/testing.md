# Testing

*Every layer of test coverage this project runs, from plain `go test` to a real containerized end-to-end run, and how to reproduce each one locally.*

## Overview

pg-regression-radar has four distinct layers of testing, each catching a different class of bug. This page lists all four and how to run each locally; see [CI/CD](ci-cd.md) for which of them run automatically and when.

## Unit tests (fakes/mocks, no external services)

```bash
go test ./...       # run all tests
go vet ./...        # static analysis
go build ./...      # build all binaries
```

These cover collector, correlation, ingester, alerting, storage, and controller logic against synthetic data and fakes — fast, and safe to run with no external services.

## Real-infrastructure integration tests

Gated behind the `integration` build tag so `go test ./...` stays safe with no database or Kubernetes API available:

```bash
# Real PostgreSQL (internal/storage/postgres, internal/collector, and
# internal/e2e — the last one runs the full Collector -> correlation.Engine
# -> alerting pipeline against real pg_stat_statements data, the closest
# thing to a proof that regressions are actually detected end to end):
docker run -d --name pgrr-test -e POSTGRES_PASSWORD=test -p 5432:5432 \
  postgres:16 postgres -c shared_preload_libraries=pg_stat_statements
export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
go test -tags=integration ./internal/storage/... ./internal/collector/... ./internal/e2e/...

# Real kube-apiserver + etcd (internal/controller):
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
export KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 -p path)"
go test ./internal/controller/... -run TestEnvtest -v
```

### Why `internal/e2e` exists

Every *other* test in this project stops short of proving the full detection pipeline works:

- `internal/correlation`'s tests feed the engine hand-built `QuerySample` slices, so they validate the statistics but never touch a real Collector or a real Postgres.
- `internal/collector`'s integration test proves `scrape()` reads `pg_stat_statements` correctly, but stops there — it never feeds those samples into `correlation.Engine`.
- `internal/controller`'s tests (fake client and envtest) deliberately use an unreachable DSN so no scrape ever fires; they prove the Kubernetes reconciliation state machine, not detection.

`internal/e2e/pipeline_integration_test.go` closes that gap: it drives a real Collector against a real Postgres, feeds the real samples into a real `correlation.Engine`, and asserts a real HTTP POST fires from `alerting.WebhookNotifier` — with a control query that must *not* be flagged, so the test can't pass just by flagging everything indiscriminately after any deploy.

## Manual end-to-end smoke test (real container) {#manual-e2e-real-container}

None of the tests above build the Docker image or run it as a container — they exercise Go source directly. The **"Manual E2E (real container)"** workflow (`.github/workflows/e2e-manual.yml`) does: it builds the operator image from the [Dockerfile](https://github.com/joao00001/pg-regression-radar/blob/main/Dockerfile), runs it as a real container against a real PostgreSQL container (`pg_stat_statements` preloaded), generates real before/after query traffic, POSTs a real deploy webhook to the running container's `/webhook` endpoint, and asserts a real alert HTTP request lands on a mock receiver — the operator treated as an opaque black box, the same way it runs in Kubernetes.

It runs as two jobs (`build-image`, then `e2e`) so a failure to build the image and a failure to detect the regression show up as two distinct, independently-inspectable results in the Actions UI, with the image crossing the job boundary via `docker save`/`upload-artifact` and `download-artifact`/`docker load`.

It's `workflow_dispatch`-only (not on every push/PR, since it spins up several containers and sleeps through real wall-clock windows): trigger it from the **Actions** tab -> "Manual E2E (real container)" -> **Run workflow**.

To build the image locally:

```bash
docker build --target operator -t pg-regression-radar/operator .
docker build --target manager  -t pg-regression-radar/manager  .
```

It deliberately uses `--source-type=generic` with an explicit, already-elapsed deploy timestamp rather than `--source-type=argocd`: `cmd/operator`'s ingester-poll loop analyses a deploy event exactly once, on the first 5s poll tick that observes it, with no retry. An `argocd`-shaped webhook always stamps the deploy timestamp at receipt time, which would race that fixed poll interval against however much "after" traffic had been generated so far. Recording the timestamp after both traffic phases have already happened removes that race entirely.

## See also

- [CI/CD](ci-cd.md) — which of these run automatically, and on what trigger.
- [Detection Algorithm](detection-algorithm.md) — what `internal/e2e` and the manual e2e workflow are both ultimately proving.
- [Contributing](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md) — what's expected of tests in a PR.
