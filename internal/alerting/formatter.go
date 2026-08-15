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

import (
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// Formatter renders a detected regression into a notification body and its
// Content-Type header. WebhookNotifier owns only the HTTP transport (POST,
// timeout, status-code handling); Formatter owns the actual payload layout,
// so a new destination (Teams, PagerDuty, a user's own webhook consumer)
// only ever means a new Formatter implementation, never a change to
// WebhookNotifier itself. See BuildNotifier for how a Formatter is picked
// from a --alert-format flag or a PostgresWatch's spec.alerting.format.
//
// Format is only ever called for a regression already known to have
// Status == v1alpha1.StatusDetected — WebhookNotifier.Notify handles the
// no-op case for every other status itself, so implementations don't need
// to check r.Status again.
type Formatter interface {
	Format(r v1alpha1.PerformanceRegression, clusterName string) (body []byte, contentType string, err error)
}

type noopFormatter struct{}

func (noopFormatter) Format(v1alpha1.PerformanceRegression, string) ([]byte, string, error) {
	return []byte("{}"), "application/json", nil
}
