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
# Runs as the postgres OS user (invoked by entrypoint.sh via `su postgres`).
# Starts Debian's default "17/main" cluster once with its now-rewritten
# postgresql.conf (shared_preload_libraries set, compute_query_id on),
# creates both extensions, then stops it again so entrypoint.sh can exec the
# real server as PID 1. Isolated into its own script (rather than inlined in
# entrypoint.sh) so its multi-word psql/pg_ctl arguments don't have to
# survive a second layer of `su -c "..."` shell-quoting.
set -euo pipefail

PGBIN=/usr/lib/postgresql/17/bin
PGDATA=/var/lib/postgresql/17/main
PGCONF=/etc/postgresql/17/main/postgresql.conf

"$PGBIN/pg_ctl" -D "$PGDATA" -o "-c config_file=$PGCONF" -w start
"$PGBIN/psql" -v ON_ERROR_STOP=1 -c "ALTER USER postgres PASSWORD '${POSTGRES_PASSWORD:-test}';"
"$PGBIN/psql" -v ON_ERROR_STOP=1 -c "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;"
"$PGBIN/psql" -v ON_ERROR_STOP=1 -c "CREATE EXTENSION IF NOT EXISTS pg_store_plans;"
"$PGBIN/pg_ctl" -D "$PGDATA" -m fast -w stop
