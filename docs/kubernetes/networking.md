# Services and networking

*How a webhook sender reaches the radar, how the radar reaches Postgres, and how to test from your laptop.*

## Overview

Pod IPs change every time a pod is replaced, so nothing in Kubernetes talks to a pod IP directly. Instead, traffic goes to a **Service**, a stable name and virtual IP that forwards to whichever pods currently match it. For the radar there are two connections to get right: Postgres to radar (the radar opens a connection to your database) and deploy sender to radar (ArgoCD, Flux, a CI job or an OpenTelemetry Collector posts events to the radar). This page covers both, plus `kubectl port-forward` for testing and NetworkPolicy for locking it down.

## Services

A Service has a **selector** (which pods it targets, by label), **ports** and a **type**:

| Type | Reachable from | Use |
|---|---|---|
| `ClusterIP` (default) | inside the cluster only | everything the radar exposes, by default |
| `NodePort` | every node's IP on a high port | quick tests on bare clusters |
| `LoadBalancer` | outside, through a cloud load balancer | exposing the webhook beyond the cluster |

```bash
kubectl get service -n <ns>
kubectl describe service <fullname>-manager -n <ns>     # check the Endpoints line
kubectl get endpoints <fullname>-manager -n <ns>        # which pod IPs are behind it
```

!!! tip "Empty endpoints means the selector matches nothing"
    If `Endpoints` is `<none>`, the Service exists but sends traffic nowhere: either no pod matches its selector (a label mismatch) or no matching pod is Ready. This is the first thing to check when "the webhook times out".

### What the radar exposes

In `manager` mode, the Service `<fullname>-manager` has three ports:

| Port name | Default | What it serves |
|---|---|---|
| `webhook` | `8080` | deploy events: one route per `DeploySource`, at `/webhook/<namespace>/<name>` |
| `pg-metrics` | `9090` | Prometheus metrics derived from `pg_stat_statements` |
| `ctrl-metrics` | `8443` | controller-runtime's own reconcile and queue metrics |

The health probes (`8081`) are not on the Service: the kubelet probes the pod directly. In `operator` mode, the Service `<fullname>` exposes `webhook` and `metrics` on `service.webhookPort` and `service.metricsPort`. The chart's default `service.type` is `ClusterIP`.

## DNS: how names resolve

Every Service gets a DNS name inside the cluster:

```
<service>.<namespace>.svc.cluster.local
```

Within the **same namespace** the short name `<service>` is enough; from another namespace use `<service>.<namespace>`. The full form always works.

This is how you tell things where the radar is:

```text
# a deploy sender in any namespace posting to the radar
http://<fullname>-manager.<ns>.svc.cluster.local:8080/webhook/<ns>/<deploysource-name>

# the radar's DSN pointing at a Postgres Service
postgres://pgrr_monitor:...@orders-rw.<ns>.svc.cluster.local:5432/orders
```

For CloudNativePG, the read-write endpoint is the Service named `<cluster>-rw` (and `-ro`, `-r` for replicas). Point the DSN at `-rw` for the primary, which is where `pg_stat_statements` lives that you want to watch.

!!! note "In pg-regression-radar"
    A `DeploySource` creates a route in the manager; its path is reported in `status.webhookPath`: `kubectl get deploysource <name> -n <ns> -o jsonpath='{.status.webhookPath}'`. A `DeploySource` with `sourceType: kubernetes` has no webhook path, because the radar watches the workload itself and nothing needs to call it. Setting up senders is covered in [Deploy Sources & Webhooks](../webhooks.md).

## Testing from your laptop: `port-forward`

A `ClusterIP` Service is not reachable from your machine. `kubectl port-forward` opens a temporary tunnel through the API server:

```bash
# in one terminal: forward local port 8080 to the manager Service
kubectl port-forward -n <ns> service/<fullname>-manager 8080:8080

# in another: post a test deploy event
curl -X POST http://localhost:8080/webhook/<ns>/<deploysource-name> \
  -H 'Content-Type: application/json' \
  -d '{"app":"orders-api","namespace":"<ns>","revision":"abc123","timestamp":"2026-11-11T12:00:00Z"}'
```

Port-forward lasts only while the command runs, binds to `localhost` by default, and is for debugging, not for production access. The same works for metrics:

```bash
kubectl port-forward -n <ns> service/<fullname>-manager 9090:9090
curl -s localhost:9090/metrics | head
```

That payload matches a `DeploySource` with `sourceType: generic`; other source types expect their own payload shape (see [Deploy Sources & Webhooks](../webhooks.md)). If the request is refused with `401`, the radar has a webhook secret configured: send it as `-H 'X-Webhook-Token: <value>'`. See [Config and Secrets](config-secrets.md#webhook-authentication).

## Reaching the radar from outside the cluster

Only expose the webhook endpoint when a sender outside the cluster needs it, and **always set a webhook secret first**. Two common routes:

- An **Ingress** (or Gateway) in front of the Service, with TLS. The Ingress controller is cluster-specific; the radar needs nothing special from it other than forwarding the path to port `8080`.
- A `LoadBalancer` Service (`--set service.type=LoadBalancer`), which is simpler but exposes the port directly.

Either way the endpoint must be authenticated, because an open endpoint lets anyone inject deploy events and skew detection. The chart prints a warning on install when `ingester.webhookSecret` is empty.

## NetworkPolicy

By default, any pod in a cluster can talk to any other pod. A **NetworkPolicy** restricts that: it selects pods by label and lists which ingress (incoming) and egress (outgoing) connections are allowed. Once any policy selects a pod for a direction, everything not listed for that direction is denied.

Two things you must know first:

- **Policies only work if your cluster's network plugin enforces them.** On a cluster whose CNI does not, the objects are accepted and silently ignored.
- **The model is "default deny plus explicit allows".** An empty policy that selects the radar's pods and lists no rules blocks all traffic to and from them. That is the right starting point, and it is also why a freshly applied policy "breaks" things until the allows are in.

The chart can render both parts for you (off by default):

```bash
helm upgrade --install radar deploy/helm/deploylens \
  --set mode=manager \
  --set networkPolicy.enabled=true \
  --set networkPolicy.egress.postgresCIDRs[0]=10.10.0.0/16 \
  --set networkPolicy.egress.apiServerCIDRs[0]=10.20.0.0/16
```

What the radar needs to be allowed, by direction:

| Direction | To / from | Port | Needed for |
|---|---|---|---|
| ingress | your deploy senders, Prometheus | `8080`, `9090` | webhooks and metrics |
| egress | Postgres | `5432` | scraping `pg_stat_statements` |
| egress | the Kubernetes API server (manager mode only) | `443` / `6443` | reconciling objects, leader election |
| egress | cluster DNS | `53` UDP/TCP | resolving every name above |
| egress | your alert destination | `443` | Slack, PagerDuty, relay |

The most frequent mistake is forgetting **DNS** and the **API server** egress. A manager that cannot resolve names or reach the API server fails in ways that look unrelated to the network. The full options, and how to adjust the DNS namespace, are in [Hardening (Helm)](../deployment-security.md#networkpolicy).

```bash
kubectl get networkpolicy -n <ns>
kubectl describe networkpolicy <fullname>-manager-allow -n <ns>
```

## See also

- [Deploy Sources & Webhooks](../webhooks.md): what to send, and from which tool.
- [Hardening (Helm)](../deployment-security.md#networkpolicy): every NetworkPolicy value.
- [Troubleshooting toolkit](troubleshooting.md): when a connection times out.
- [Alerting](../alerting.md): destination policies that complement egress rules.
