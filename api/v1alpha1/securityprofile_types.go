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

package v1alpha1

// SecurityProfile governs how strictly the manager enforces the cluster
// registry when a PostgresWatch references a remote cluster. The profile is
// set at the manager/operator level, not per-watch.
type SecurityProfile string

const (
	// SecurityProfileControlled is the default profile. Both
	// spec.remoteClusterRef (the new registry path) and the deprecated
	// spec.remoteClusterSecretRef (the old arbitrary-Secret path) are
	// accepted. A deprecation warning is logged whenever the old path is
	// used, to help operators migrate to the registry model.
	SecurityProfileControlled SecurityProfile = "controlled"

	// SecurityProfileHardened rejects spec.remoteClusterSecretRef outright:
	// only pre-registered clusters (spec.remoteClusterRef pointing at a
	// PostgresRadarCluster) are allowed. Use this profile when you want
	// operators — not watch creators — to be the sole authority over which
	// remote clusters are reachable.
	SecurityProfileHardened SecurityProfile = "hardened"
)
