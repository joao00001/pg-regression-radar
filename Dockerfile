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
# this is one `build` stage that compiles all four, followed by one minimal
# runtime stage per binary. Pick which one you want with --target:
#
#   docker build --target operator -t pg-regression-radar/operator .
#   docker build --target manager  -t pg-regression-radar/manager  .
#   docker build --target collector -t pg-regression-radar/collector .
#   docker build --target ingester  -t pg-regression-radar/ingester .
#
# With no --target, the last stage in the file wins (operator), matching the
# Helm chart's default `mode: operator`.

FROM golang:1.24-bookworm AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 for fully static binaries that run unmodified on the
# distroless runtime image below (no libc dependency to satisfy).
# The go.mod `go 1.26.0` directive triggers Go's toolchain auto-download
# (see https://go.dev/doc/toolchain) to fetch the exact matching toolchain
# even though this build stage's base image ships an older `go` — the same
# behaviour this project's own CI and local development already rely on.
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/operator  ./cmd/operator  && \
    go build -trimpath -ldflags="-s -w" -o /out/manager   ./cmd/manager   && \
    go build -trimpath -ldflags="-s -w" -o /out/collector ./cmd/collector && \
    go build -trimpath -ldflags="-s -w" -o /out/ingester  ./cmd/ingester

# ---- operator ----
FROM gcr.io/distroless/static-debian12:nonroot AS operator
COPY --from=build /out/operator /usr/local/bin/operator
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/operator"]

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
