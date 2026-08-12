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

// pg-regression-radar is the single, unified CLI for the project: one
// binary, one `go install`, with each of the project's four run modes
// (operator, manager, collector, ingester) available as a subcommand. It's
// the simplest way to install pg-regression-radar for local use or
// evaluation:
//
//	go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest
//	pg-regression-radar operator --dsn "host:5432/dbname?sslmode=disable"
//
// See docs/installation.md for every install option and
// docs/getting-started.md for what to do once it's running.
//
// Every subcommand has its own --help, --version, and (where applicable)
// --dry-run: e.g. `pg-regression-radar operator --help`.
//
// The four modes are also still available as their own standalone binaries
// (cmd/operator, cmd/manager, cmd/collector, cmd/ingester) — those exist
// purely so the Dockerfile's per-mode container images and the Helm chart's
// image references don't need one process per container to also parse a
// subcommand argument. Both forms call the exact same code
// (internal/cli.Run*), so behaviour is identical either way.
package main

import (
	"fmt"
	"os"

	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/cli"
)

const usage = `pg-regression-radar detects Postgres query performance regressions and
correlates them with GitOps deploy events (ArgoCD, Argo Rollouts, Flux).

Usage:

  pg-regression-radar <command> [flags]

Commands:

  operator    All-in-one mode: Collector + Ingester + Correlation Engine +
              Alerting in a single process. Start here for a quick
              evaluation or a simple single-cluster deployment.
  manager     Kubernetes-native mode: a controller-runtime Manager that
              reconciles PostgresWatch/DeploySource/PerformanceRegression
              CRDs, with leader-election HA. Recommended for production.
  collector   Standalone Postgres metric scraper (pg_stat_statements ->
              Prometheus), without correlation/alerting/webhooks.
  ingester    Standalone deploy-event webhook receiver, without the
              collector/correlation/alerting pieces.
  version     Print version, commit, and build date.
  help        Show this message.

Run 'pg-regression-radar <command> --help' for a command's full flag list
(every command also accepts --version and, except manager, --dry-run).

Docs: https://joao00001.github.io/pg-regression-radar
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "operator":
		cli.RunOperator(args)
	case "manager":
		cli.RunManager(args)
	case "collector":
		cli.RunCollector(args)
	case "ingester":
		cli.RunIngester(args)
	case "version", "--version", "-v":
		fmt.Println(buildinfo.String("pg-regression-radar"))
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "pg-regression-radar: unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
