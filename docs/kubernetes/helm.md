# Helm basics

*Charts, releases and values, and the Helm commands to install, inspect, upgrade and remove the radar.*

## Overview

Helm is the package manager for Kubernetes and the supported way to install the radar. A **chart** is a bundle of templated manifests; a **release** is one installation of a chart in a cluster; **values** are the settings you give it. Instead of writing a dozen manifests by hand, you set a few values and Helm renders and applies the whole set. This page covers the vocabulary and the commands that matter, and the details specific to this chart.

## Vocabulary

| Term | Meaning | In this project |
|---|---|---|
| **Chart** | a directory (or OCI artifact) of templates plus default `values.yaml` | `deploy/helm/deploylens`, or `oci://ghcr.io/joao00001/charts/pg-regression-radar` |
| **Release** | a named installation of a chart in a namespace | whatever name you pass to `helm install`; objects are named from it |
| **Values** | settings that fill the templates | `mode`, `postgres.dsn`, `alerting.*`, `manager.replicaCount`, ... |
| **Revision** | a numbered version of a release, created on every install, upgrade and rollback | `helm history` lists them |

The release name matters because it ends up in object names. The chart builds `<fullname>` from the release name, adding the chart name unless the release name already contains it, so `helm install radar ...` gives objects like `radar-deploylens-manager`. `kubectl get deploy -n <ns>` shows the real names.

## Install

```bash
helm install radar oci://ghcr.io/joao00001/charts/pg-regression-radar \
  --version <version> \
  --namespace <ns> --create-namespace \
  --set mode=manager \
  --set postgres.dsn='postgres://pgrr_monitor:change-me@orders-rw.<ns>.svc.cluster.local:5432/orders?sslmode=require' \
  --set postgres.clusterName=orders
```

| Part | Meaning |
|---|---|
| `radar` | the release name |
| `oci://...` | the chart. An OCI registry needs no `helm repo add`. A local clone works too: `./deploy/helm/deploylens`. |
| `--version` | the chart's SemVer, which is the release tag **without** the leading `v` ([Installation](../installation.md#option-4-install-the-helm-chart)) |
| `--namespace`, `--create-namespace` | where the release lives; create it if it does not exist |
| `--set key=value` | one value, from the command line |

Unlike `kubectl apply`, `helm install` **fails** if the release name already exists in that namespace. To install or upgrade with one command, use `helm upgrade --install`.

## Values: `--set` versus a file

For more than two or three settings, put them in a file:

```yaml
# my-values.yaml
mode: manager
postgres:
  clusterName: orders
  scrapeIntervalSeconds: 60
manager:
  replicaCount: 2
alerting:
  slackWebhookUrl: https://hooks.slack.com/services/XXX/YYY/ZZZ
```

```bash
helm upgrade --install radar oci://ghcr.io/joao00001/charts/pg-regression-radar \
  --version <version> -n <ns> --create-namespace -f my-values.yaml \
  --set postgres.dsn="$PGRR_DSN"          # keep secrets out of the file
```

Values from later `-f` files and from `--set` override earlier ones, and everything you do not set keeps the chart's default. To see **every** value the chart accepts, with comments:

```bash
helm show values oci://ghcr.io/joao00001/charts/pg-regression-radar --version <version>
```

!!! warning "Values you pass on the command line are stored in the release"
    Helm stores the merged values in a Secret in the cluster (`sh.helm.release.v1...`) so it can roll back. Anyone who can read Secrets in that namespace can read your DSN from it. This is one more reason to keep Secret access narrow, and why the chart's own Secret, `<fullname>-secret`, is what the pods actually read.

## See what you would apply: `template`

`helm template` renders the manifests locally and prints them. It changes nothing, and it is the cheapest way to check a values file before you apply it:

```bash
helm template radar ./deploy/helm/deploylens -f my-values.yaml | less
helm template radar ./deploy/helm/deploylens -f my-values.yaml | kubectl apply --dry-run=server -f -
helm lint ./deploy/helm/deploylens -f my-values.yaml
```

The `--dry-run=server` variant sends the rendered manifests to the API server for validation without creating anything.

## Inspect a running release

```bash
helm list -n <ns>                      # releases and their status
helm status radar -n <ns>              # status, plus the chart's own NOTES
helm get values radar -n <ns>          # the values YOU set
helm get values radar -n <ns> --all    # the merged values, defaults included
helm get manifest radar -n <ns>        # the objects Helm applied
helm history radar -n <ns>             # revisions
```

`helm get values` is the first command to run when someone asks "how is this installed?" It answers without guessing.

## Upgrade and roll back

```bash
helm upgrade radar oci://ghcr.io/joao00001/charts/pg-regression-radar \
  --version <new-version> -n <ns> --reuse-values
helm rollback radar 3 -n <ns>          # back to revision 3
```

Two details are easy to get wrong:

- **`--reuse-values` versus `-f`.** `--reuse-values` keeps the values from the last release and ignores new defaults in the chart, which can hide new options. Passing your `-f my-values.yaml` every time is more predictable: your file is the source of truth.
- **Rollback restores the release's objects, not the CRDs.** See the next section.

The chart's `image.tag` defaults to the chart's own `appVersion`, so upgrading the chart upgrades the radar image with it, unless you pinned a tag.

### CRDs and Helm

Helm applies the chart's `crds/` directory **only on `helm install`**. It never updates or deletes it. That is deliberate Helm behaviour, because CRDs are cluster-wide and changing them can affect every object of that kind. Three consequences for the radar:

- After `helm upgrade` to a chart with a changed CRD schema, apply the new definitions yourself: `kubectl apply -f deploy/helm/deploylens/crds/`.
- `helm uninstall` leaves the CRDs, and therefore your `PerformanceRegression` history, in the cluster.
- In `operator` mode, no CRDs are used at all.

See [CRDs and operators](crds-operators.md#installing-and-upgrading-the-definitions).

## Uninstall

```bash
helm uninstall radar -n <ns>
```

This removes everything the release created: the Deployment, Service, Secret, ServiceAccount and RBAC objects. It does **not** remove the namespace you created, and it does not remove the CRDs or the custom resources you created yourself (including any `PostgresWatch` you applied separately).

## Mode and the objects it creates

The single most important value is `mode`. It decides what Helm creates:

| `mode` | Creates | Notable |
|---|---|---|
| `operator` (default) | a Deployment, a Service, a ServiceAccount, the Secret | configured by flags and environment; no CRDs, no cluster-scoped objects |
| `manager` | a Deployment (`manager.replicaCount`, default 2), a Service, ServiceAccount, ClusterRole and binding, a leader-election Role, the CRDs, the Secret, and unless `manager.createDefaultWatch=false`, a `PostgresWatch` and a `DeploySource` | production mode; see [Architecture Overview](../architecture.md#two-ways-to-run-pg-regression-radar) |

Optional guard rails (`networkPolicy.enabled`, `resourceQuota.enabled`, `limitRange.enabled`) are off by default and described in [Hardening (Helm)](../deployment-security.md).

## Troubleshooting Helm itself

| Message | Meaning and fix |
|---|---|
| `cannot re-use a name that is still in use` | a release with that name exists. Use `helm upgrade --install`, or pick another name. |
| `Kubernetes cluster unreachable` | `kubectl` has no working context; check `kubectl config current-context`. |
| `rendered manifests contain a resource that already exists` | an object of the same name exists that Helm does not own, often left from a manual `kubectl apply`. Delete it or rename the release. |
| `no matches for kind "PostgresWatch"` | the CRDs are not installed (Helm skips `crds/` on upgrade). Apply them, then retry. |
| `UPGRADE FAILED: another operation is in progress` | an earlier Helm command was interrupted. `helm history` shows a `pending-*` state; `helm rollback` to the last good revision. |

## See also

- [Installation](../installation.md#option-4-install-the-helm-chart): the published chart, signatures and versions.
- [Getting Started](../getting-started.md#deploy-on-kubernetes-via-helm): `operator` and `manager` mode installs.
- [Hardening (Helm)](../deployment-security.md): security values.
- [CRDs and operators](crds-operators.md): what `manager` mode adds to the cluster.
