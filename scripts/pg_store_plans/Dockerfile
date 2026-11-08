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
# Builds a REAL PostgreSQL server with the REAL pg_store_plans extension
# (https://github.com/ossc-db/pg_store_plans) compiled from source and
# installed — the one third-party piece internal/planner's
# CapturePlanFromStorePlans path had only ever been exercised against a fake
# *sql.DB for (see docs/testing.md#real-pg_store_plans-extension). Used by
# internal/planner's storeplans_integration_test.go and the dedicated
# integration-pg-store-plans job in .github/workflows/ci.yml.
#
# Deliberately NOT based on the official `postgres` Docker Hub image: that
# image's apt source is apt.postgresql.org (PGDG), which only ships
# pre-built server binaries — no postgresql-server-dev-<major> headers
# needed to build a PGXS extension at all, and that registry is not
# reachable from every environment (including the one this file was first
# authored in). Debian trixie's OWN archives (deb.debian.org, always
# reachable) ship both `postgresql-17` (the server) and
# `postgresql-server-dev-17` (its matching headers) built from the exact
# same source at the exact same version — e.g. both as 17.11-0+deb13u1 as of
# this writing — which is exactly the version-exact match PGXS needs to
# produce a `.so` that actually loads into that same server. PostgreSQL 17
# also comfortably clears pg_store_plans' own PostgreSQL 14+ requirement for
# trusting its queryid against pg_stat_statements (see
# internal/planner/storeplans.go's storePlansMinReliablePGMajor).
#
# Installing the `postgresql-17` package also auto-creates Debian's default
# "17/main" cluster via its own postinst hook (a real `initdb`, the same one
# a human running `apt install postgresql` on a real Debian server would
# get) — entrypoint.sh reconfigures and (re)starts that same cluster rather
# than creating a second one from scratch.
FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive

# Pinned to pg_store_plans' latest tagged release
# (https://github.com/ossc-db/pg_store_plans/releases) rather than `master`,
# so this image is reproducible and so a future upstream commit can't
# silently change what's being validated here out from under this repo.
ARG PG_STORE_PLANS_REF=1.10

RUN apt-get update && apt-get install -y --no-install-recommends \
      postgresql-17 postgresql-server-dev-17 \
      build-essential git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --branch "${PG_STORE_PLANS_REF}" --depth 1 \
      https://github.com/ossc-db/pg_store_plans.git /tmp/pg_store_plans \
    && cd /tmp/pg_store_plans \
    && export PATH="/usr/lib/postgresql/17/bin:${PATH}" \
    && make USE_PGXS=1 \
    && make USE_PGXS=1 install \
    && rm -rf /tmp/pg_store_plans

COPY pg-setup.sh /pg-setup.sh
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /pg-setup.sh /entrypoint.sh

EXPOSE 5432
ENTRYPOINT ["/entrypoint.sh"]
