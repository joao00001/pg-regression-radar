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

// Package buildinfo holds version metadata for the pg-regression-radar
// binaries, set at build time via `-ldflags -X` rather than read from
// runtime/debug.BuildInfo: the latter only knows the module's pseudo-version
// from `go build`'s own resolution, which is empty/unhelpful for a `go build`
// invoked directly on a checked-out commit (as the Dockerfile and local
// development both do), and never reflects a hand-chosen release tag.
//
// The Dockerfile sets all three via build args:
//
//	docker build \
//	  --build-arg VERSION=v0.1.0 \
//	  --build-arg COMMIT=$(git rev-parse HEAD) \
//	  --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
//	  --target operator -t pg-regression-radar/operator .
//
// A plain `go build`/`go run` (no -ldflags) gets the zero-value defaults
// below, which is deliberately honest about "this wasn't a tracked release
// build" rather than printing a misleading version string.
package buildinfo

import "fmt"

var (
	// Version is the release tag this binary was built from (e.g. "v0.1.0").
	Version = "dev"
	// Commit is the git commit SHA this binary was built from.
	Commit = "none"
	// Date is the UTC build timestamp (RFC3339).
	Date = "unknown"
)

// String renders a single-line "<binary> <version> (commit <commit>, built
// <date>)" string, used by every cmd/*'s --version flag and startup log line
// so the same identifying information is available whether a human is
// looking or the line ends up in structured logs.
func String(binary string) string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", binary, Version, Commit, Date)
}
