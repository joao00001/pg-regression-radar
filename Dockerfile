# Copyright 2026 The pg-regression-radar Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# syntax=docker/dockerfile:1
#
# Multi-stage, multi-binary build. This repo ships four independently
# runnable commands (cmd/operator, cmd/manager, cmd/collector, cmd/ingester —
# see each package's doc comment and README's "Two ways to run" section), and
# deploy/helm/deploylens already references separate image paths for the
# operator and manager modes. Rather than four near-identical Dockerfiles,
# this is one `build` stage that compiles all four (plus the unified
# cmd/pg-regression-radar CLI — see its doc comment), followed by one
# minimal runtime stage per binary. Pick which one you want with --target:
#
#   docker build --target operator -t pg-regression-radar/operator .
#   docker build --target manager  -t pg-regression-radar/manager  .
#   docker build --target collector -t pg-regression-radar/collector .
#   docker build --target ingester  -t pg-regression-radar/ingester .
#   docker build --target cli       -t pg-regression-radar/cli      .
#
# With no --target, the last stage in the file wins (operator), matching the
# Helm chart's default `mode: operator`.

FROM golang:1.24-bookworm AS build
WORKDIR /src

# Build metadata for internal/buildinfo, surfaced by every binary's
# --version flag. All three default to buildinfo's own honest "unbuilt"
# defaults when left unset (e.g. a bare `docker build .` with no
# --build-arg), so omitting them is safe — see internal/buildinfo's doc
# comment for the rationale. Example release build:
#
#   docker build \
#     --build-arg VERSION=v0.1.0 \
#     --build-arg COMMIT=$(git rev-parse HEAD) \
#     --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     --target operator -t pg-regression-radar/operator .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

# go.mod declares `go 1.26.0`, newer than this base image's bootstrap `go`
# (1.24.x). Go's toolchain-switching feature (see https://go.dev/doc/toolchain)
# would normally fetch the exact 1.26.0 toolchain automatically on demand —
# but the official golang Docker images set GOTOOLCHAIN=local specifically to
# suppress that implicit network fetch, so it must be re-enabled explicitly.
# This is deliberately not "just bump the base image to golang:1.26-bookworm"
# instead: pinning the bootstrap image to whatever tag matches today's go.mod
# would silently break every time go.mod's `go` directive is bumped, whereas
# GOTOOLCHAIN=auto keeps working unattended for any future Go version as long
# as this bootstrap `go` is >= 1.21 (when toolchain switching was introduced).
ENV GOTOOLCHAIN=auto

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 for fully static binaries that run unmodified on the
# distroless runtime image below (no libc dependency to satisfy).
ENV CGO_ENABLED=0 GOOS=linux
RUN LDFLAGS="-s -w \
      -X github.com/joao00001/pg-regression-radar/internal/buildinfo.Version=${VERSION} \
      -X github.com/joao00001/pg-regression-radar/internal/buildinfo.Commit=${COMMIT} \
      -X github.com/joao00001/pg-regression-radar/internal/buildinfo.Date=${DATE}" && \
    go build -trimpath -ldflags="${LDFLAGS}" -o /out/operator  ./cmd/operator  && \
    go build -trimpath -ldflags="${LDFLAGS}" -o /out/manager   ./cmd/manager   && \
    go build -trimpath -ldflags="${LDFLAGS}" -o /out/collector ./cmd/collector && \
    go build -trimpath -ldflags="${LDFLAGS}" -o /out/ingester  ./cmd/ingester && \
    go build -trimpath -ldflags="${LDFLAGS}" -o /out/pg-regression-radar ./cmd/pg-regression-radar

# ---- cli (unified pg-regression-radar binary, all four modes as subcommands) ----
FROM gcr.io/distroless/static-debian12:nonroot AS cli
COPY --from=build /out/pg-regression-radar /usr/local/bin/pg-regression-radar
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/pg-regression-radar"]

# ---- manager (controller-runtime, for the CRD-based deployment mode) ----
FROM gcr.io/distroless/static-debian12:nonroot AS manager
COPY --from=build /out/manager /usr/local/bin/manager
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/manager"]

# ---- collector (standalone) ----
FROM gcr.io/distroless/static-debian12:nonroot AS collector
COPY --from=build /out/collector /usr/local/bin/collector
USER nonroot:nonroot
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/collector"]

# ---- ingester (standalone) ----
FROM gcr.io/distroless/static-debian12:nonroot AS ingester
COPY --from=build /out/ingester /usr/local/bin/ingester
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ingester"]

# ---- operator ----
FROM gcr.io/distroless/static-debian12:nonroot AS operator
COPY --from=build /out/operator /usr/local/bin/operator
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/operator"]
