# Collect a report

*Everything a bug report needs, gathered read-only, so the first reply can be an answer and not a list of questions.*

## Overview

A report that says "it does not alert" cannot be diagnosed. One that has the version, the status of the watch, the conditions, and the last hour of the manager log usually can. The script below only reads: it makes no change to the cluster and it does not read Secrets.

## The script

Save it as `pgrr-report.sh`, set `NS` and `MANAGER`, and run it. It writes a directory you can attach.

```bash
#!/usr/bin/env bash
# Read-only. Does not read Secrets.
set -u
NS="${NS:?set NS to the namespace of the manager}"
MANAGER="${MANAGER:-deploy/pg-regression-radar-manager}"
OUT="pgrr-report-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUT"

kubectl version -o json                                > "$OUT/kubectl-version.json" 2>&1
kubectl -n "$NS" get "$MANAGER" -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}{.spec.template.spec.containers[0].args}{"\n"}' \
                                                        > "$OUT/manager-image-and-args.txt" 2>&1
kubectl get crd -o name | grep radar.pgregressionradar.io > "$OUT/crds.txt" 2>&1

kubectl get pgw,pgds,pgreg -A -o wide                  > "$OUT/objects.txt" 2>&1
kubectl get pgrc -o wide                               > "$OUT/clusters.txt" 2>&1
for w in $(kubectl -n "$NS" get pgw -o name); do
  n=$(basename "$w")
  kubectl -n "$NS" get "$w" -o json | jq '{name: .metadata.name, annotations: .metadata.annotations, phase: .status.phase,
      trackedQueryIds: .status.trackedQueryIds, lastScrapeTime: .status.lastScrapeTime, conditions: .status.conditions,
      attribution: .spec.attribution}' > "$OUT/watch-$n.json" 2>&1
  kubectl -n "$NS" describe "$w"                       > "$OUT/watch-$n-describe.txt" 2>&1
done
kubectl -n "$NS" describe pgds                         > "$OUT/deploysources-describe.txt" 2>&1

kubectl -n "$NS" get pods -l app.kubernetes.io/component=manager -o wide > "$OUT/manager-pods.txt" 2>&1
kubectl -n "$NS" logs -l app.kubernetes.io/component=manager --since=1h --tail=-1 > "$OUT/manager.log" 2>&1
kubectl -n "$NS" logs -l app.kubernetes.io/component=manager --previous --tail=200 > "$OUT/manager-previous.log" 2>&1
kubectl -n "$NS" get events --sort-by=.lastTimestamp   > "$OUT/events.txt" 2>&1

echo "wrote $OUT/. Read manager.log before you share it."
```

`spec.attribution` is included because the mode, the actuation settings and the arms configuration decide most of what the engine does. The DSN is in a Secret and is not collected; if you add the full spec of a watch, remove `dsnSecretRef` details you do not want to share.

## What else to add

- **The question.** What you expected, what you got, and the deploy id or the time it happened.
- **The reason codes in play.** The lines that matter most are those with a `reason`:

    ```bash
    jq -c 'select(.reason) | {time, level, reason, msg}' pgrr-report-*/manager.log | tail -40
    ```

- **The ledger, if it is about acting.** Through a port-forward to the leader, `GET /api/v1/attribution/actions` and the verdict for the app: see [Where to look](where-to-look.md#5-the-managers-http-endpoints).
- **A debug log of one deploy.** Run the manager with `--log-level=debug` (chart: `manager.logLevel: debug`) across one deploy and attach that stretch. It shows every query the correlation engine looked at.

## Before you share it

The log holds namespaces, application names, revisions, deploy ids and, in error messages, host names and addresses. It is not meant to hold the DSN or query text, but an error message can quote what the database or the destination returned. Read it once and redact what you cannot share. Do not paste Secrets, kubeconfigs or webhook tokens.

## See also

- [Where to look](where-to-look.md)
- [Reason codes](reasons.md) and [Log catalog](log-catalog.md)
