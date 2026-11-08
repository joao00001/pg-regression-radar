#!/bin/bash
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
#
# Real PostgreSQL 17 (Debian's own postgresql-17 package, default "main"
# cluster) with pg_store_plans actually compiled and installed via PGXS —
# see the Dockerfile and docs/testing.md. Not a fake/mocked extension: this
# is the real https://github.com/ossc-db/pg_store_plans code, loaded via
# shared_preload_libraries, exposing the real pg_store_plans view that
# internal/planner.CapturePlanFromStorePlans reads from.
set -euo pipefail

PGBIN=/usr/lib/postgresql/17/bin
PGDATA=/var/lib/postgresql/17/main
PGCONF=/etc/postgresql/17/main/postgresql.conf
PGHBA=/etc/postgresql/17/main/pg_hba.conf

# Only do the one-time setup (rewrite config, create extensions) the first
# time a container starts against a fresh (bind-mounted or ephemeral) data
# directory — reruns against an already-configured $PGDATA would otherwise
# keep appending duplicate settings.
if ! grep -q "^# pgrr-configured" "$PGCONF"; then
  {
    echo "# pgrr-configured"
    echo "listen_addresses = '*'"
    echo "shared_preload_libraries = 'pg_stat_statements,pg_store_plans'"
    echo "compute_query_id = on"
    # Debian's default postgresql.conf turns ssl on and points at a
    # snakeoil cert this image never generates; this is a throwaway
    # integration-test fixture reachable only from the test runner itself
    # (see docs/testing.md), so plain TCP is an acceptable, deliberate
    # trade-off here — never do this for anything that isn't.
    echo "ssl = off"
    echo "pg_store_plans.plan_format = 'json'"
  } >> "$PGCONF"
  # Host-based auth for TCP connections published outside the container
  # (e.g. a test runner on the Docker host) — Debian's default pg_hba.conf
  # only allows 127.0.0.1/::1 directly, which a published container port
  # does not appear as (the source address is the Docker bridge gateway).
  echo "host all all 0.0.0.0/0 md5" >> "$PGHBA"
  echo "host all all ::/0 md5" >> "$PGHBA"

  su postgres -s /bin/bash /pg-setup.sh
fi

exec su postgres -s /bin/bash -c "exec \"$PGBIN/postgres\" -D \"$PGDATA\" -c config_file=\"$PGCONF\""
