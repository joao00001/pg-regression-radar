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

// Package lab_test guards the manifests under examples/lab. The lab is the
// first thing a new user runs, so a typo in a CR or a drift between the two
// orders-api versions must fail in CI instead of in front of them.
package lab_test

import (
	"bytes"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

const labNamespace = "pgrr-lab"

// docs splits a multi-document YAML file into its non-empty documents.
func docs(t *testing.T, file string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	var out [][]byte
	for _, d := range bytes.Split(raw, []byte("\n---\n")) {
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal(d, &meta); err != nil {
			t.Fatalf("%s: invalid YAML document: %v", file, err)
		}
		if meta.Kind != "" {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s has no documents", file)
	}
	return out
}

type object struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	raw []byte
}

func objects(t *testing.T, file string) []object {
	t.Helper()
	var out []object
	for _, d := range docs(t, file) {
		var o object
		if err := yaml.Unmarshal(d, &o); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		o.raw = d
		out = append(out, o)
	}
	return out
}

func find(t *testing.T, objs []object, kind, name string) object {
	t.Helper()
	for _, o := range objs {
		if o.Kind == kind && o.Metadata.Name == name {
			return o
		}
	}
	t.Fatalf("no %s named %q", kind, name)
	return object{}
}

var clusterScoped = map[string]bool{
	"Namespace": true, "CustomResourceDefinition": true, "ClusterRole": true, "ClusterRoleBinding": true,
}

func TestEveryNamespacedObjectLandsInTheLabNamespace(t *testing.T) {
	for _, file := range []string{"00-radar.yaml", "10-lab.yaml", "20-bad-deploy.yaml"} {
		for _, o := range objects(t, file) {
			if clusterScoped[o.Kind] {
				continue
			}
			if o.Metadata.Namespace != labNamespace {
				t.Errorf("%s: %s/%s has namespace %q, want %q (kubectl apply would put it in the current namespace)",
					file, o.Kind, o.Metadata.Name, o.Metadata.Namespace, labNamespace)
			}
		}
	}
}

func TestRadarManifestPinsAnImageAndOneReplica(t *testing.T) {
	objs := objects(t, "00-radar.yaml")
	var dep appsv1.Deployment
	if err := yaml.Unmarshal(find(t, objs, "Deployment", "pgrr-manager").raw, &dep); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dep.Spec.Template.Spec.Containers[0].Image, "ghcr.io/joao00001/pg-regression-radar/manager:") {
		t.Errorf("unexpected manager image %q", dep.Spec.Template.Spec.Containers[0].Image)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Errorf("the lab runs a single manager replica")
	}
	crds := 0
	for _, o := range objs {
		if o.Kind == "CustomResourceDefinition" {
			crds++
		}
	}
	if crds != 4 {
		t.Errorf("expected the 4 radar CRDs, found %d", crds)
	}
}

func TestLabCustomResourcesAreValidAndConsistent(t *testing.T) {
	objs := objects(t, "10-lab.yaml")

	var watch radarv1alpha1.PostgresWatch
	if err := yaml.UnmarshalStrict(find(t, objs, "PostgresWatch", "orders").raw, &watch); err != nil {
		t.Fatalf("PostgresWatch does not decode strictly: %v", err)
	}
	var src radarv1alpha1.DeploySource
	if err := yaml.UnmarshalStrict(find(t, objs, "DeploySource", "orders-api").raw, &src); err != nil {
		t.Fatalf("DeploySource does not decode strictly: %v", err)
	}

	// CRD validation floors (see api/v1alpha1).
	if watch.Spec.ScrapeIntervalSeconds < 5 {
		t.Errorf("scrapeIntervalSeconds %d is below the CRD minimum of 5", watch.Spec.ScrapeIntervalSeconds)
	}
	if watch.Spec.WindowMinutes < 1 || watch.Spec.MinSamples < 1 || watch.Spec.MinExecutions < 1 {
		t.Errorf("windowMinutes, minSamples and minExecutions must be at least 1")
	}
	if !watch.Spec.CapturePlans {
		t.Errorf("the lab's headline output is the plan diff; capturePlans must be on")
	}

	// The watch reads its DSN from a Secret the owner opted in.
	if watch.Spec.DSNSecretRef == nil {
		t.Fatal("dsnSecretRef is required")
	}
	secret := find(t, objs, "Secret", watch.Spec.DSNSecretRef.Name)
	if secret.Metadata.Labels["pg-regression-radar.io/allow-postgreswatch-access"] != "true" {
		t.Errorf("Secret %s lacks the consent label the manager requires", secret.Metadata.Name)
	}
	var sec corev1.Secret
	if err := yaml.Unmarshal(secret.raw, &sec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sec.StringData[watch.Spec.DSNSecretRef.Key], "@postgres.pgrr-lab.svc") {
		t.Errorf("Secret key %q must hold a DSN pointing at the postgres Service", watch.Spec.DSNSecretRef.Key)
	}

	// The alert URL must point at the sink Service, on its port, and must not
	// be a loopback address (the radar refuses those as alert destinations).
	if watch.Spec.Alerting == nil {
		t.Fatal("alerting is required")
	}
	u, err := url.Parse(watch.Spec.Alerting.URL)
	if err != nil {
		t.Fatal(err)
	}
	if h := u.Hostname(); h == "localhost" || strings.HasPrefix(h, "127.") {
		t.Errorf("alert URL host %q is a loopback address", h)
	}
	if want := "alert-sink." + labNamespace + ".svc"; !strings.HasPrefix(u.Hostname(), want) {
		t.Errorf("alert URL host %q does not name the alert-sink Service (%s...)", u.Hostname(), want)
	}
	var svc corev1.Service
	if err := yaml.Unmarshal(find(t, objs, "Service", "alert-sink").raw, &svc); err != nil {
		t.Fatal(err)
	}
	if got := u.Port(); len(svc.Spec.Ports) != 1 || got != itoa(svc.Spec.Ports[0].Port) {
		t.Errorf("alert URL port %q does not match the alert-sink Service port", got)
	}

	// The DeploySource watches the lab's own Deployment and the watch above.
	if src.Spec.PostgresWatchRef != watch.Name {
		t.Errorf("postgresWatchRef %q != watch %q", src.Spec.PostgresWatchRef, watch.Name)
	}
	if src.Spec.SourceType != "kubernetes" || src.Spec.WorkloadKind != "Deployment" {
		t.Errorf("expected sourceType kubernetes on a Deployment, got %q/%q", src.Spec.SourceType, src.Spec.WorkloadKind)
	}
	find(t, objs, "Deployment", src.Spec.AppName)
}

func itoa(i int32) string { return strconv.Itoa(int(i)) }

func ordersAPI(t *testing.T, file string) appsv1.Deployment {
	t.Helper()
	var d appsv1.Deployment
	if err := yaml.UnmarshalStrict(find(t, objects(t, file), "Deployment", "orders-api").raw, &d); err != nil {
		t.Fatalf("%s: orders-api does not decode strictly: %v", file, err)
	}
	return d
}

// The bad deploy must be a real rollout whose only effect is the migration:
// same application container, same queries. If anything else changed, the
// query text could differ and the radar would see a new query, not a slower
// one.
func TestBadDeployChangesOnlyTheMigration(t *testing.T) {
	v1 := ordersAPI(t, "10-lab.yaml")
	v2 := ordersAPI(t, "20-bad-deploy.yaml")

	if v1.Spec.Template.Annotations["pgrr-lab/version"] == v2.Spec.Template.Annotations["pgrr-lab/version"] {
		t.Fatal("the two versions must differ in pod template, or applying v2 is not a rollout")
	}
	up := strings.Join(v1.Spec.Template.Spec.InitContainers[0].Command, " ")
	down := strings.Join(v2.Spec.Template.Spec.InitContainers[0].Command, " ")
	if !strings.Contains(up, "CREATE UNIQUE INDEX IF NOT EXISTS orders_order_ref_idx") {
		t.Errorf("v1 migration must (re)create the index: %s", up)
	}
	if !strings.Contains(down, "DROP INDEX IF EXISTS orders_order_ref_idx") {
		t.Errorf("v2 migration must drop the index: %s", down)
	}

	// Everything else is identical.
	norm := func(d appsv1.Deployment) appsv1.Deployment {
		d = *d.DeepCopy()
		d.Spec.Template.Annotations = nil
		d.Spec.Template.Spec.InitContainers[0].Command = nil
		return d
	}
	if !reflect.DeepEqual(norm(v1), norm(v2)) {
		t.Errorf("orders-api v1 and v2 differ in more than the version annotation and the migration")
	}
}

func TestSeedAndMigrationsAgreeOnTheIndexName(t *testing.T) {
	raw, err := os.ReadFile("10-lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "orders_order_ref_idx"); n < 2 {
		t.Errorf("expected the seed and the v1 migration to reference orders_order_ref_idx, found %d mentions", n)
	}
	if !strings.Contains(string(raw), "g::bigint * 7919") {
		t.Errorf("order_ref must be computed in bigint: g * 7919 overflows int4 above 271k rows")
	}
}
