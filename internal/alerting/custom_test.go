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
	"encoding/json"
	"strings"
	"testing"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// TestNewCustomFormatter_RejectsInvalidTemplate verifies a broken template
// is reported at construction time (so BuildNotifier can fail fast at
// startup) rather than only surfacing on the first detected regression.
func TestNewCustomFormatter_RejectsInvalidTemplate(t *testing.T) {
	if _, err := NewCustomFormatter("{{ .Unclosed", ""); err == nil {
		t.Fatal("expected an error for an unparseable template, got nil")
	}
}

// TestCustomFormatter_Format verifies a template can reference every
// documented CustomTemplateData field, that pre-formatted fields (e.g.
// ConfidencePercent) match what the built-in formatters render, and that
// contentType defaults to application/json when left empty.
func TestCustomFormatter_Format(t *testing.T) {
	const tmpl = `{"cluster":"{{.ClusterName}}","event":"{{.DeployEventID}}","query_id":{{.QueryID}},"confidence":"{{.ConfidencePercent}}","external_cause":{{.ExternalCauseSuspected}}}`

	f, err := NewCustomFormatter(tmpl, "")
	if err != nil {
		t.Fatalf("NewCustomFormatter: %v", err)
	}

	reg := sampleRegression(v1alpha1.StatusDetected)
	body, contentType, err := f.Format(reg, "prod-east")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("expected default contentType application/json, got %q", contentType)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode rendered template as JSON: %v (body=%s)", err, body)
	}
	if decoded["cluster"] != "prod-east" {
		t.Errorf("expected cluster=prod-east, got %v", decoded["cluster"])
	}
	if decoded["event"] != reg.DeployEventID {
		t.Errorf("expected event=%q, got %v", reg.DeployEventID, decoded["event"])
	}
	if decoded["confidence"] != "97%" {
		t.Errorf("expected confidence=97%%, got %v", decoded["confidence"])
	}
}

// TestCustomFormatter_CustomContentType verifies an explicit contentType
// overrides the application/json default, for templates rendering a
// non-JSON body (e.g. plain text, form-encoded).
func TestCustomFormatter_CustomContentType(t *testing.T) {
	f, err := NewCustomFormatter("regression on {{.ClusterName}}: query {{.QueryID}}", "text/plain")
	if err != nil {
		t.Fatalf("NewCustomFormatter: %v", err)
	}

	body, contentType, err := f.Format(sampleRegression(v1alpha1.StatusDetected), "prod-east")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if contentType != "text/plain" {
		t.Errorf("expected contentType=text/plain, got %q", contentType)
	}
	if !strings.Contains(string(body), "prod-east") {
		t.Errorf("expected rendered body to contain the cluster name, got %q", body)
	}
}
