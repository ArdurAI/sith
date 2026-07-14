// SPDX-License-Identifier: Apache-2.0
//go:build e2e && helm

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	helmContractVersion = "v4.2.2"
	validHubImage       = "registry.example.invalid/sith/hub@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type helmProfileResources struct {
	requests map[string]string
	limits   map[string]string
}

var hubProfileResources = map[string]helmProfileResources{
	"light": {
		requests: map[string]string{"cpu": "100m", "memory": "128Mi"},
		limits:   map[string]string{"cpu": "500m", "memory": "512Mi"},
	},
	"heavy": {
		requests: map[string]string{"cpu": "500m", "memory": "512Mi"},
		limits:   map[string]string{"cpu": "2", "memory": "2Gi"},
	},
}

func TestHelmHubChartContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	helm := os.Getenv("HELM_BIN")
	if helm == "" {
		helm = "helm"
	}
	if _, err := exec.LookPath(helm); err != nil {
		t.Fatalf("find Helm binary %q: %v", helm, err)
	}
	if output, err := runHelm(ctx, t, helm, root, "version", "--short"); err != nil || !strings.HasPrefix(strings.TrimSpace(output), helmContractVersion) {
		t.Fatalf("Helm version/output = %q / %v, want %s", output, err, helmContractVersion)
	}

	chart := filepath.Join(root, "charts", "sith-hub")
	lightValues := writeHelmValues(t, validHubValues())
	if output, err := runHelm(ctx, t, helm, root, "lint", chart, "--values", lightValues); err != nil {
		t.Fatalf("helm lint valid chart: %v\n%s", err, output)
	}
	lightRendered, err := runHelm(ctx, t, helm, root, "template", "sith-hub", chart, "--namespace", "sith-system", "--values", lightValues)
	if err != nil {
		t.Fatalf("helm template light profile: %v\n%s", err, lightRendered)
	}
	lightObjects := assertHelmHubRender(t, lightRendered, "light")
	heavyRendered, err := runHelm(ctx, t, helm, root, "template", "sith-hub", chart, "--namespace", "sith-system", "--values", writeHelmValues(t, profileHubValues("heavy")))
	if err != nil {
		t.Fatalf("helm template heavy profile: %v\n%s", err, heavyRendered)
	}
	heavyObjects := assertHelmHubRender(t, heavyRendered, "heavy")
	assertProfileOnlyChangesResources(t, lightObjects, heavyObjects)

	for name, invalid := range map[string]string{
		"mutable tag":                  strings.Replace(validHubValues(), validHubImage, "registry.example.invalid/sith/hub:latest", 1),
		"missing digest":               strings.Replace(validHubValues(), validHubImage, "registry.example.invalid/sith/hub", 1),
		"blank runtime secret":         strings.Replace(validHubValues(), "existingSecret: sith-runtime", "existingSecret: \"\"", 1),
		"unknown profile":              strings.Replace(validHubValues(), "profile: light", "profile: unbounded", 1),
		"unexpected image pull secret": validHubValues() + "\nimagePullSecrets:\n  password: must-not-render\n",
		"unexpected resources":         validHubValues() + "\nresources:\n  requests:\n    cpu: 999\n",
	} {
		t.Run(name, func(t *testing.T) {
			if output, err := runHelm(ctx, t, helm, root, "template", "sith-hub", chart, "--namespace", "sith-system", "--values", writeHelmValues(t, invalid)); err == nil {
				t.Fatalf("helm template accepted %s values:\n%s", name, output)
			}
		})
	}

	for name, invalid := range map[string]string{
		"mutable image":        strings.Replace(validHubValues(), validHubImage, "registry.example.invalid/sith/hub:latest", 1),
		"unknown profile":      strings.Replace(validHubValues(), "profile: light", "profile: unbounded", 1),
		"unexpected resources": validHubValues() + "\nresources:\n  requests:\n    cpu: 999\n",
	} {
		t.Run("skip schema validation "+name, func(t *testing.T) {
			if output, err := runHelm(ctx, t, helm, root, "template", "sith-hub", chart, "--namespace", "sith-system", "--skip-schema-validation", "--values", writeHelmValues(t, invalid)); err == nil {
				t.Fatalf("helm template --skip-schema-validation accepted %s:\n%s", name, output)
			}
		})
	}
}

func validHubValues() string {
	return profileHubValues("light")
}

func profileHubValues(profile string) string {
	return fmt.Sprintf(`profile: %s
image:
  reference: %s
runtime:
  existingSecret: sith-runtime
  sessionIssuer: https://issuer.sith.example
  sessionAudience: https://hub.sith.example
  sessionKeyID: session-2026-07
  proxyAddress: cluster-proxy.open-cluster-management.svc:8090
  proxyServerName: cluster-proxy.open-cluster-management.svc
  kubeAPIServerName: kubernetes
migration:
  existingSecret: sith-migration
  applicationRole: sith_app
`, profile, validHubImage)
}

func writeHelmValues(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write Helm values: %v", err)
	}
	return path
}

func runHelm(ctx context.Context, t *testing.T, helm, root string, args ...string) (string, error) {
	t.Helper()
	command := exec.CommandContext(ctx, helm, args...)
	command.Dir = root
	scratch := t.TempDir()
	command.Env = append(os.Environ(),
		"HELM_CACHE_HOME="+filepath.Join(scratch, "cache"),
		"HELM_CONFIG_HOME="+filepath.Join(scratch, "config"),
		"HELM_DATA_HOME="+filepath.Join(scratch, "data"),
		"HELM_PLUGINS="+filepath.Join(scratch, "plugins"),
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func assertHelmHubRender(t *testing.T, rendered, profile string) []*unstructured.Unstructured {
	t.Helper()
	if strings.Contains(rendered, "kind: Secret") || strings.Contains(rendered, "stringData:") || strings.Contains(rendered, "\ndata:") {
		t.Fatal("rendered chart created or embedded secret data")
	}
	objects := decodeHelmObjects(t, rendered)
	if len(objects) != 6 {
		t.Fatalf("rendered object count = %d, want 6", len(objects))
	}
	for _, object := range objects {
		if object.GetNamespace() != "" {
			t.Fatalf("%s unexpectedly sets namespace %q", object.GetKind(), object.GetNamespace())
		}
	}

	serviceAccountName, selectorLabels := assertHubDeployment(t, requiredHelmObject(t, objects, "Deployment"), profile)
	assertHubRBAC(t, requiredHelmObject(t, objects, "ClusterRole"), requiredHelmObject(t, objects, "ClusterRoleBinding"), serviceAccountName)
	assertMigrationJob(t, requiredHelmObject(t, objects, "Job"), profile)
	assertHubService(t, requiredHelmObject(t, objects, "Service"), selectorLabels)
	serviceAccount := requiredHelmObject(t, objects, "ServiceAccount")
	if value, found, _ := unstructured.NestedBool(serviceAccount.Object, "automountServiceAccountToken"); !found || !value {
		t.Fatal("hub ServiceAccount must explicitly mount its in-cluster token")
	}
	return objects
}

func decodeHelmObjects(t *testing.T, rendered string) []*unstructured.Unstructured {
	t.Helper()
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewBufferString(rendered), 4096)
	var objects []*unstructured.Unstructured
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode Helm output: %v", err)
		}
		if len(object) != 0 {
			objects = append(objects, &unstructured.Unstructured{Object: object})
		}
	}
	return objects
}

func requiredHelmObject(t *testing.T, objects []*unstructured.Unstructured, kind string) *unstructured.Unstructured {
	t.Helper()
	var found *unstructured.Unstructured
	for _, object := range objects {
		if object.GetKind() != kind {
			continue
		}
		if found != nil {
			t.Fatalf("rendered more than one %s", kind)
		}
		found = object
	}
	if found == nil {
		t.Fatalf("rendered no %s", kind)
	}
	return found
}

func assertHubDeployment(t *testing.T, deployment *unstructured.Unstructured, profile string) (string, map[string]any) {
	t.Helper()
	podSpec := nestedHelmMap(t, deployment.Object, "spec", "template", "spec")
	if value, found, _ := unstructured.NestedBool(podSpec, "automountServiceAccountToken"); !found || !value {
		t.Fatal("hub Deployment must deliberately mount its in-cluster service-account token")
	}
	assertHelmPodSecurity(t, podSpec, true)
	container := onlyHelmContainer(t, podSpec)
	if container["image"] != validHubImage || container["imagePullPolicy"] != "IfNotPresent" {
		t.Fatalf("hub image contract = %#v", container)
	}
	assertHelmContainerSecurity(t, container)
	assertHelmProfileResources(t, container, profile)
	environment := helmEnvironment(t, container)
	if len(environment) != 14 || environment["SITH_HUB_DATABASE_URL"] != "secret:sith-runtime/database-url" {
		t.Fatalf("hub environment = %#v", environment)
	}
	for _, name := range []string{"SITH_HUB_SESSION_PUBLIC_KEY_FILE", "SITH_HUB_SERVER_TLS_CERT_FILE", "SITH_HUB_SERVER_TLS_KEY_FILE", "SITH_HUB_PROXY_CA_FILE", "SITH_HUB_PROXY_CERT_FILE", "SITH_HUB_PROXY_KEY_FILE"} {
		if !strings.HasPrefix(environment[name], "/var/run/sith/runtime/") {
			t.Fatalf("hub mounted path %s = %q", name, environment[name])
		}
	}
	volumes, found, err := unstructured.NestedSlice(podSpec, "volumes")
	if err != nil || !found || len(volumes) != 1 {
		t.Fatalf("hub volumes = %#v / %v", volumes, err)
	}
	volume, ok := volumes[0].(map[string]any)
	if !ok {
		t.Fatalf("hub volume = %#v", volumes[0])
	}
	secret := nestedHelmMap(t, volume, "secret")
	if volume["name"] != "runtime" || secret["secretName"] != "sith-runtime" || helmInt(t, secret["defaultMode"]) != 288 {
		t.Fatalf("hub runtime Secret volume = %#v", volume)
	}
	serviceAccountName, ok := podSpec["serviceAccountName"].(string)
	if !ok || serviceAccountName == "" {
		t.Fatalf("hub service account = %#v", podSpec["serviceAccountName"])
	}
	return serviceAccountName, nestedHelmMap(t, deployment.Object, "spec", "template", "metadata", "labels")
}

func assertHubService(t *testing.T, service *unstructured.Unstructured, wantSelector map[string]any) {
	t.Helper()
	spec := nestedHelmMap(t, service.Object, "spec")
	if spec["type"] != "ClusterIP" {
		t.Fatalf("service spec = %#v", spec)
	}
	ports, found, err := unstructured.NestedSlice(spec, "ports")
	if err != nil || !found || len(ports) == 0 {
		t.Fatalf("service ports = %#v / %v", ports, err)
	}
	if !reflect.DeepEqual(nestedHelmMap(t, spec, "selector"), wantSelector) {
		t.Fatalf("service selector = %#v, want pod labels %#v", spec["selector"], wantSelector)
	}
}

func assertHubRBAC(t *testing.T, role, binding *unstructured.Unstructured, serviceAccountName string) {
	t.Helper()
	rules, found, err := unstructured.NestedSlice(role.Object, "rules")
	if err != nil || !found || len(rules) != 1 {
		t.Fatalf("hub ClusterRole rules = %#v / %v", rules, err)
	}
	rule, ok := rules[0].(map[string]any)
	if !ok || !reflect.DeepEqual(stringSlice(t, rule["apiGroups"]), []string{""}) || !reflect.DeepEqual(stringSlice(t, rule["resources"]), []string{"secrets"}) || !reflect.DeepEqual(stringSlice(t, rule["resourceNames"]), []string{"sith-reader"}) || !reflect.DeepEqual(stringSlice(t, rule["verbs"]), []string{"get"}) {
		t.Fatalf("hub ClusterRole is broader than the fixed reader Secret: %#v", rule)
	}
	roleRef := nestedHelmMap(t, binding.Object, "roleRef")
	if roleRef["kind"] != "ClusterRole" || roleRef["name"] != role.GetName() {
		t.Fatalf("ClusterRoleBinding roleRef = %#v", roleRef)
	}
	subjects, found, err := unstructured.NestedSlice(binding.Object, "subjects")
	if err != nil || !found || len(subjects) != 1 {
		t.Fatalf("ClusterRoleBinding subjects = %#v / %v", subjects, err)
	}
	subject, ok := subjects[0].(map[string]any)
	if !ok || subject["kind"] != "ServiceAccount" || subject["name"] != serviceAccountName || subject["namespace"] != "sith-system" {
		t.Fatalf("ClusterRoleBinding subject = %#v", subject)
	}
}

func assertMigrationJob(t *testing.T, job *unstructured.Unstructured, profile string) {
	t.Helper()
	annotations := job.GetAnnotations()
	if annotations["helm.sh/hook"] != "pre-install,pre-upgrade" || annotations["helm.sh/hook-delete-policy"] != "before-hook-creation,hook-succeeded" || annotations["helm.sh/hook-weight"] != "-10" {
		t.Fatalf("migration hook annotations = %#v", annotations)
	}
	spec := nestedHelmMap(t, job.Object, "spec")
	if helmInt(t, spec["backoffLimit"]) != 0 || helmInt(t, spec["activeDeadlineSeconds"]) != 300 || helmInt(t, spec["ttlSecondsAfterFinished"]) != 3600 {
		t.Fatalf("migration Job lifecycle = %#v", spec)
	}
	podSpec := nestedHelmMap(t, job.Object, "spec", "template", "spec")
	if value, found, _ := unstructured.NestedBool(podSpec, "automountServiceAccountToken"); !found || value {
		t.Fatal("migration Job must not mount a Kubernetes service-account token")
	}
	if _, found := podSpec["serviceAccountName"]; found {
		t.Fatalf("migration Job unexpectedly selects a service account: %#v", podSpec)
	}
	assertHelmPodSecurity(t, podSpec, false)
	container := onlyHelmContainer(t, podSpec)
	if !reflect.DeepEqual(stringSlice(t, container["args"]), []string{"hub", "migrate"}) {
		t.Fatalf("migration arguments = %#v", container["args"])
	}
	assertHelmContainerSecurity(t, container)
	assertHelmProfileResources(t, container, profile)
	environment := helmEnvironment(t, container)
	if len(environment) != 2 || environment["SITH_HUB_MIGRATION_OWNER_DATABASE_URL"] != "secret:sith-migration/owner-database-url" || environment["SITH_HUB_APPLICATION_DATABASE_ROLE"] != "sith_app" {
		t.Fatalf("migration environment = %#v", environment)
	}
}

func assertProfileOnlyChangesResources(t *testing.T, light, heavy []*unstructured.Unstructured) {
	t.Helper()
	if len(light) != len(heavy) {
		t.Fatalf("light/heavy rendered object counts = %d/%d", len(light), len(heavy))
	}
	heavyByKind := make(map[string]*unstructured.Unstructured, len(heavy))
	for _, object := range heavy {
		heavyByKind[object.GetKind()] = object
	}
	for _, lightObject := range light {
		heavyObject, found := heavyByKind[lightObject.GetKind()]
		if !found {
			t.Fatalf("heavy profile did not render %s", lightObject.GetKind())
		}
		lightCopy := lightObject.DeepCopy()
		heavyCopy := heavyObject.DeepCopy()
		removeHelmProfileResources(t, lightCopy)
		removeHelmProfileResources(t, heavyCopy)
		if !reflect.DeepEqual(lightCopy.Object, heavyCopy.Object) {
			t.Fatalf("profile changed non-resource %s manifest", lightObject.GetKind())
		}
	}
}

func removeHelmProfileResources(t *testing.T, object *unstructured.Unstructured) {
	t.Helper()
	if object.GetKind() != "Deployment" && object.GetKind() != "Job" {
		return
	}
	spec, ok := object.Object["spec"].(map[string]any)
	if !ok {
		t.Fatalf("%s spec = %#v", object.GetKind(), object.Object["spec"])
	}
	template, ok := spec["template"].(map[string]any)
	if !ok {
		t.Fatalf("%s template = %#v", object.GetKind(), spec["template"])
	}
	podSpec, ok := template["spec"].(map[string]any)
	if !ok {
		t.Fatalf("%s Pod spec = %#v", object.GetKind(), template["spec"])
	}
	containers, ok := podSpec["containers"].([]any)
	if !ok || len(containers) != 1 {
		t.Fatalf("%s containers = %#v", object.GetKind(), podSpec["containers"])
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("%s container = %#v", object.GetKind(), containers[0])
	}
	delete(container, "resources")
}

func assertHelmPodSecurity(t *testing.T, podSpec map[string]any, requireFSGroup bool) {
	t.Helper()
	security := nestedHelmMap(t, podSpec, "securityContext")
	if security["runAsNonRoot"] != true || helmInt(t, security["runAsUser"]) != 65532 || helmInt(t, security["runAsGroup"]) != 65532 {
		t.Fatalf("pod security context = %#v", security)
	}
	if requireFSGroup && helmInt(t, security["fsGroup"]) != 65532 {
		t.Fatalf("pod fsGroup = %#v", security)
	}
	if nestedHelmMap(t, security, "seccompProfile")["type"] != "RuntimeDefault" {
		t.Fatalf("pod seccomp profile = %#v", security)
	}
}

func onlyHelmContainer(t *testing.T, podSpec map[string]any) map[string]any {
	t.Helper()
	containers, found, err := unstructured.NestedSlice(podSpec, "containers")
	if err != nil || !found || len(containers) != 1 {
		t.Fatalf("containers = %#v / %v", containers, err)
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("container = %#v", containers[0])
	}
	return container
}

func assertHelmContainerSecurity(t *testing.T, container map[string]any) {
	t.Helper()
	security := nestedHelmMap(t, container, "securityContext")
	if security["privileged"] != false || security["allowPrivilegeEscalation"] != false || security["readOnlyRootFilesystem"] != true {
		t.Fatalf("container security context = %#v", security)
	}
	if !reflect.DeepEqual(stringSlice(t, nestedHelmMap(t, security, "capabilities")["drop"]), []string{"ALL"}) {
		t.Fatalf("container capabilities = %#v", security["capabilities"])
	}
}

func assertHelmProfileResources(t *testing.T, container map[string]any, profile string) {
	t.Helper()
	want, found := hubProfileResources[profile]
	if !found {
		t.Fatalf("unknown expected profile %q", profile)
	}
	resources := nestedHelmMap(t, container, "resources")
	if len(resources) != 2 || !reflect.DeepEqual(stringMap(t, nestedHelmMap(t, resources, "requests")), want.requests) || !reflect.DeepEqual(stringMap(t, nestedHelmMap(t, resources, "limits")), want.limits) {
		t.Fatalf("%s profile resources = %#v", profile, resources)
	}
}

func helmEnvironment(t *testing.T, container map[string]any) map[string]string {
	t.Helper()
	entries, found, err := unstructured.NestedSlice(container, "env")
	if err != nil || !found {
		t.Fatalf("container environment = %#v / %v", entries, err)
	}
	environment := make(map[string]string, len(entries))
	for _, entry := range entries {
		value, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("environment entry = %#v", entry)
		}
		name, _ := value["name"].(string)
		if literal, found := value["value"].(string); found {
			environment[name] = literal
			continue
		}
		secret := nestedHelmMap(t, value, "valueFrom", "secretKeyRef")
		environment[name] = "secret:" + fmt.Sprint(secret["name"]) + "/" + fmt.Sprint(secret["key"])
	}
	return environment
}

func nestedHelmMap(t *testing.T, object map[string]any, fields ...string) map[string]any {
	t.Helper()
	value, found, err := unstructured.NestedMap(object, fields...)
	if err != nil || !found {
		t.Fatalf("missing map %s: %v", strings.Join(fields, "."), err)
	}
	return value
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("string slice = %#v", value)
	}
	result := make([]string, len(items))
	for index, item := range items {
		stringValue, ok := item.(string)
		if !ok {
			t.Fatalf("string slice item = %#v", item)
		}
		result[index] = stringValue
	}
	return result
}

func stringMap(t *testing.T, values map[string]any) map[string]string {
	t.Helper()
	result := make(map[string]string, len(values))
	for key, value := range values {
		stringValue, ok := value.(string)
		if !ok {
			t.Fatalf("string map %s = %#v", key, value)
		}
		result[key] = stringValue
	}
	return result
}

func helmInt(t *testing.T, value any) int64 {
	t.Helper()
	switch number := value.(type) {
	case int64:
		return number
	case int:
		return int64(number)
	case float64:
		if float64(int64(number)) != number {
			t.Fatalf("non-integer number = %#v", value)
		}
		return int64(number)
	default:
		t.Fatalf("integer = %#v", value)
		return 0
	}
}
