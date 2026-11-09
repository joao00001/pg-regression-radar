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
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/telemetry/otlpexport"
)

// otlpFlushTimeout bounds how long a stopped watch's OTLP exporter keeps
// trying to deliver what is already queued.
const otlpFlushTimeout = 5 * time.Second

// watchSecrets holds the values of the optional Secrets a PostgresWatch
// references for its telemetry integrations (as opposed to its DSN).
type watchSecrets struct {
	// promBearerToken is spec.sampleSource.prometheus.bearerTokenSecretRef's value.
	promBearerToken string
	// otlpHeaders is spec.otlpExport.headersSecretRef's value
	// (OTEL_EXPORTER_OTLP_HEADERS format).
	otlpHeaders string
}

// resolveWatchSecrets reads the Secrets named by the watch's telemetry
// settings from the watch's own namespace in the hub cluster. Like every
// Secret a PostgresWatch references, each must carry the consent label (see
// checkSecretConsent) -- the manager's RBAC could read any of them.
func (r *PostgresWatchReconciler) resolveWatchSecrets(ctx context.Context, watch *radarv1alpha1.PostgresWatch) (watchSecrets, error) {
	var out watchSecrets
	var err error
	if usesPrometheusSampleSource(watch.Spec) && watch.Spec.SampleSource.Prometheus != nil {
		if ref := watch.Spec.SampleSource.Prometheus.BearerTokenSecretRef; ref != nil {
			if out.promBearerToken, err = r.readConsentedSecret(ctx, watch.Namespace, ref, "prometheus bearer token secret"); err != nil {
				return out, err
			}
		}
	}
	if watch.Spec.OTLPExport != nil && watch.Spec.OTLPExport.HeadersSecretRef != nil {
		if out.otlpHeaders, err = r.readConsentedSecret(ctx, watch.Namespace, watch.Spec.OTLPExport.HeadersSecretRef, "otlp export headers secret"); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (r *PostgresWatchReconciler) readConsentedSecret(ctx context.Context, namespace string, ref *radarv1alpha1.SecretKeySelector, kind string) (string, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: namespace, Name: ref.Name}
	if err := r.Get(ctx, key, &secret); err != nil {
		return "", fmt.Errorf("fetch %s %s: %w", kind, key, err)
	}
	if err := checkSecretConsent(&secret, kind); err != nil {
		return "", err
	}
	val, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("%s %s has no key %q", kind, key, ref.Key)
	}
	return string(val), nil
}

// buildOTLPExporter returns nil, nil when the watch has no spec.otlpExport.
// The endpoint is held to the same manager-wide outbound policy as alert
// destinations: PostgresWatch authors choose it, but the manager makes the
// request.
func (r *PostgresWatchReconciler) buildOTLPExporter(key types.NamespacedName, watch *radarv1alpha1.PostgresWatch, headersRaw string, reg prometheus.Registerer) (*otlpexport.Exporter, error) {
	cfg := watch.Spec.OTLPExport
	if cfg == nil {
		return nil, nil
	}
	if r.AlertingDestinationPolicy == alerting.DestinationPolicyRelayOnly {
		return nil, fmt.Errorf("postgreswatch %s: spec.otlpExport is not allowed under the %q destination policy, which only permits the configured relay",
			key, alerting.DestinationPolicyRelayOnly)
	}
	endpoint, err := otlpexport.NormalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("postgreswatch %s: spec.otlpExport.endpoint: %w", key, err)
	}
	if err := alerting.ValidateOutboundURL(endpoint, r.AllowedAlertDestinations); err != nil {
		return nil, fmt.Errorf("postgreswatch %s: spec.otlpExport.endpoint rejected: %w", key, err)
	}
	headers, err := otlpexport.ParseHeaders(headersRaw)
	if err != nil {
		return nil, fmt.Errorf("postgreswatch %s: spec.otlpExport.headersSecretRef: %w", key, err)
	}
	exp, err := otlpexport.New(otlpexport.Config{
		Endpoint:       endpoint,
		Encoding:       otlpexport.Encoding(cfg.Encoding),
		Headers:        headers,
		Timeout:        time.Duration(cfg.TimeoutSeconds) * time.Second,
		Gzip:           cfg.Gzip,
		ServiceVersion: buildinfo.Version,
		ResourceAttributes: map[string]string{
			"k8s.namespace.name":             watch.Namespace,
			"k8s.cluster.name":               watch.Spec.ClusterName,
			"pg_regression_radar.watch.name": watch.Name,
		},
		Logger:     r.Logger,
		Registerer: reg,
	})
	if err != nil {
		return nil, fmt.Errorf("postgreswatch %s: create otlp exporter: %w", key, err)
	}
	return exp, nil
}
