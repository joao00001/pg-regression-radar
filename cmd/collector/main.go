// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// collector is the pg-regression-radar metric scraper binary.
// It scrapes pg_stat_statements from a Postgres cluster and exposes the data as
// Prometheus metrics.
//
// This is a thin wrapper around internal/cli.RunCollector, kept as its own
// binary so existing Dockerfile targets and Helm chart image references
// don't have to change. If you just want to install pg-regression-radar
// locally, see cmd/pg-regression-radar for the single unified CLI instead
// (`go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest`
// then `pg-regression-radar collector ...`).
//
// Usage:
//
//	collector \
//	  --dsn "******host:5432/dbname?sslmode=disable" \
//	  --scrape-interval 60s \
//	  --cluster-name my-cluster \
//	  --namespace production \
//	  --listen :9090 \
//	  --retention-minutes 180
package main

import (
	"os"

	"github.com/joao00001/pg-regression-radar/internal/cli"
)

func main() {
	os.Exit(cli.RunCollector(os.Args[1:]))
}
