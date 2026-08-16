# Deployment security hardening (Helm)

This guide covers optional Kubernetes hardening layers exposed by the Helm chart:

- NetworkPolicy (default-deny + explicit allow rules)
- ResourceQuota
- LimitRange
- Pod Security Standards (PSS) `restricted`
- Optional legacy PodSecurityPolicy rendering for older clusters

## NetworkPolicy

NetworkPolicy is disabled by default and can be enabled per release:

```bash
helm upgrade --install radar deploy/helm/deploylens \
  --set mode=manager \
  --set networkPolicy.enabled=true \
  --set networkPolicy.ingressCIDR=10.0.0.0/24 \
  --set networkPolicy.egress.postgresCIDRs[0]=10.10.0.0/16 \
  --set networkPolicy.egress.apiServerCIDRs[0]=10.20.0.0/16 \
  --set networkPolicy.egress.alertRelayCIDRs[0]=10.30.0.0/16
```

When enabled, the chart renders:

- a mode-specific **default-deny** policy (`Ingress` + `Egress`)
- a mode-specific **allow** policy with:
  - ingress for webhook and metrics ports
  - egress for PostgreSQL, API servers (manager mode), DNS, and alert relay CIDRs

### Notes

- `networkPolicy.ingressCIDR` is optional. If empty, ingress rules allow from any source.
- DNS egress is enabled by default (`kube-system` namespace, port `53` TCP/UDP).
- Keep CIDRs narrow and explicit for production.

## ResourceQuota and LimitRange

Enable namespace guardrails directly from chart values:

```bash
helm upgrade --install radar deploy/helm/deploylens \
  --set resourceQuota.enabled=true \
  --set limitRange.enabled=true
```

Defaults can be overridden in values:

- `resourceQuota.hard` for aggregate namespace limits
- `limitRange.limits` for per-container default requests/limits

See `docs/examples/values-hardened.yaml` for a complete example.

## Pod Security Standards (PSS) restricted

The chart defaults are compatible with PSS `restricted` expectations:

- `runAsNonRoot: true`
- `runAsUser`/`runAsGroup`: `65532`
- `seccompProfile.type: RuntimeDefault`
- `allowPrivilegeEscalation: false`
- `capabilities.drop: [ALL]`
- `readOnlyRootFilesystem: true`
- `serviceAccount.automountServiceAccountToken: true` (configurable)

Apply namespace Pod Security Admission labels as needed:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: pg-regression-radar
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
```

## Optional legacy PodSecurityPolicy

`PodSecurityPolicy` is deprecated/removed in modern Kubernetes. For legacy clusters only:

```bash
helm upgrade --install radar deploy/helm/deploylens \
  --set podSecurityPolicy.enabled=true
```

The template renders only when API `policy/v1beta1/PodSecurityPolicy` is available.

## ServiceAccount token automount

The chart now exposes `serviceAccount.automountServiceAccountToken` on both the ServiceAccount and Pod specs.

- **`mode: manager`**: keep this `true` (required for in-cluster Kubernetes API access, reconciliation, and leader election).
- **`mode: operator`**: can be set to `false` to reduce credential exposure when running without in-cluster API usage.

## Hardened values example

Use:

```bash
helm upgrade --install radar deploy/helm/deploylens \
  -f docs/examples/values-hardened.yaml
```

## Validation

```bash
helm lint deploy/helm/deploylens
helm template radar deploy/helm/deploylens -f docs/examples/values-hardened.yaml >/dev/null
```

## Troubleshooting

- **No NetworkPolicy created**: verify `networkPolicy.enabled=true`.
- **Pods cannot reach PostgreSQL/API/relay**: confirm destination CIDRs and ports in `networkPolicy.egress.*`.
- **DNS resolution failures**: confirm cluster DNS runs in `kube-system` or adjust `networkPolicy.egress.dns.namespace`.
- **ResourceQuota/LimitRange missing**: verify `resourceQuota.enabled` / `limitRange.enabled`.
- **PSS restricted rejection**: check overridden security context values in custom `values.yaml`.
