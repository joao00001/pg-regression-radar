# Upgrading

*How to move a Helm install to a newer release safely: CRDs first, then the chart, with a way back.*

## Overview

Upgrading the radar is `helm upgrade`, with one extra step Helm does not do for you: updating the CRDs. Helm applies a chart's `crds/` directory only on the first install, so a newer chart can arrive in a cluster that still has the old definitions. This page gives the order to follow and what to check before moving between releases. It applies to `manager` mode; in `operator` mode no CRDs are used and only the chart (or image) changes.

## Before you start

The CRDs are `v1alpha1`, which means a schema can change in any release, including a patch, and there is no conversion webhook. Read the [Changelog](changelog.md) for every release between yours and the target, and look for a **Breaking changes** heading. See [API versioning](api-versioning.md) for what is and is not guaranteed.

```bash
helm list -n <ns>                                  # the chart version you run now
kubectl get postgreswatch,deploysource -A -o yaml > backup.yaml
```

The backup is your way back if a field was renamed or removed. With `state.backend: postgres` the sample history survives restarts; with the default `memory` backend a restart starts cold ([Persistence](persistence.md)).

## Upgrade steps

1. **Get the new CRDs.** They ship with the chart. Pull them from the matching tag or from the chart itself:

    ```bash
    helm pull oci://ghcr.io/joao00001/charts/pg-regression-radar --version <new-version> --untar
    kubectl apply -f pg-regression-radar/crds/
    ```

    In a clone, the same files are in `deploy/helm/deploylens/crds/`. Use `kubectl apply` (or `--server-side` if you hit annotation size limits), never delete and recreate: deleting a CRD deletes every object of that kind.

2. **Wait until they are accepted:**

    ```bash
    kubectl wait --for=condition=Established \
      crd/postgreswatches.radar.pgregressionradar.io \
      crd/deploysources.radar.pgregressionradar.io \
      crd/performanceregressions.radar.pgregressionradar.io \
      crd/postgresradarclusters.radar.pgregressionradar.io
    ```

3. **Compare your values with the new defaults:**

    ```bash
    helm show values oci://ghcr.io/joao00001/charts/pg-regression-radar --version <new-version> > new-values.yaml
    helm get values <release> -n <ns> > current-values.yaml
    diff current-values.yaml new-values.yaml
    ```

4. **Upgrade the chart.** Pass your values file each time; it is more predictable than `--reuse-values`, which ignores new chart defaults:

    ```bash
    helm upgrade <release> oci://ghcr.io/joao00001/charts/pg-regression-radar \
      --version <new-version> -n <ns> -f my-values.yaml
    ```

    The chart version is the release tag without the leading `v`. `image.tag` defaults to the chart's `appVersion`, so the image moves with it unless you pinned a tag.

5. **Check it came up:**

    ```bash
    kubectl rollout status deployment -n <ns>
    kubectl get pgw,pgds -n <ns>
    ```

    Every `PostgresWatch` should reach `Running` and every `DeploySource` `Ready`. If one is `Failed`, `status.message` says why.

## Minor and patch releases

Patch releases usually change only the image. Minor releases can add fields or change defaults. Always run steps 1 and 2 anyway: applying an unchanged CRD is harmless, and skipping it is the common cause of "unknown field" errors after an upgrade.

## Major releases and `v1alpha1` changes

A major release, or any release whose notes list a breaking change, can rename or remove fields. Before upgrading:

- Edit your manifests (and GitOps repo) to the new schema.
- If objects stored in the cluster no longer validate, update or re-create them from your backup after the CRDs are applied.
- Upgrade in a non-production cluster first.

## Rolling back

```bash
helm history <release> -n <ns>
helm rollback <release> <revision> -n <ns>
```

Rollback restores the release's objects, **not the CRDs**. If the new CRDs changed the schema, re-apply the CRDs from the older version too, then restore `backup.yaml` if needed.

## See also

- [Helm basics](kubernetes/helm.md#upgrade-and-roll-back): the commands in context.
- [CRDs and operators](kubernetes/crds-operators.md#installing-and-upgrading-the-definitions): why Helm skips CRDs.
- [API versioning](api-versioning.md): the `v1alpha1` policy and graduation path.
- [Installation](installation.md): pinning a chart version.
- [Troubleshooting and FAQ](troubleshooting.md)
