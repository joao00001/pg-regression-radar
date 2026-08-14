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
	"bytes"
	"fmt"
	"text/template"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// CustomTemplateData is what a user-supplied --alert-template-file (CLI) or
// spec.alerting.customTemplate (CRD) template can reference. Fields are
// pre-formatted the same way the built-in Slack/Teams/PagerDuty formatters
// format them (e.g. ConfidencePercent is already "97%", not the raw 0.97),
// so a template author renders the same numbers this project's own built-in
// layouts show without reimplementing that formatting. See
// docs/alerting.md#custom-format for the full field list and a worked
// example.
type CustomTemplateData struct {
	ClusterName            string
	DeployEventID          string
	QueryID                int64
	QueryText              string
	ConfidenceScore        float64
	ConfidencePercent      string
	MeanLatencyBeforeMs    string
	MeanLatencyAfterMs     string
	LatencyChangeFactor    string
	ChangePointRFC3339     string
	ExternalCauseSuspected bool
	PlanDiffSummary        string
}

// CustomFormatter renders a notification body from a user-supplied Go
// text/template. It exists for any destination that isn't Slack, Teams, or
// PagerDuty-shaped — a generic HTTP endpoint, an internal on-call tool, an
// n8n/Zapier relay, or anything else with its own expected JSON (or
// non-JSON) shape — without this project needing a built-in Formatter for
// every such tool.
type CustomFormatter struct {
	tmpl        *template.Template
	contentType string
}

// NewCustomFormatter parses tmplSource (Go text/template syntax, executed
// against CustomTemplateData) once at construction, so a broken template is
// reported immediately by BuildNotifier rather than on the first detected
// regression. contentType defaults to "application/json" when empty, since
// that is what most webhook-consuming tools expect regardless of how the
// body itself is shaped.
func NewCustomFormatter(tmplSource, contentType string) (*CustomFormatter, error) {
	tmpl, err := template.New("pg-regression-radar-custom-alert").Parse(tmplSource)
	if err != nil {
		return nil, fmt.Errorf("alerting: parse custom template: %w", err)
	}
	if contentType == "" {
		contentType = "application/json"
	}
	return &CustomFormatter{tmpl: tmpl, contentType: contentType}, nil
}

// Format implements Formatter.
func (f *CustomFormatter) Format(r v1alpha1.PerformanceRegression, clusterName string) ([]byte, string, error) {
	data := CustomTemplateData{
		ClusterName:            clusterName,
		DeployEventID:          r.DeployEventID,
		QueryID:                r.QueryID,
		QueryText:              r.QueryText,
		ConfidenceScore:        r.ConfidenceScore,
		ConfidencePercent:      fmt.Sprintf("%.0f%%", r.ConfidenceScore*100),
		MeanLatencyBeforeMs:    fmt.Sprintf("%.2f", r.MeanLatencyBefore),
		MeanLatencyAfterMs:     fmt.Sprintf("%.2f", r.MeanLatencyAfter),
		LatencyChangeFactor:    fmt.Sprintf("%.2fx", r.LatencyChangeFactor),
		ChangePointRFC3339:     r.DetectedChangeAt.Format(time.RFC3339),
		ExternalCauseSuspected: r.ExternalCauseSuspected,
		PlanDiffSummary:        r.PlanDiffSummary,
	}

	var buf bytes.Buffer
	if err := f.tmpl.Execute(&buf, data); err != nil {
		return nil, "", fmt.Errorf("alerting: execute custom template: %w", err)
	}
	return buf.Bytes(), f.contentType, nil
}
