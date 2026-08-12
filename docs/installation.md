# Installation

*How to obtain the binaries, the container image, or the Helm chart — all three are built from source today, since no packaged release artifact is published yet.*

## Overview

There is no `apt`/`brew` package, no container image on a public registry, and no published Helm chart repository yet — see [Roadmap](roadmap.md). Every install path below starts from cloning the repository (or, for the Go binaries, from Go's module proxy directly, which needs no local clone). This page covers *obtaining* pg-regression-radar; see [Getting Started](getting-started.md) for actually running it once installed.

## Prerequisites

- Go 1.22+ (to build from source)
- Docker (to build the container image)
- A Postgres cluster with `pg_stat_statements` enabled — see [Support Matrix](support-matrix.md) for officially supported PostgreSQL versions (16, 17, 18) and distributions (community, CloudNativePG, EDB Postgres Advanced/Extended Server)
- `kubectl` and/or `helm` (only for the Kubernetes-deployed paths)

## Option 1: Go install — one command, one binary (recommended)

The simplest way to get pg-regression-radar: a single unified CLI, `pg-regression-radar`, with each run mode as a subcommand. No clone needed — `go install` resolves the tagged release directly from the repository's tags via the Go module proxy:

```bash
go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest
# or pin a release: @v0.1.0
```

This places a single `pg-regression-radar` binary in `$(go env GOPATH)/bin`. From there:

```bash
pg-regression-radar operator --dsn "postgres://user:pass@host:5432/dbname?sslmode=disable"
pg-regression-radar manager --leader-elect=true
pg-regression-radar collector --dsn "..."
pg-regression-radar ingester --source-type argocd
pg-regression-radar version
pg-regression-radar help
```

Every subcommand accepts its own `--help`, `--version`, and (except `manager`) `--dry-run` — see [Configuration Reference](configuration.md#versioning-dry-run).

If you'd rather have four separate binaries (e.g. to match the container images described in Option 3), the same code is also available as four standalone commands:

```bash
go install github.com/joao00001/pg-regression-radar/cmd/operator@latest
go install github.com/joao00001/pg-regression-radar/cmd/manager@latest
go install github.com/joao00001/pg-regression-radar/cmd/collector@latest
go install github.com/joao00001/pg-regression-radar/cmd/ingester@latest
```

Both forms run identical code (`internal/cli`) — `pg-regression-radar operator ...` and the standalone `operator ...` behave the same way.

## Option 2: Build from source

```bash
git clone https://github.com/joao00001/pg-regression-radar.git
cd pg-regression-radar
go build -o bin/pg-regression-radar ./cmd/pg-regression-radar
# or the four standalone binaries individually:
go build -o bin/operator  ./cmd/operator
go build -o bin/manager   ./cmd/manager
go build -o bin/collector ./cmd/collector
go build -o bin/ingester  ./cmd/ingester
```

Useful if you want to build against an unreleased commit, or need the CRD manifests and Helm chart alongside the binaries (both live in the same clone — see options 3 and 4).

## Option 3: Build the container image

```bash
git clone https://github.com/joao00001/pg-regression-radar.git
cd pg-regression-radar
docker build --target operator -t pg-regression-radar/operator .
docker build --target manager  -t pg-regression-radar/manager  .
```

See the [Dockerfile](https://github.com/joao00001/pg-regression-radar/blob/main/Dockerfile) for the other `--target` values (`collector`, `ingester`, and `cli` for an image running the unified `pg-regression-radar` binary, e.g. `docker run pg-regression-radar/cli operator --dsn ...`). No image is published to a registry yet, so building locally is the only option today.

## Option 4: Install the Helm chart

```bash
git clone https://github.com/joao00001/pg-regression-radar.git
helm install pg-regression-radar ./pg-regression-radar/deploy/helm/deploylens \
  --set postgres.dsn="postgres://user:pass@cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

There is no Helm chart repository to `helm repo add` yet — install directly from the cloned chart directory, as above. See [Getting Started](getting-started.md#deploy-on-kubernetes-via-helm) for the full set of values, including switching to `manager` mode.

## See also

- [Getting Started](getting-started.md) — running each of these once installed.
- [Configuration Reference](configuration.md) — every flag and CRD field.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions and distributions.
- [Roadmap](roadmap.md) — publishing a container image and a Helm chart repository are tracked as open operational follow-ups.
