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

// manager is the highly-available, Kubernetes-native entrypoint for
// pg-regression-radar: it runs a controller-runtime Manager that reconciles
// PostgresWatch, DeploySource, and PerformanceRegression CRDs, starting one
// Collector+Correlation Engine worker per PostgresWatch and one webhook
// route per DeploySource — the same engine packages cmd/operator wires up
// directly, but now driven by Kubernetes objects instead of CLI flags, and
// with leader election so multiple replicas can run for HA (only the
// elected leader does any work; standbys take over via a
// coordination.k8s.io Lease on failover).
//
// cmd/operator remains the simple, CRD-free, single-process/single-DSN
// mode documented in the README; cmd/manager is the additional,
// recommended-for-production mode. See README.md, section "Two ways to
// run pg-regression-radar".
//
// This is a thin wrapper around internal/cli.RunManager, kept as its own
// binary so existing Dockerfile targets and Helm chart image references
// don't have to change. If you just want to install pg-regression-radar
// locally, see cmd/pg-regression-radar for the single unified CLI instead
// (`go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest`
// then `pg-regression-radar manager ...`).
//
// Usage:
//
//	manager \
//	  --metrics-bind-address=:9090 \
//	  --health-probe-bind-address=:8081 \
//	  --pg-metrics-bind-address=:9091 \
//	  --webhook-bind-address=:8080 \
//	  --leader-elect=true \
//	  --leader-election-namespace=pg-regression-radar
package main

import (
	"os"

	"github.com/joao00001/pg-regression-radar/internal/cli"
)

func main() {
	cli.RunManager(os.Args[1:])
}
