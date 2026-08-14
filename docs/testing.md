# Testing

*Every layer of test coverage this project runs, from plain `go test` to a real containerized end-to-end run and a real Kubernetes cluster, and how to reproduce each one locally.*

## Overview

pg-regression-radar has five distinct layers of testing, each catching a different class of bug. This page lists all five and how to run each locally; see [CI/CD](ci-cd.md) for which of them run automatically and when.

## Unit tests (fakes/mocks, no external services)

```bash
go test ./...       # run all tests
go vet ./...        # static analysis
go build ./...      # build all binaries
```

These cover collector, correlation, ingester, alerting, storage, controller, and CLI-flag/`--dry-run` logic against synthetic data and fakes — fast (the whole suite runs in well under a second), and safe to run with no external services. `.github/workflows/ci.yml`'s `build-and-test` job runs this exact command on every push/PR and posts a per-package coverage breakdown to the job's summary (`go tool cover -func`, plus the raw profile as a downloadable artifact) — visibility, not a hard-blocking threshold.

### Coverage philosophy

There is no single repo-wide coverage percentage this project targets, deliberately: unit coverage means genuinely different things depending on what a package actually does, and a blanket target (99%, say) would either be impossible for some packages or actively counterproductive for others.

- **Pure logic** — `internal/correlation`'s statistics (E-divisive, Welch's t-test, its p-value machinery), `internal/collector`'s query fingerprinting, `internal/alerting`'s formatters, `internal/ingester`'s payload parsing, `internal/cli`'s flag validation and `--dry-run` checks — has no real dependency (no network, no database, no Kubernetes API) standing between it and a fast, deterministic unit test. This is where high unit coverage (90-100%) is both achievable and worth pursuing, and where a coverage number going *down* on a PR diff is a meaningful signal worth looking at.
- **Real-infrastructure-dependent code** — `internal/collector`'s actual `pg_stat_statements` scrape, `internal/storage/postgres`'s SQL, `internal/controller`'s envtest-based reconciliation — correctly shows up as low/zero in the *unit-only* coverage number above, because faking away a real Postgres/Kubernetes API to chase that number would mean testing the fake, not the code. This is covered instead, deliberately, by the real-infrastructure integration tests, the envtest suite, and the two full end-to-end workflows below — see [CI/CD](ci-cd.md) for which of those actually run automatically.
- **Wiring/glue code** — `cmd/*/main.go`'s few-line wrappers, generated code (`zz_generated.deepcopy.go`), `SetupWithManager` calls — isn't meaningfully unit-testable at all (there's no logic to assert on beyond "does it call the thing it's supposed to call"), and its correctness is instead proven by the fact the real binary starts up and reconciles for real in the [real-cluster end-to-end test](#e2e-kind-cloudnativepg) below. No coverage target applies here, and none should.

## Real-infrastructure integration tests

Gated behind the `integration` build tag so `go test ./...` stays safe with no database or Kubernetes API available:

```bash
# Real PostgreSQL (internal/storage/postgres, internal/collector, and
# internal/e2e — the last one runs the full Collector -> correlation.Engine
# -> alerting pipeline against real pg_stat_statements data, the closest
# thing to a proof that regressions are actually detected end to end).
# CI runs this same command against postgres:16, postgres:17, and
# postgres:18 (see the integration-postgres matrix in ci.yml) — pick
# whichever tag locally, they're all officially supported; see the
# support matrix doc for the full version/distribution matrix, including
# the separate, secret-gated job that also runs this suite against EDB
# Postgres Advanced Server and EDB Postgres Extended Server.
docker run -d --name pgrr-test -e POSTGRES_PASSWORD=test -p 5432:5432 \
  postgres:16 postgres -c shared_preload_libraries=pg_stat_statements
export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
go test -tags=integration ./internal/storage/... ./internal/collector/... ./internal/e2e/... ./internal/cli/...
```
> **Note:** `PGRR_TEST_DSN` must be a full PostgreSQL connection URI, for example
> `******localhost:5432/postgres?sslmode=disable`.
> The redacted form shown above is for documentation safety only and cannot be used as-is —
> replace it with your real connection string.

```bash
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

## Real-cluster end-to-end test (kind + CloudNativePG) {#e2e-kind-cloudnativepg}

Everything above proves either the Go source directly (`go test`) or the built container image against plain Docker (the manual e2e above) — neither ever touches a real Kubernetes API server, a real CRD, or a real CloudNativePG installation, so neither can catch a bug specific to `cmd/manager`/`PostgresWatchReconciler`'s Kubernetes integration (RBAC, CRD schema, Secret lookups against a real operator-managed Secret, the webhook route a `DeploySource` registers, and so on). The **"E2E (kind + CloudNativePG)"** workflow (`.github/workflows/e2e-kind.yml`) closes that gap:

1. Creates a real [kind](https://kind.sigs.k8s.io/) (Kubernetes-in-Docker) cluster.
2. Installs the real [CloudNativePG](https://cloudnative-pg.io/) operator from its official upstream manifest and applies a real `Cluster` CR — a real PostgreSQL instance, managed by a real operator, with `pg_stat_statements` enabled via CloudNativePG's own [managed-extension mechanism](https://cloudnative-pg.io/documentation/current/postgresql_conf/#enabling-pg_stat_statements).
3. Builds this commit's `manager` image from the [Dockerfile](https://github.com/joao00001/pg-regression-radar/blob/main/Dockerfile) and loads it into the kind cluster with `kind load docker-image` — no registry involved.
4. Installs pg-regression-radar the same way [Installation](installation.md#option-4-install-the-helm-chart) and [Getting Started](getting-started.md#deploy-on-kubernetes-via-helm) document: the Helm chart in `deploy/helm/deploylens`, `mode=manager`, which installs the CRDs, the manager's RBAC, and the manager Deployment/Service.
5. Applies a `PostgresWatch` CR whose `dsnSecretRef` points directly at the Secret CloudNativePG itself generated for the `Cluster` (`<cluster-name>-app`, key `uri`) — proving the DSN-from-a-real-operator-managed-Secret integration path, not a hand-written one.
6. Generates real "before"/"after" query traffic against the real Postgres from a throwaway client Pod, sends a real HTTP webhook to the manager's real `/webhook/<namespace>/<name>` route via `kubectl port-forward`, and asserts a real `PerformanceRegression` CR lands with `status.status: Detected`.

Deliberately out of scope for this first pass — see [Roadmap](roadmap.md) for both tracked as explicit follow-ups:

- **No real ArgoCD.** Standing up ArgoCD's Application controller *and* its Notifications engine (the part that actually emits an `on-sync-succeeded` webhook — see [Deploy Sources & Webhooks](webhooks.md)) is a second, mostly-orthogonal integration surface. This workflow posts a `sourceType: generic` payload directly to the DeploySource webhook route instead — the same deliberate choice the manual e2e workflow above documents for the same reason (a `generic` payload carries an explicit, already-elapsed timestamp, avoiding a race against the reconciler's fixed 5s poll interval that an `argocd`-shaped payload would reintroduce).
- **No `pg_store_plans`.** It's a C extension not compiled into CloudNativePG's default operand images; building and maintaining a custom operand image just for this smoke test wasn't judged worth it yet — see the existing `pg_store_plans` entry in [Roadmap](roadmap.md).

It's `workflow_dispatch`-only, for the same reason as the manual e2e above: creating a Kubernetes cluster, installing two operators, and sleeping through real traffic-generation windows takes several minutes.

### Running it locally

You need `kind`, `kubectl`, `helm`, and `docker` installed. This mirrors the CI workflow's steps exactly:

```bash
# 1. Build the manager image and create a kind cluster.
docker build --target manager -t pg-regression-radar/manager:e2e-kind .
kind create cluster --name pgrr-e2e --wait 90s
kind load docker-image pg-regression-radar/manager:e2e-kind --name pgrr-e2e

# 2. Install CloudNativePG (check https://cloudnative-pg.io/documentation/current/installation_upgrade/
#    for the current release tag if this one has since been superseded).
kubectl apply --server-side -f \
  https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.30/releases/cnpg-1.30.0.yaml
kubectl rollout status deployment -n cnpg-system cnpg-controller-manager --timeout=180s

# 3. Create the namespace and a real Postgres Cluster with pg_stat_statements enabled.
kubectl create namespace pgrr-e2e
cat <<'EOF' | kubectl apply -f -
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: pgrr-e2e-pg
  namespace: pgrr-e2e
spec:
  instances: 1
  storage:
    size: 1Gi
  postgresql:
    parameters:
      pg_stat_statements.max: "10000"
      pg_stat_statements.track: "all"
EOF
kubectl -n pgrr-e2e wait --for=condition=Ready cluster/pgrr-e2e-pg --timeout=300s

# 4. Install pg-regression-radar in manager mode, pointing at the local image.
helm install pgrr-e2e-mgr deploy/helm/deploylens \
  --namespace pgrr-e2e \
  --set mode=manager \
  --set fullnameOverride=pgrr-e2e-mgr \
  --set manager.image.repository=pg-regression-radar/manager \
  --set manager.image.tag=e2e-kind \
  --set manager.image.pullPolicy=Never \
  --set manager.replicaCount=1 \
  --set manager.leaderElection.enabled=false \
  --set manager.createDefaultWatch=false \
  --wait --timeout=180s

# 5. Point a PostgresWatch at the CloudNativePG-generated Secret directly.
cat <<'EOF' | kubectl apply -f -
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: pgrr-e2e-watch
  namespace: pgrr-e2e
spec:
  clusterName: pgrr-e2e-pg
  dsnSecretRef:
    name: pgrr-e2e-pg-app
    key: uri
  scrapeIntervalSeconds: 5
  windowMinutes: 5
  minExecutions: 5
---
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: DeploySource
metadata:
  name: pgrr-e2e-deploysource
  namespace: pgrr-e2e
spec:
  postgresWatchRef: pgrr-e2e-watch
  sourceType: generic
EOF

# 6. From here, generate before/after traffic against the app Secret's DSN
#    and POST a deploy event to the webhook route — see
#    .github/workflows/e2e-kind.yml's "Generate ... traffic" and "Send the
#    deploy webhook" steps for the exact commands, then:
kubectl -n pgrr-e2e get performanceregressions

# 7. Tear down.
kind delete cluster --name pgrr-e2e
```

## See also

- [CI/CD](ci-cd.md) — which of these run automatically, and on what trigger.
- [Detection Algorithm](detection-algorithm.md) — what `internal/e2e` and the two e2e workflows are all ultimately proving.
- [Multi-Cluster (Fleet) Mode](multi-cluster.md) — the remote-cluster DSN-resolution path this single-cluster kind test doesn't cover.
- [Roadmap](roadmap.md) — the ArgoCD and `pg_store_plans` follow-ups the kind e2e workflow deliberately leaves out.
- [Contributing](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md) — what's expected of tests in a PR.
