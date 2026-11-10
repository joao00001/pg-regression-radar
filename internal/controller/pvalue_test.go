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
	"strings"
	"testing"
	"time"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func TestApplyRegressionStatus_PValue(t *testing.T) {
	tiny, typical := 3.2e-9, 0.0213456

	for _, tt := range []struct {
		name string
		p    *float64
		want string
		msg  string
	}{
		{"tiny keeps its magnitude", &tiny, "3.2e-09", "p-value 3.2e-09"},
		{"typical keeps four significant digits", &typical, "0.02135", "p-value 0.021"},
		{"absent stays empty, not 0", nil, "", "confidence score"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			obj := &radarv1alpha1.PerformanceRegression{}
			applyRegressionStatus(obj, dto.PerformanceRegression{
				QueryID: 1, Status: dto.StatusDetected, ConfidenceScore: 0.9787, LatencyChangeFactor: 3,
				PValue: tt.p, CreatedAt: time.Now(),
			})
			if obj.Status.PValue != tt.want {
				t.Errorf("status.pValue = %q, want %q", obj.Status.PValue, tt.want)
			}
			if obj.Status.ConfidenceScore != "0.9787" {
				t.Errorf("status.confidenceScore = %q, want it unchanged", obj.Status.ConfidenceScore)
			}
			var msg string
			for _, c := range obj.Status.Conditions {
				if c.Type == "Detected" {
					msg = c.Message
				}
			}
			if !strings.Contains(msg, tt.msg) || strings.Contains(msg, "%") {
				t.Errorf("Detected condition message = %q, want it to contain %q and no percentage", msg, tt.msg)
			}
		})
	}
}
