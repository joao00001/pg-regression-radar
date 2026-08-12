# Installation

*How to obtain the binaries, the container image, or the Helm chart.*

## Overview

Every tagged release (`git tag vX.Y.Z`, matching [CI/CD](ci-cd.md#releaseyml-on-pushing-a-v-tag)'s `release.yml` workflow) publishes five container images and one Helm chart to `ghcr.io/joao00001` — see [Option 3](#option-3-pull-or-build-the-container-image) and [Option 4](#option-4-install-the-helm-chart) below. Building from source (cloning the repo, or `go install` directly from the module proxy) still works exactly as before and is the only option for anything unreleased. This page covers *obtaining* pg-regression-radar; see [Getting Started](getting-started.md) for actually running it once installed.

## Prerequisites

- Go 1.22+ (to build from source)
- Docker (to build the container image)
- A Postgres cluster with `pg_stat_statements` enabled — see [Support Matrix](support-matrix.md) for officially supported PostgreSQL versions (16, 17, 18) and distributions (community, CloudNativePG, EDB Postgres Advanced/Extended Server)
- `kubectl` and/or `helm` (only for the Kubernetes-deployed paths)

## Creating a least-privilege Postgres role

pg-regression-radar only needs to read `pg_stat_statements` and a handful of catalog views — it never writes to user data. **Do not use a superuser or a role with broad privileges for `--dsn`.** If the connection string is ever leaked (e.g. via a misrouted Secret), the blast radius should be limited to read-only statistics access.

Create a dedicated role before installing pg-regression-radar:

```sql
-- Create the role with login but no superuser privileges.
CREATE ROLE pgrr_monitor WITH LOGIN PASSWORD 'change-me';

-- Built-in role that grants read access to pg_stat_statements, pg_stat_*
-- views, and other monitoring functions — exactly what pg-regression-radar needs.
GRANT pg_monitor TO pgrr_monitor;

-- If pg_stat_statements is installed in a non-public schema, also grant USAGE.
-- GRANT USAGE ON SCHEMA extensions TO pgrr_monitor;
```

Then use that role in your DSN:

```
******host:5432/mydb?sslmode=require
```

!!! warning
    Never pass a superuser or application-owner connection string. A leaked DSN with superuser access gives an attacker full control of the database; a `pg_monitor` DSN limits exposure to read-only statistics.

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

## Option 3: Pull or build the container image

**Pull a published release** (every `vX.Y.Z` tag publishes all five images — see [CI/CD](ci-cd.md#releaseyml-on-pushing-a-v-tag)):

```bash
docker pull ghcr.io/joao00001/pg-regression-radar/operator:v0.3.0
docker pull ghcr.io/joao00001/pg-regression-radar/manager:v0.3.0
docker pull ghcr.io/joao00001/pg-regression-radar/collector:v0.3.0
docker pull ghcr.io/joao00001/pg-regression-radar/ingester:v0.3.0
docker pull ghcr.io/joao00001/pg-regression-radar/cli:v0.3.0
```

Substitute the actual latest tag from the [Releases page](https://github.com/joao00001/pg-regression-radar/releases), or use `:latest` for the most recent release (there is no rolling/"edge" image — every published tag corresponds to a real `vX.Y.Z` release). `docker run ghcr.io/joao00001/pg-regression-radar/cli:v0.3.0 operator --dsn ...` runs the unified CLI image; the other four are single-mode images matching the Dockerfile's other `--target` values.

**Verify the signature and SBOM before trusting a pulled image.** Every published image is keyless-signed with [cosign](https://github.com/sigstore/cosign) using the publishing GitHub Actions run's own OIDC identity (no shared private key to leak or rotate), and carries an attached SBOM attestation:

```bash
# Verify the image was signed by this repo's release.yml workflow, specifically
# (not just "signed by someone with a GitHub account").
cosign verify ghcr.io/joao00001/pg-regression-radar/operator:v0.3.0 \
  --certificate-identity-regexp "^https://github.com/joao00001/pg-regression-radar/.github/workflows/release.yml@.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# Fetch and inspect the attached SPDX SBOM attestation.
cosign verify-attestation ghcr.io/joao00001/pg-regression-radar/operator:v0.3.0 \
  --type spdxjson \
  --certificate-identity-regexp "^https://github.com/joao00001/pg-regression-radar/.github/workflows/release.yml@.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq -r '.payload | @base64d | fromjson | .predicate'
```

Every image is also scanned with [Trivy](https://github.com/aquasecurity/trivy) at release time and the release job fails outright on any CRITICAL-severity finding (see [CI/CD](ci-cd.md#releaseyml-on-pushing-a-v-tag)) — a published image never carries a known CRITICAL CVE as of its release date, though new CVEs can of course be disclosed later against the same fixed image bytes.

**Build locally**, e.g. to test an unreleased commit:

```bash
git clone https://github.com/joao00001/pg-regression-radar.git
cd pg-regression-radar
docker build --target operator -t pg-regression-radar/operator .
docker build --target manager  -t pg-regression-radar/manager  .
```

See the [Dockerfile](https://github.com/joao00001/pg-regression-radar/blob/main/Dockerfile) for the other `--target` values (`collector`, `ingester`, and `cli` for an image running the unified `pg-regression-radar` binary).

## Option 4: Install the Helm chart

**From the published OCI chart** (published alongside the images by every release — see [CI/CD](ci-cd.md#releaseyml-on-pushing-a-v-tag)):

```bash
helm install pg-regression-radar oci://ghcr.io/joao00001/charts/pg-regression-radar \
  --version 0.3.0 \
  --set postgres.dsn="postgres://user:pass@cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

`--version` here is the chart's SemVer version (the release tag *without* its leading `v` — release `v0.3.0` publishes chart version `0.3.0`; see [CI/CD](ci-cd.md#releaseyml-on-pushing-a-v-tag) for why the two differ by that one character). No `helm repo add` is needed for an OCI registry — `helm install`/`helm pull` reference `oci://` URLs directly. The chart's `image.tag` values default to the chart's own `appVersion` (the release tag *with* the `v`, e.g. `v0.3.0`), which already matches the images published in the same release, so you don't need to set `image.tag`/`manager.image.tag` explicitly unless you want to run a different image tag than the chart's own release.

**From a local clone**, e.g. to test unreleased chart changes:

```bash
git clone https://github.com/joao00001/pg-regression-radar.git
helm install pg-regression-radar ./pg-regression-radar/deploy/helm/deploylens \
  --set postgres.dsn="postgres://user:pass@cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

See [Getting Started](getting-started.md#deploy-on-kubernetes-via-helm) for the full set of values, including switching to `manager` mode.

## See also

- [Getting Started](getting-started.md) — running each of these once installed.
- [Configuration Reference](configuration.md) — every flag and CRD field.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions and distributions.
- [CI/CD](ci-cd.md) — how `release.yml` builds, scans, signs, and publishes every image and the Helm chart.
- [Roadmap](roadmap.md) — remaining operational follow-ups.
