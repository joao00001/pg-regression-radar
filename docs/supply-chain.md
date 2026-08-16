# Supply Chain Verification

*How to confirm that a pg-regression-radar artifact you downloaded matches what was built from the source commit you expect.*

Every artifact published by a release — container images, Helm chart, and attached SBOMs — is cryptographically signed and traceable back to the exact source commit and GitHub Actions run that produced it. This page explains the trust model and gives the exact commands to verify each artifact type.

## Trust model

Signing uses [Cosign](https://github.com/sigstore/cosign) in **keyless mode**: instead of a stored private key (which can be stolen, rotated, or mis-scoped), the GitHub Actions runner exchanges its own OIDC token for a short-lived signing certificate from [Sigstore's public Fulcio CA](https://github.com/sigstore/fulcio). The certificate's Subject Alternative Name encodes the exact workflow URL and Git ref at build time. Every signature is recorded in [Rekor](https://github.com/sigstore/rekor), Sigstore's public, append-only transparency log, so it can be audited independently.

What this guarantees when you run `cosign verify`:

- The artifact was signed during a run of `.github/workflows/release.yml` in this repository (not just "any GitHub Actions workflow, anywhere").
- The signing certificate was issued for the exact ref and SHA you expect (visible in the verified output).
- The signature is present in the public Rekor log — it cannot be back-dated or silently removed.

What this does **not** guarantee:

- That the tag commit itself is correct — verify the Git tag signature with `git tag -v <tag>` if your threat model includes a compromised GitHub account pushing a bad tag.
- That new CVEs have not been disclosed against the published image bytes after release date — scan locally with Trivy if you need a current view.

## Artifact inventory

| Artifact | Registry path | Signed? | SBOM? |
|---|---|---|---|
| `cli` image (amd64 + arm64) | `ghcr.io/joao00001/pg-regression-radar/cli:<tag>` | ✓ keyless | ✓ SPDX attached |
| `operator` image (amd64 + arm64) | `ghcr.io/joao00001/pg-regression-radar/operator:<tag>` | ✓ keyless | ✓ SPDX attached |
| `manager` image (amd64 + arm64) | `ghcr.io/joao00001/pg-regression-radar/manager:<tag>` | ✓ keyless | ✓ SPDX attached |
| `collector` image (amd64 + arm64) | `ghcr.io/joao00001/pg-regression-radar/collector:<tag>` | ✓ keyless | ✓ SPDX attached |
| `ingester` image (amd64 + arm64) | `ghcr.io/joao00001/pg-regression-radar/ingester:<tag>` | ✓ keyless | ✓ SPDX attached |
| Helm chart (OCI) | `ghcr.io/joao00001/charts/pg-regression-radar:<chart-version>` | ✓ keyless | — |

Per-architecture images (`:v1.2.3-amd64`, `:v1.2.3-arm64`) are signed individually during the build job. The multi-arch manifest lists (`:v1.2.3`, `:latest`) are additionally signed after assembly — so `cosign verify` works whether you pin the multi-arch tag or a specific arch digest.

Chart versions use SemVer without a leading `v` (e.g. `1.2.3`), matching what `helm install --version` accepts. The image tag (`:v1.2.3`) and chart version (`1.2.3`) are always derived from the same Git tag.

## Finding the digest

Every GitHub Release page lists the pushed tag name and links to the Actions run. To resolve the exact digest of any image:

```bash
# Print the digest for the multi-arch manifest list
docker buildx imagetools inspect \
  ghcr.io/joao00001/pg-regression-radar/operator:v1.2.3 \
  --format '{{json .Manifest}}' | jq -r '.digest'

# Or with crane (faster, no daemon needed)
crane digest ghcr.io/joao00001/pg-regression-radar/operator:v1.2.3
```

For the Helm chart:

```bash
helm show chart oci://ghcr.io/joao00001/charts/pg-regression-radar --version 1.2.3 \
  | grep -E '^(version|appVersion):'
```

## Verifying image signatures

Replace `v1.2.3` with the release tag you are verifying. The `--certificate-identity-regexp` must match the release workflow path exactly; the `--certificate-oidc-issuer` must be GitHub's Actions token endpoint.

```bash
IMAGE=ghcr.io/joao00001/pg-regression-radar/operator:v1.2.3

cosign verify "$IMAGE" \
  --certificate-identity-regexp \
    "^https://github.com/joao00001/pg-regression-radar/.github/workflows/release.yml@refs/tags/v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

A successful verification prints the JSON payload of each matching signature, which includes the workflow ref, run URL, and SHA. A failure (wrong issuer, wrong identity, or no recorded signature) exits non-zero and prints an error — there is no silent pass on mismatch.

Verify a per-arch image by digest instead of tag:

```bash
DIGEST=$(docker buildx imagetools inspect \
  ghcr.io/joao00001/pg-regression-radar/operator:v1.2.3-amd64 \
  --format '{{json .Manifest}}' | jq -r '.digest')

cosign verify "ghcr.io/joao00001/pg-regression-radar/operator@${DIGEST}" \
  --certificate-identity-regexp \
    "^https://github.com/joao00001/pg-regression-radar/.github/workflows/release.yml@refs/tags/v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Verifying the Helm chart signature

The Helm chart is published as an OCI artifact to `ghcr.io/joao00001/charts/pg-regression-radar`. Cosign signs any OCI artifact by digest, so `cosign verify` works identically to images:

```bash
cosign verify ghcr.io/joao00001/charts/pg-regression-radar:1.2.3 \
  --certificate-identity-regexp \
    "^https://github.com/joao00001/pg-regression-radar/.github/workflows/release.yml@refs/tags/v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Inspecting the SBOM

Each image digest has an SPDX-JSON SBOM attached as a signed in-toto attestation. Fetch and inspect it:

```bash
IMAGE=ghcr.io/joao00001/pg-regression-radar/operator:v1.2.3

cosign verify-attestation "$IMAGE" \
  --type spdxjson \
  --certificate-identity-regexp \
    "^https://github.com/joao00001/pg-regression-radar/.github/workflows/release.yml@refs/tags/v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq -r '.payload | @base64d | fromjson | .predicate'
```

The predicate output is an SPDX document listing every OS package and Go module baked into the image at build time. To extract just the package list:

```bash
cosign verify-attestation "$IMAGE" \
  --type spdxjson \
  --certificate-identity-regexp \
    "^https://github.com/joao00001/pg-regression-radar/.github/workflows/release.yml@refs/tags/v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq -r '.payload | @base64d | fromjson | .predicate.packages[].name'
```

SBOM files are also uploaded as GitHub Actions artifacts during the release run and attached to the GitHub Release for offline inspection (named `sbom-<target>-<arch>-<version>.spdx.json`).

## Scanning locally for vulnerabilities

To check a released image for CVEs beyond those known at release date:

```bash
trivy image ghcr.io/joao00001/pg-regression-radar/operator:v1.2.3
```

The release pipeline already scans every image at build time and fails on any CRITICAL-severity finding, so the image was clean at release date. `trivy image` here gives you a current view (new CVE disclosures may have occurred since).

## Provenance: connecting source commit to built artifact

Build arguments `VERSION`, `COMMIT`, and `DATE` are injected at build time (see the `Build` step in `.github/workflows/release.yml`):

- `VERSION` — the pushed Git tag (e.g. `v1.2.3`)
- `COMMIT` — the full Git SHA of the tagged commit (`github.sha`)
- `DATE` — RFC 3339 UTC timestamp of the build start

These values are baked into each binary's `--version` output via `internal/buildinfo`. To confirm the binary in a pulled image was built from the commit you expect:

```bash
docker run --rm ghcr.io/joao00001/pg-regression-radar/cli:v1.2.3 pg-regression-radar version
# Expected output includes: version=v1.2.3  commit=<sha>  date=<timestamp>
```

Cross-reference `commit` with the GitHub Release's linked tag commit SHA to confirm they match.

## Automated verification script

`scripts/verify-release.sh` automates the checks above. It requires `cosign` and `jq` to be installed:

```bash
./scripts/verify-release.sh v1.2.3
```

The script verifies signatures for all five images (multi-arch manifest lists) and the Helm chart, then prints a summary. Pass `--help` for usage.

## Troubleshooting

**`cosign verify` exits with "no matching signatures"**

The image was either not pushed by `release.yml` (e.g. a local build), or the `--certificate-identity-regexp` does not match. Verify you are using the exact URL pattern shown above, including the trailing `/refs/tags/v` anchor. Signatures are not stored in the image itself — they live in the registry's referrers API and in the public Rekor log. If the image was re-tagged or mirrored to a different registry, signatures do not follow.

**`cosign verify-attestation` exits with "no matching attestations"**

The SBOM attestation is attached to the per-arch digest, not the manifest-list tag. If you are verifying a manifest-list reference (`:v1.2.3`) and your registry/cosign version does not traverse the index, pin to a per-arch digest instead (`:v1.2.3-amd64` or `:v1.2.3-arm64`).

**`helm push` digest not parseable (historic release gap)**

Before `v1.0.1`, the Helm chart signing step was missing a separate Docker registry login for cosign, causing the sign step to fail with `UNAUTHORIZED`. The chart was pushed but unsigned. Starting with `v1.0.1`, `cosign verify` against the chart passes. Images were signed from the first release.

## See also

- [Installation](installation.md) — pull, install, and verify commands in one place.
- [CI/CD](ci-cd.md) — how `release.yml` builds, scans, signs, and publishes each artifact.
- [Security Model](security-model.md) — the broader threat model this sits inside.
