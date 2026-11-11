# Config and Secrets

*How to store the Postgres DSN in a Secret, what the consent label does, and how to rotate a credential.*

## Overview

The radar needs one credential above all: the connection string (DSN) for the Postgres it watches. It may also hold a webhook token, alert destinations and an OTLP header. Kubernetes has two objects for this kind of data, ConfigMaps and Secrets. This page explains the difference, how a pod receives either, and the exact steps to give the radar its DSN safely.

## ConfigMaps and Secrets

Both are objects holding key/value pairs that live outside the container image. A **ConfigMap** is for non-sensitive settings; a **Secret** is for credentials and tokens. They are used the same way, but the cluster treats Secrets more carefully: their values are hidden from `kubectl describe`, access to them is a separate RBAC permission, and they can be encrypted at rest if the cluster is configured for it.

!!! warning "Base64 is not encryption"
    Values in a Secret are stored base64-encoded, which is only a text encoding. Anyone who can `kubectl get secret -o yaml` can read the plaintext. The protection of a Secret is **who is allowed to read it**, not how it looks. Keep Secret read access narrow, and never commit a Secret manifest with real values to git.

```bash
kubectl get secrets -n <ns>
kubectl get secret postgres-credentials -n <ns> -o jsonpath='{.data.dsn}' | base64 -d
```

## How a pod receives them

A pod gets Secret and ConfigMap values in one of two ways:

| Way | What it looks like | When values update |
|---|---|---|
| **Environment variable** from a key | `env: [{name: POSTGRES_DSN, valueFrom: {secretKeyRef: {name: ..., key: dsn}}}]` | only when the pod restarts |
| **Mounted as a file** | a volume appears as files in a directory | the file updates in place, after a delay |

The radar's `operator` mode takes its Secret values as **environment variables** (`POSTGRES_DSN`, `SLACK_WEBHOOK_URL`, and so on, filled from the chart's Secret). This has one consequence you must remember: **changing a Secret does not change a running pod's environment**. After you update a Secret that `operator` mode reads, restart it:

```bash
kubectl rollout restart deployment/<fullname> -n <ns>
kubectl rollout status deployment/<fullname> -n <ns>
```

`manager` mode is different: it reads the Secret **through the Kubernetes API** when it reconciles a `PostgresWatch` (the `dsnSecretRef` field), so it does not depend on the pod's environment for the DSN.

## Create the DSN Secret

The DSN is a standard PostgreSQL connection URL. Use a **least-privilege role**, never a superuser; the role setup is in [Installation](../installation.md#creating-a-least-privilege-postgres-role).

```bash
kubectl create secret generic orders-dsn -n <ns> \
  --from-literal=dsn='postgres://pgrr_monitor:change-me@orders-rw.<ns>.svc.cluster.local:5432/orders?sslmode=require'
```

Two details worth getting right:

- **Single-quote the value.** Passwords containing `$`, `!` or `&` are mangled by the shell inside double quotes.
- **Special characters in the password must be percent-encoded in the URL** (`@` becomes `%40`, `/` becomes `%2F`). A DSN that works in `psql` with a keyword string may fail as a URL for this reason.

!!! tip "Keep the password out of your shell history"
    `--from-literal` leaves the value in your shell history. Prefer `--from-file=dsn=./dsn.txt` with a file you delete afterwards, or create the Secret from your secret manager's tooling.

### Applying it from a manifest

If you manage Secrets as manifests, use `stringData` so you can write plain text and let Kubernetes encode it:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: orders-dsn
  namespace: <ns>
  labels:
    pg-regression-radar.io/allow-postgreswatch-access: "true"
type: Opaque
stringData:
  dsn: postgres://pgrr_monitor:change-me@orders-rw.<ns>.svc.cluster.local:5432/orders?sslmode=require
```

## The consent label

In `manager` mode the radar's ServiceAccount can read Secrets cluster-wide, so that it can resolve any `PostgresWatch`'s `dsnSecretRef`. To stop that power being used on someone else's behalf, **the radar refuses to use a Secret unless its owner has labelled it**:

```bash
kubectl label secret orders-dsn -n <ns> \
  pg-regression-radar.io/allow-postgreswatch-access=true
```

Without that label the `PostgresWatch` ends up in `status.phase: Failed`, with the reason in `status.message`:

```bash
kubectl get postgreswatch orders -n <ns> -o jsonpath='{.status.phase}{"\n"}{.status.message}{"\n"}'
```

Labelling a Secret needs the same permission as editing it, so only the people who own the Secret can opt it in. The reasoning, and the admission policies that restrict who may set the label in shared clusters, are in [RBAC and access](rbac.md#the-consent-label).

!!! note "In pg-regression-radar"
    The Helm chart creates its own Secret, `<fullname>-secret`, with the consent label already set, holding the keys `dsn`, `slackWebhookUrl`, `alertUrl`, `stateDsn`, and, only when you set them, `pagerDutyRoutingKey`, `alertTemplate`, `otlpHeaders` and `webhookSecret`. If you create a `PostgresWatch` yourself, the Secret and its label are yours to create.

## Pointing a PostgresWatch at the Secret

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: orders
  namespace: <ns>
spec:
  clusterName: orders
  dsnSecretRef:
    name: orders-dsn      # the Secret's name, in the SAME namespace
    key: dsn              # the key inside it
```

The Secret must be in the `PostgresWatch`'s own namespace (see [Objects and namespaces](objects.md#why-namespaces-matter-for-the-radar)). Prefer `dsnSecretRef` over putting the DSN directly in the spec: a plain DSN in a `PostgresWatch` shows up in `kubectl get postgreswatch -o yaml` and in anyone's `describe` output.

## Rotating a credential

1. Change the password in Postgres (or create the new role).
2. Update the Secret: `kubectl create secret generic orders-dsn -n <ns> --from-literal=dsn='...' --dry-run=client -o yaml | kubectl apply -f -`.
3. `operator` mode: `kubectl rollout restart deployment/<fullname> -n <ns>`. `manager` mode: nothing to restart. The manager re-reads the Secret on every reconcile (about every 30 seconds for a running watch) and, because the DSN is part of what it compares, restarts that watch's worker on its own when the credential changed.

The pipe in step 2 is the standard way to *update* a Secret from literals: `create --dry-run=client -o yaml` renders the manifest and `apply` applies it, so it works whether or not the Secret exists.

## Webhook authentication

If you protect the deploy-event endpoint with a token, the sender must send it in an `X-Webhook-Token` header, and the radar compares it with the configured secret. With the chart, set `ingester.webhookSecret`; it lands in the chart's Secret under `webhookSecret`. Leaving it empty leaves `POST /webhook` open to anyone who can reach the Service, which is acceptable only while the Service stays cluster-internal. See [Deploy Sources & Webhooks](../webhooks.md#webhook-authentication).

## See also

- [RBAC and access](rbac.md): why Secret read access and the consent label exist.
- [Installation](../installation.md#creating-a-least-privilege-postgres-role): the Postgres role to put in the DSN.
- [Security Model](../security-model.md): the full threat model for credentials.
- [Multi-Cluster (Fleet) Mode](../multi-cluster.md): reading the DSN Secret from a different cluster.
