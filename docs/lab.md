# Try it in five minutes (lab)

The fastest way to see pg-regression-radar do its job: a throwaway lab that runs in any Kubernetes cluster (kind, minikube, k3d, Docker Desktop) and needs nothing but `kubectl`. No Postgres of your own, no ArgoCD, no Slack, no credentials, no `helm`.

The story it plays out:

1. A small app, `orders-api`, runs the same lookup query against a Postgres database about ten times a second. The radar learns what "normal" looks like.
2. You ship **version 2** of `orders-api`. Its database migration drops an index. The code and the queries are unchanged, but every lookup now scans the whole table.
3. The radar notices the slowdown, ties it to that rollout, and tells you what changed in the query plan: `Index Scan -> Seq Scan`.

Everything lives in the `pgrr-lab` namespace and is deleted with it. The manifests are in [`examples/lab`](https://github.com/joao00001/pg-regression-radar/tree/main/examples/lab).

## Before you start

You need a cluster you can throw things at and `kubectl` pointed at it. If you have none, [kind](https://kind.sigs.k8s.io/) is the quickest:

```bash
kind create cluster --name pgrr-lab
```

The lab pulls `postgres:16`, `python:3.12-alpine` and the radar's `manager` image from `ghcr.io`, so the cluster needs internet access. About 1 CPU and 1.5 GiB of memory are enough.

## Step 1: install the radar

```bash
kubectl apply -f https://raw.githubusercontent.com/joao00001/pg-regression-radar/main/examples/lab/00-radar.yaml

kubectl wait --for=condition=Established --timeout=60s \
  crd/postgreswatches.radar.pgregressionradar.io \
  crd/deploysources.radar.pgregressionradar.io \
  crd/performanceregressions.radar.pgregressionradar.io

kubectl -n pgrr-lab rollout status deployment/pgrr-manager
```

This creates the namespace, the radar's custom resources (CRDs), its permissions and the radar itself, running in `manager` mode. The `kubectl wait` matters: the next step creates objects of the new types, which only works once the CRDs are registered.

## Step 2: start the lab

```bash
kubectl apply -f https://raw.githubusercontent.com/joao00001/pg-regression-radar/main/examples/lab/10-lab.yaml

kubectl -n pgrr-lab wait --for=condition=Available deployment --all --timeout=300s
kubectl -n pgrr-lab get postgreswatch,deploysource
```

This starts Postgres (seeded with 400,000 orders and an index on `orders.order_ref`), the `orders-api` workload, and `alert-sink`, a small receiver that prints the alerts the radar sends, in place of Slack. It also tells the radar what to watch: a `PostgresWatch` that scrapes the database every 5 seconds, and a `DeploySource` that treats every rollout of the `orders-api` Deployment as a deploy.

**Wait about three minutes now.** The radar has to see the query running normally before a change means anything. (It also records the first rollout of `orders-api` as a deploy; waiting lets that one expire so it cannot be mistaken for the bad deploy below.)

In a second terminal, follow the alerts:

```bash
kubectl -n pgrr-lab logs -f deployment/alert-sink
```

## Step 3: ship the bad deploy

```bash
kubectl apply -f https://raw.githubusercontent.com/joao00001/pg-regression-radar/main/examples/lab/20-bad-deploy.yaml

kubectl -n pgrr-lab get performanceregressions -w
```

Version 2 rolls out, its migration drops the index, and within about a minute a `PerformanceRegression` appears with status `Detected`. Look at it:

```bash
kubectl -n pgrr-lab describe performanceregressions
```

The fields to look at in `Status`: `Latency Change Factor` (how many times slower), `Confidence Score`, `P Value`, and `Plan Diff Summary`, which names the plan change. In the other terminal, the alert arrives, in the same shape it would have in Slack:

```text
================================================================
:rotating_light: *pg-regression-radar* — performance regression detected on cluster *orders*
  Deploy Event     k8s-Deployment-pgrr-lab-orders-api-orders-api-2
  Query (excerpt)  SELECT id, total FROM orders WHERE order_ref = $1
  Latency Before   0.05 ms
  Latency After    18.00 ms
  Change Factor    360.00x
  Plan Diff        plan diff for queryid ...:
      root node type changed: "Index Scan" -> "Seq Scan"
```

(The numbers depend on your machine; the shape does not.)

## Undo and run it again

```bash
kubectl -n pgrr-lab rollout undo deployment/orders-api
```

Version 1's migration recreates the index and the query is fast again. To repeat the demo, wait three minutes so the radar sees the healthy baseline again, then re-apply step 3.

## Clean up

```bash
kind delete cluster --name pgrr-lab      # if you used kind
# otherwise:
kubectl delete -f https://raw.githubusercontent.com/joao00001/pg-regression-radar/main/examples/lab/00-radar.yaml
```

## If something looks wrong

| Symptom | Check |
|---|---|
| A pod stays `Pending` or `ImagePullBackOff` | `kubectl -n pgrr-lab describe pod <name>`: the cluster needs to reach `ghcr.io` and Docker Hub. |
| `postgreswatch` never reaches `Running` | `kubectl -n pgrr-lab describe postgreswatch orders` and `kubectl -n pgrr-lab logs deployment/pgrr-manager`. |
| Step 2 fails with "no matches for kind PostgresWatch" | Step 1's `kubectl wait` did not finish; re-run step 2. |
| No regression after five minutes | You applied step 3 before the baseline was learned, or Postgres was not ready. Check `kubectl -n pgrr-lab get pods`, undo, wait three minutes and repeat. |
| The plan diff says `Gather` instead of `Seq Scan` | Only if you changed the Postgres settings: the lab turns parallel query off so the plan stays readable. |

## What is in the manifests

| File | What it is |
|---|---|
| `00-radar.yaml` | Namespace, CRDs, permissions and the manager. Generated from the Helm chart by `scripts/build_lab.sh`; do not edit by hand. |
| `10-lab.yaml` | Postgres, `orders-api` v1, `alert-sink`, the `PostgresWatch` and the `DeploySource`. |
| `20-bad-deploy.yaml` | `orders-api` v2: identical to v1 except that its migration drops the index. |

The deploy is detected with `sourceType: kubernetes` (see [Webhooks](webhooks.md#native-kubernetes-watch-no-webhook)): the radar watches the Deployment itself, so the lab needs no webhook sender. The settings in the `PostgresWatch` (5 second scrapes, three samples) are sized for a demo; the defaults are meant for production traffic.

When you are ready for your own database, continue with [Installation](installation.md) and [Getting Started](getting-started.md).
