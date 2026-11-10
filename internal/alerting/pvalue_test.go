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
	"strings"
	"testing"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func withPValue(p float64) v1alpha1.PerformanceRegression {
	r := sampleRegression(v1alpha1.StatusDetected)
	r.PValue = &p
	return r
}

func TestFormatPValue(t *testing.T) {
	zero, tiny, typical := 0.0, 0.000012345, 0.0213
	for _, tt := range []struct {
		in   *float64
		want string
	}{
		{nil, ""},
		{&zero, "0"},
		{&tiny, "1.2e-05"},
		{&typical, "0.021"},
	} {
		if got := formatPValue(tt.in); got != tt.want {
			t.Errorf("formatPValue(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatters_ShowThePValueAndLabelConfidenceAsOneMinusP(t *testing.T) {
	for name, f := range map[string]Formatter{
		"slack":     SlackFormatter{},
		"teams":     TeamsFormatter{},
		"pagerduty": NewPagerDutyFormatter("rk"),
	} {
		t.Run(name, func(t *testing.T) {
			body, _, err := f.Format(withPValue(0.0213), "c1")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "0.021") {
				t.Errorf("payload does not carry the p-value:\n%s", body)
			}
			if name != "pagerduty" && !strings.Contains(string(body), "1 − p") {
				t.Errorf("confidence is not labelled as 1 − p:\n%s", body)
			}

			// Without a p-value (test did not run) nothing is invented.
			r := sampleRegression(v1alpha1.StatusDetected)
			body, _, err = f.Format(r, "c1")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.ToLower(string(body)), "p-value") || strings.Contains(string(body), "p_value") {
				t.Errorf("payload mentions a p-value although none was computed:\n%s", body)
			}
		})
	}
}

func TestCustomFormatter_ExposesPValue(t *testing.T) {
	f, err := NewCustomFormatter(`{{.PValueText}}|{{.PValue}}`, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := f.Format(withPValue(0.0213), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "0.021|0.0213" {
		t.Errorf("rendered %q, want %q", body, "0.021|0.0213")
	}
	body, _, _ = f.Format(sampleRegression(v1alpha1.StatusDetected), "c1")
	if string(body) != "|0" {
		t.Errorf("rendered %q for a nil p-value, want %q", body, "|0")
	}
}
