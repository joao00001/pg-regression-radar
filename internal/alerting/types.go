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

package alerting

// DestinationPolicy controls how the alerting destination is validated and
// resolved when BuildNotifier is called. See BuildConfig.DestinationPolicy
// and docs/alerting.md#destination-policies for the full description of each
// mode.
type DestinationPolicy string

const (
	// DestinationPolicyPermissive accepts any URL that passes the static SSRF
	// blocklist (no loopback, link-local, or cloud-metadata targets). This is
	// the default and is backward-compatible with all existing installations.
	DestinationPolicyPermissive DestinationPolicy = "permissive"

	// DestinationPolicyAllowlist requires the webhook URL's host to also
	// appear in BuildConfig.AllowedDestinations. Reconciliation fails with a
	// clear message when the host is not in the list.
	DestinationPolicyAllowlist DestinationPolicy = "allowlist"

	// DestinationPolicyRelayOnly ignores the CRD-level URL entirely and
	// always sends alerts to the relay URL supplied in
	// BuildConfig.RelayUrl. BuildNotifier returns an error when RelayUrl is
	// empty.
	DestinationPolicyRelayOnly DestinationPolicy = "relay-only"
)
