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

package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// secretConsentLabel is the label a Secret's owner must set to opt that
// Secret in to being referenced by a PostgresWatch's dsnSecretRef or
// remoteClusterSecretRef.
//
// Why this exists: the manager's RBAC grants get/list/watch on every
// Secret in the cluster (see config/rbac/role.yaml — required for the
// remote/multi-cluster kubeconfig-Secret path, which by definition can't
// know in advance which namespace it needs to reach). Without this check,
// anyone who can create or edit a PostgresWatch in a namespace — a much
// weaker permission than being able to read Secrets in that namespace
// directly — could name *any* Secret there and have the manager read it on
// their behalf and use its contents as a DSN or a remote-cluster
// kubeconfig: a classic confused-deputy path, since the manager's own
// broader Secret-read privilege is exercised at the referencer's request,
// not the Secret owner's. Requiring this label turns "can create a
// PostgresWatch" into "can read this Secret" only for Secrets whose owner
// has explicitly opted in, closing that gap without needing a separate
// admission webhook or narrowing RBAC (which the multi-cluster path can't
// do reliably anyway, since the target namespace is only known once a
// PostgresWatch names it).
const (
	secretConsentLabel = "pg-regression-radar.io/allow-postgreswatch-access"
	secretConsentValue = "true"
)

// checkSecretConsent returns an error if secret is missing the consent
// label required before a PostgresWatch may use its contents. kind is a
// short human-readable description of what the Secret was being read for
// (e.g. "dsn secret", "remote cluster kubeconfig secret"), used only to
// make the returned error actionable.
func checkSecretConsent(secret *corev1.Secret, kind string) error {
	if secret.Labels[secretConsentLabel] == secretConsentValue {
		return nil
	}
	return fmt.Errorf(
		"%s %s/%s is missing the required consent label %s=%s — a PostgresWatch cannot reference a Secret its owner hasn't explicitly opted in, even though the manager's RBAC can read it; add the label to the Secret to allow this (see docs/multi-cluster.md#secret-consent-label)",
		kind, secret.Namespace, secret.Name, secretConsentLabel, secretConsentValue,
	)
}
