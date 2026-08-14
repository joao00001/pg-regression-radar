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

// TestTeamsFormatter_Format verifies the MessageCard payload's shape and
// that every field SlackFormatter reports also shows up here — the two
// built-in chat formats are meant to be equivalent, just under each
// platform's own field names.
func TestTeamsFormatter_Format(t *testing.T) {
	reg := sampleRegression(v1alpha1.StatusDetected)

	body, contentType, err := TeamsFormatter{}.Format(reg, "prod-east")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("expected application/json, got %q", contentType)
	}

	var card teamsMessageCard
	if err := json.Unmarshal(body, &card); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if card.Type != "MessageCard" {
		t.Errorf("expected @type=MessageCard, got %q", card.Type)
	}
	if card.ThemeColor != teamsColorDanger {
		t.Errorf("expected themeColor=%s for a plain detected regression, got %q", teamsColorDanger, card.ThemeColor)
	}
	if !strings.Contains(card.Summary, "prod-east") {
		t.Errorf("expected summary to reference the cluster name, got %q", card.Summary)
	}
	if len(card.Sections) != 1 {
		t.Fatalf("expected exactly one section, got %d", len(card.Sections))
	}

	facts := make(map[string]string, len(card.Sections[0].Facts))
	for _, f := range card.Sections[0].Facts {
		facts[f.Name] = f.Value
	}
	wantContains := map[string]string{
		"Deploy Event":    reg.DeployEventID,
		"Query ID":        "987654321",
		"Query (excerpt)": reg.QueryText,
		"Confidence":      "97",
	}
	for name, want := range wantContains {
		got, ok := facts[name]
		if !ok {
			t.Errorf("missing fact %q in teams payload", name)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("fact %q: expected value to contain %q, got %q", name, want, got)
		}
	}
}

// TestTeamsFormatter_ExternalCauseSuspected mirrors
// TestNotify_ExternalCauseSuspected's Slack coverage for the Teams format.
func TestTeamsFormatter_ExternalCauseSuspected(t *testing.T) {
	reg := sampleRegression(v1alpha1.StatusDetected)
	reg.ExternalCauseSuspected = true

	body, _, err := TeamsFormatter{}.Format(reg, "test-cluster")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	var card teamsMessageCard
	if err := json.Unmarshal(body, &card); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if card.ThemeColor != teamsColorWarning {
		t.Errorf("expected themeColor=%s when ExternalCauseSuspected, got %q", teamsColorWarning, card.ThemeColor)
	}

	found := false
	for _, f := range card.Sections[0].Facts {
		if strings.Contains(f.Value, "External cause suspected") {
			found = true
		}
	}
	if !found {
		t.Error("expected a fact explaining the external-cause suspicion, found none")
	}
}
