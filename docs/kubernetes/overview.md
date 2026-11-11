# Kubernetes basics

*The Kubernetes you need to install, operate and debug pg-regression-radar, and nothing more.*

## Overview

pg-regression-radar is built to run in Kubernetes, and its `manager` mode is driven by Kubernetes objects: you describe what to watch in YAML, and the radar does the rest. You do not need to be a Kubernetes administrator to use it, but you do need a working model of a handful of concepts: objects and namespaces, Deployments, Secrets, Services, custom resources, RBAC and Helm. This section teaches exactly those, always from the point of view of what the radar does with each one.

Every page follows the same shape: the concept in a few paragraphs, the `kubectl` commands you will actually type, and a short "In pg-regression-radar" part that ties it back to the tool.

!!! note "What this section does not cover"
    Building or sizing a cluster, choosing a CNI or a storage class, autoscaling, service meshes, GitOps tooling and Postgres operators (CloudNativePG and friends) are out of scope. If you can already run `kubectl get pods` against a cluster you are allowed to use, you have everything this section assumes.

## What you need on your machine

| Tool | Needed for | Check |
|---|---|---|
| `kubectl` | everything on these pages | `kubectl version --client` |
| `helm` (3.x) | only the Helm install path ([Helm basics](helm.md)) | `helm version` |
| A cluster and a kubeconfig | a place to run the radar: [kind](https://kind.sigs.k8s.io/), minikube, k3d, Docker Desktop, or a real cluster | `kubectl config current-context` |

`kubectl` talks to whichever cluster your **current context** points at. Before running anything that changes state, confirm you are where you think you are:

```bash
kubectl config current-context
kubectl config get-contexts
kubectl get nodes
```

!!! warning "Check the context before you apply"
    The most common Kubernetes mistake is applying a manifest to the wrong cluster. `kubectl apply` does not ask for confirmation. If you work with more than one cluster, make `kubectl config current-context` a habit before every install or delete.

### What permissions you need

Installing the radar in `manager` mode creates cluster-scoped objects (CustomResourceDefinitions, a ClusterRole and a ClusterRoleBinding), so the person installing it needs cluster-level rights. You can check before you start:

```bash
kubectl auth can-i create customresourcedefinitions
kubectl auth can-i create clusterroles
kubectl auth can-i create deployments -n <ns>
```

`operator` mode creates nothing cluster-scoped: a Deployment, a Service, a ServiceAccount and a Secret in one namespace are enough. See [RBAC and access](rbac.md) for what each mode asks for.

## How the concepts map to the radar

| Kubernetes concept | What pg-regression-radar does with it | Page |
|---|---|---|
| Object, `spec` and `status` | You write `spec`; the radar writes `status` (phase, message, results) | [Objects and namespaces](objects.md) |
| Namespace | Scopes your `PostgresWatch`, `DeploySource` and Secrets; the radar's own home | [Objects and namespaces](objects.md) |
| Labels and annotations | The consent label on your DSN Secret; the revision annotation on a Deployment | [Objects and namespaces](objects.md) |
| Deployment, rollout | The radar itself runs as a Deployment; a finished rollout of **your** app is a deploy event | [Pods and Deployments](workloads.md) |
| Probes, requests, limits | Health of the radar's pods; the namespace quota the chart can create | [Pods and Deployments](workloads.md) |
| Secret | Holds the Postgres DSN, the webhook token and alert credentials | [Config and Secrets](config-secrets.md) |
| Service, DNS, port-forward | How a webhook sender reaches the radar, and how the radar reaches Postgres | [Services and networking](networking.md) |
| NetworkPolicy | Optional default-deny rules the chart can render | [Services and networking](networking.md) |
| CustomResourceDefinition, controller | `PostgresWatch`, `DeploySource`, `PerformanceRegression`, `PostgresRadarCluster` | [CRDs and operators](crds-operators.md) |
| ServiceAccount, Role, ClusterRole | The permissions the manager has, and why they are what they are | [RBAC and access](rbac.md) |
| Helm chart, release, values | The supported way to install and upgrade | [Helm basics](helm.md) |
| Events, logs, conditions | How you find out why something is not working | [Troubleshooting toolkit](troubleshooting.md) |

## Suggested reading order

- **You want to try the [lab](../lab.md):** read [Objects and namespaces](objects.md) and the `kubectl` parts of [Pods and Deployments](workloads.md), then keep [Troubleshooting toolkit](troubleshooting.md) open in a tab.
- **You are installing it for real, in `manager` mode:** read all of them in order. The pages build on each other, and [RBAC and access](rbac.md) explains the one security decision you have to make (the consent label).
- **You run it in `operator` mode:** [Config and Secrets](config-secrets.md), [Services and networking](networking.md) and [Helm basics](helm.md) cover almost everything; the CRD pages are background.
- **Something is broken:** go straight to [Troubleshooting toolkit](troubleshooting.md).

## Conventions on these pages

- `<ns>` stands for the namespace the radar is installed in. In the [lab](../lab.md) it is `pgrr-lab`.
- `<fullname>` stands for the name Helm gives the chart's objects: the release name, plus the chart name unless the release name already contains it. Find the real value with `kubectl get deploy -n <ns>`.
- Commands that only read are shown plain. Commands that change something say so in the text around them.
- Every command works with a plain `kubectl`; none of them needs a plugin.

## A ten-minute look at a running install

If the radar is already running (the [lab](../lab.md) is the easiest way to get one), these four commands show you most of what the rest of the section explains:

```bash
# 1. The radar's pods. Look for READY 1/1 and STATUS Running.
kubectl get pods -n <ns>

# 2. The things you configure, in one call (short names, see "CRDs and operators").
kubectl get postgreswatch,deploysource -n <ns>

# 3. The things the radar produces.
kubectl get performanceregressions -n <ns>

# 4. What the radar says about itself.
kubectl logs -n <ns> deployment/<fullname>-manager --tail=50
```

If any of these shows something you do not recognise, the page for that concept is in the table above.

## See also

- [Try it in five minutes (lab)](../lab.md): a complete install and a deliberate regression to practice on.
- [Installation](../installation.md) and [Getting Started](../getting-started.md): the install paths these pages prepare you for.
- [Architecture Overview](../architecture.md): what `operator` and `manager` mode are.
