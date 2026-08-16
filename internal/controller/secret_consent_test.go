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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCheckSecretConsent_MissingLabel_ReturnsError(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-secret", Namespace: "default"},
	}

	err := checkSecretConsent(secret, "dsn secret")
	if err == nil {
		t.Fatal("expected an error for a Secret with no consent label")
	}
	if !strings.Contains(err.Error(), secretConsentLabel) {
		t.Fatalf("expected the error to name the missing label %q, got: %v", secretConsentLabel, err)
	}
	if !strings.Contains(err.Error(), "some-secret") {
		t.Fatalf("expected the error to name the Secret, got: %v", err)
	}
}

func TestCheckSecretConsent_WrongLabelValue_ReturnsError(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-secret",
			Namespace: "default",
			// A common mistake — the key is right but the value isn't the
			// exact expected "true" — must not be treated as consent.
			Labels: map[string]string{secretConsentLabel: "yes"},
		},
	}

	if err := checkSecretConsent(secret, "dsn secret"); err == nil {
		t.Fatal("expected an error when the consent label's value isn't exactly \"true\"")
	}
}

func TestCheckSecretConsent_OtherLabelsPresent_StillRequiresConsentLabel(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-secret",
			Namespace: "default",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "cnpg"},
		},
	}

	if err := checkSecretConsent(secret, "dsn secret"); err == nil {
		t.Fatal("expected an error: unrelated labels must not satisfy the consent check")
	}
}

func TestCheckSecretConsent_LabelPresent_ReturnsNil(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
	}

	if err := checkSecretConsent(secret, "dsn secret"); err != nil {
		t.Fatalf("expected no error for a properly labeled Secret, got: %v", err)
	}
}

func TestCheckSecretConsent_ValidationMatrix_ForDSNAndRemoteKubeconfig(t *testing.T) {
	kinds := []string{"dsn secret", "remote cluster kubeconfig secret"}

	for _, kind := range kinds {
		t.Run(kind+"/missing-label-rejected-with-actionable-error", func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "missing-label", Namespace: "default"},
			}

			err := checkSecretConsent(secret, kind)
			if err == nil {
				t.Fatal("expected an error for a Secret with no consent label")
			}
			msg := err.Error()
			if !strings.Contains(msg, kind) {
				t.Fatalf("expected error to include kind %q, got: %v", kind, err)
			}
			if !strings.Contains(msg, secretConsentLabel+"="+secretConsentValue) {
				t.Fatalf("expected error to include required label/value, got: %v", err)
			}
			if !strings.Contains(msg, "add the label to the Secret to allow this") {
				t.Fatalf("expected actionable guidance in error, got: %v", err)
			}
			if !strings.Contains(msg, "docs/multi-cluster.md#secret-consent-label") {
				t.Fatalf("expected docs reference in error, got: %v", err)
			}
		})

		t.Run(kind+"/invalid-label-value-rejected", func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-value",
					Namespace: "default",
					Labels:    map[string]string{secretConsentLabel: "yes"},
				},
			}
			if err := checkSecretConsent(secret, kind); err == nil {
				t.Fatal("expected an error when the consent label value is not exactly true")
			}
		})

		t.Run(kind+"/valid-label-accepted", func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "allowed",
					Namespace: "default",
					Labels:    map[string]string{secretConsentLabel: secretConsentValue},
				},
			}
			if err := checkSecretConsent(secret, kind); err != nil {
				t.Fatalf("expected no error for a properly labeled Secret, got: %v", err)
			}
		})
	}
}
