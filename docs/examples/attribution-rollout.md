# Example: let the radar pause a bad rollout

*A copy-and-adapt setup for the attribution engine in enforce mode: the radar watches your database, judges each deploy of an Argo Rollouts app, and pauses the rollout when a deploy slows queries down. Read the [Attribution engine](../attribution-engine.md) first for what the grades mean.*

## Before you start

- Postgres with `pg_stat_statements`, scraped by the watch's own collector (a Prometheus sample source is not enough for this engine).
- A `DeploySource` of type `argo-rollouts` that reports your Rollout's deploys ([Deploy Sources & Webhooks](../webhooks.md)).
- Updated CRDs ([Upgrading](../upgrading.md), step 1).
- Time: the engine needs about 45 minutes of history after the manager starts before it can pause, and about 15 minutes after a deploy to convict it.

## 1. Start in shadow mode

Shadow mode records what the engine would do and never acts. Run it for a while and compare its decisions with what actually happened.

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: shop-db
  namespace: shop
spec:
  clusterName: shop-db
  dsnSecretRef:        # adapt to your Secret
    name: shop-db-dsn
    key: dsn
  attribution:
    enabled: true
    mode: shadow
```

Look for `attribution (shadow): decision, not executed` in the manager log, with `wanted: hold` on the deploys you consider bad.

## 2. Let the manager pause

Grant the Rollout RBAC in the chart, then switch the watch to enforce and turn actuation on, limited to the namespaces you choose:

```yaml
# Helm values
manager:
  autoAbort:
    rbacEnabled: true   # grants get and patch on rollouts and rollouts/status
```

```yaml
spec:
  attribution:
    enabled: true
    mode: enforce
    actuate:
      enabled: true
      namespaces: [shop]
```

## 3. Give the Rollout time to be judged

The engine cannot convict before about 15 minutes after a deploy, so the canary must not be promoted before that:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  name: checkout
  namespace: shop
spec:
  strategy:
    canary:
      steps:
        - setWeight: 20
        - pause: {duration: 25m}   # long enough for the engine to judge
        - setWeight: 50
        - pause: {duration: 10m}
```

## 4. See what it did

```bash
# Every pause or abort, newest first (port-forward the verdict port first)
curl -s http://127.0.0.1:8082/api/v1/attribution/actions | jq .

# The Rollout it paused
kubectl -n shop get rollout checkout -o jsonpath='{.spec.paused}'
```

A person decides what happens next: `kubectl argo rollouts promote checkout` to continue, or `kubectl argo rollouts abort checkout` to stop it.

## Stopping it

```bash
kubectl -n shop annotate postgreswatch shop-db pg-regression-radar.io/actuation-paused=true
```

Remove the annotation (`...actuation-paused-`) to resume. No restart is needed.

## If it does not act

See [The engine only alerts and does not act](../troubleshooting/enforce.md#the-engine-only-alerts-and-does-not-act).
