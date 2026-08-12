# Installation

*How to obtain the binaries, the container image, or the Helm chart — all three are built from source today, since no packaged release artifact is published yet.*

## Overview

There is no `apt`/`brew` package, no container image on a public registry, and no published Helm chart repository yet — see [Roadmap](roadmap.md). Every install path below starts from cloning the repository (or, for the Go binaries, from Go's module proxy directly, which needs no local clone). This page covers *obtaining* pg-regression-radar; see [Getting Started](getting-started.md) for actually running it once installed.

## Prerequisites

- Go 1.22+ (to build from source)
- Docker (to build the container image)
- A Postgres cluster with `pg_stat_statements` enabled
- `kubectl` and/or `helm` (only for the Kubernetes-deployed paths)

## Option 1: Go install (fastest, no clone needed)

Every tagged release is a real, fetchable Go module version — `go install` resolves it directly from the repository's tags via the Go module proxy, no separate binary release needed:

```bash
go install github.com/joao00001/pg-regression-radar/cmd/operator@v0.1.0
go install github.com/joao00001/pg-regression-radar/cmd/manager@v0.1.0
# or @latest for the newest tagged release
```

This places `operator`/`manager` in `$(go env GOPATH)/bin`.

## Option 2: Build from source

```bash
git clone https://github.com/joao00001/pg-regression-radar.git
cd pg-regression-radar
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

See the [Dockerfile](https://github.com/joao00001/pg-regression-radar/blob/main/Dockerfile) for the other two `--target` values (`collector`, `ingester`). No image is published to a registry yet, so building locally is the only option today.

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
- [Roadmap](roadmap.md) — publishing a container image and a Helm chart repository are tracked as open operational follow-ups.
