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

// ingester is the pg-regression-radar deploy-event webhook receiver binary.
// It accepts webhook payloads from ArgoCD, Argo Rollouts, and Flux and stores
// normalised DeployEvents which the Correlation Engine can query.
//
// This is a thin wrapper around internal/cli.RunIngester, kept as its own
// binary so existing Dockerfile targets and Helm chart image references
// don't have to change. If you just want to install pg-regression-radar
// locally, see cmd/pg-regression-radar for the single unified CLI instead
// (`go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest`
// then `pg-regression-radar ingester ...`).
//
// Usage:
//
//	ingester \
//	  --listen :8080 \
//	  --source-type argocd \
//	  --source-name prod-argocd \
//	  --postgres-watch-ref prod-watch \
//	  --app-name my-app \
//	  --cluster-name prod-cluster-1
package main

import (
	"os"

	"github.com/joao00001/pg-regression-radar/internal/cli"
)

func main() {
	os.Exit(cli.RunIngester(os.Args[1:]))
}
