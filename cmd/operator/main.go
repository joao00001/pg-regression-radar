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

// operator is the pg-regression-radar all-in-one binary.
// It wires together the Collector, Deploy Event Ingester, Correlation Engine,
// and Alerting components into a single process suitable for running in
// Kubernetes as a Deployment.
//
// This is a thin wrapper around internal/cli.RunOperator, kept as its own
// binary so existing Dockerfile targets and Helm chart image references
// don't have to change. If you just want to install pg-regression-radar
// locally, see cmd/pg-regression-radar for the single unified CLI instead
// (`go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest`
// then `pg-regression-radar operator ...`).
//
// Usage:
//
//	operator \
//	  --dsn "host:5432/dbname?sslmode=disable" \
//	  --cluster-name my-cluster \
//	  --namespace production \
//	  --webhook-listen :8080 \
//	  --metrics-listen :9090 \
//	  --slack-url https://hooks.slack.com/... \
//	  --window-minutes 30 \
//	  --min-executions 10 \
//	  --latency-threshold 0.20 \
//	  --changepoint-tolerance 6m \
//	  --retention-minutes 180 \
//	  --state-backend postgres \
//	  --state-dsn "host:5432/dbname?sslmode=disable"
package main

import (
	"os"

	"github.com/joao00001/pg-regression-radar/internal/cli"
)

func main() {
	os.Exit(cli.RunOperator(os.Args[1:]))
}
