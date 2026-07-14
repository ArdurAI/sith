// SPDX-License-Identifier: Apache-2.0

package hubocm

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"

	konnectivity "sigs.k8s.io/apiserver-network-proxy/konnectivity-client/pkg/client"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestManagedServiceAccountReaderGetsOnlyPinnedSecret(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: managedServiceAccount, Namespace: "spoke-a"},
		Data:       map[string][]byte{"token": []byte("scoped-token"), "ca.crt": []byte("scoped-ca")},
	})
	var actions []ktesting.Action
	client.PrependReactor("get", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		actions = append(actions, action)
		return false, nil, nil
	})
	reader, err := NewManagedServiceAccountReader(client.CoreV1())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := reader.Read(context.Background(), "workspace-a", "spoke-a")
	if err != nil {
		t.Fatal(err)
	}
	if string(credential.token) != "scoped-token" || string(credential.ca) != "scoped-ca" {
		t.Fatal("projected credential did not contain the expected fixed material")
	}
	if len(actions) != 1 || actions[0].GetVerb() != "get" || actions[0].GetResource().Resource != "secrets" ||
		actions[0].GetNamespace() != "spoke-a" || actions[0].(ktesting.GetAction).GetName() != managedServiceAccount {
		t.Fatalf("Secret actions = %#v, want one exact get", actions)
	}
}

func TestManagedServiceAccountReaderRejectsUnsafeProjection(t *testing.T) {
	t.Parallel()

	for _, secret := range []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: managedServiceAccount, Namespace: "spoke-a"}, Data: map[string][]byte{"token": []byte("x")}},
		{ObjectMeta: metav1.ObjectMeta{Name: managedServiceAccount, Namespace: "spoke-a"}, Data: map[string][]byte{"token": []byte("x"), "ca.crt": []byte("ca"), "kubeconfig": []byte("forbidden")}},
		{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "spoke-a"}, Data: map[string][]byte{"token": []byte("x"), "ca.crt": []byte("ca")}},
	} {
		if _, err := credentialFromSecret(secret); err == nil {
			t.Fatal("credentialFromSecret unexpectedly accepted an unsafe Secret")
		}
	}
}

func TestNewRejectsUnsafeProxyConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing reader", mutate: func(config *Config) { config.CredentialReader = nil }},
		{name: "unqualified address", mutate: func(config *Config) { config.ProxyAddress = "proxy.example" }},
		{name: "insecure TLS", mutate: func(config *Config) { config.ProxyTLSConfig.InsecureSkipVerify = true }},
		{name: "missing CA pin", mutate: func(config *Config) { config.ProxyTLSConfig.RootCAs = nil }},
		{name: "weak TLS minimum", mutate: func(config *Config) { config.ProxyTLSConfig.MinVersion = tls.VersionTLS11 }},
		{name: "unconfigured Kubernetes name", mutate: func(config *Config) { config.KubeAPIServerName = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(credentialReaderFunc(func(context.Context, tenancy.WorkspaceID, string) (projectedCredential, error) {
				return projectedCredential{}, nil
			}))
			test.mutate(&config)
			if _, err := New(config); err == nil {
				t.Fatal("New() unexpectedly accepted unsafe configuration")
			}
		})
	}
}

func TestSnapshotPinsMSACredentialTLSAndNormalizedFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 13, 18, 0, 0, 0, time.UTC)
	reader := &rotatingCredentialReader{}
	deployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: "apps", Generation: 4},
		Spec:       appsv1.DeploymentSpec{Replicas: pointer[int32](2)},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 4, AvailableReplicas: 2},
	}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "apps", Generation: 5},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{ImageID: "containerd://sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	client := &fakeSnapshotClient{
		deployments: []*appsv1.DeploymentList{{Items: []appsv1.Deployment{deployment}}},
		pods:        []*corev1.PodList{{Items: []corev1.Pod{pod}}},
		rollouts:    []*unstructured.UnstructuredList{{}},
	}
	adapter := testAdapter(t, reader, client, now)

	snapshot, err := adapter.Snapshot(context.Background(), "workspace-a", hubfleet.Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := hubfleet.ValidateSnapshot(hubfleet.Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}, snapshot, now); err != nil {
		t.Fatalf("ValidateSnapshot() error = %v", err)
	}
	if len(snapshot.Facts) != 4 || client.closed != 1 {
		t.Fatalf("snapshot facts/close = %d/%d, want 4/1", len(snapshot.Facts), client.closed)
	}
	if len(client.configs) != 1 {
		t.Fatalf("client configs = %d, want 1", len(client.configs))
	}
	config := client.configs[0]
	if config.Host != "https://spoke-a" || config.BearerToken != "rotated-token-1" || config.Insecure ||
		config.ServerName != "kubernetes" || string(config.CAData) != "rotated-ca-1" {
		t.Fatal("rest config was not the pinned projected-credential path")
	}
	for _, fact := range snapshot.Facts {
		if fact.Ref.SourceKind != hubfleet.SourceKind || fact.Ref.Scope != "spoke-a" || fact.Source != "spoke-a" ||
			fact.Provenance.NativeID != "" || strings.Contains(string(fact.Observed), "token") || strings.Contains(string(fact.Observed), "endpoint") {
			t.Fatalf("unsafe normalized fact: %#v", fact)
		}
	}
	for _, fact := range snapshot.Facts {
		if fact.Kind == fleet.FactInventory && fact.Ref.Kind == "Pod" && !strings.Contains(string(fact.Observed), "\"image_digests\":[\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"]") {
			t.Fatalf("pod inventory did not retain the canonical runtime digest: %s", fact.Observed)
		}
	}
}

func TestPodImageDigestsAbstainsFromMutableOrNonWorkloadStatus(t *testing.T) {
	t.Parallel()

	digest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pod := corev1.Pod{Status: corev1.PodStatus{
		ContainerStatuses:     []corev1.ContainerStatus{{ImageID: "docker-pullable://registry.example/api@" + digest}, {ImageID: "registry.example/api:latest"}, {ImageID: "docker-pullable://registry.example/api@" + digest}},
		InitContainerStatuses: []corev1.ContainerStatus{{ImageID: "containerd://sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
		EphemeralContainerStatuses: []corev1.ContainerStatus{{
			ImageID: "containerd://sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		}},
	}}
	if got := podImageDigests(pod); !slices.Equal(got, []string{digest}) {
		t.Fatalf("podImageDigests() = %#v, want one ordinary-container digest", got)
	}
}

func TestSnapshotReadsRotatedCredentialForEachCall(t *testing.T) {
	t.Parallel()

	reader := &rotatingCredentialReader{}
	client := &fakeSnapshotClient{deployments: []*appsv1.DeploymentList{{}, {}}, pods: []*corev1.PodList{{}, {}}, rollouts: []*unstructured.UnstructuredList{{}, {}}}
	adapter := testAdapter(t, reader, client, time.Date(2026, time.July, 13, 18, 0, 0, 0, time.UTC))
	spoke := hubfleet.Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}
	for range 2 {
		if _, err := adapter.Snapshot(context.Background(), "workspace-a", spoke); err != nil {
			t.Fatal(err)
		}
	}
	if reader.calls != 2 || len(client.configs) != 2 || client.configs[0].BearerToken != "rotated-token-1" || client.configs[1].BearerToken != "rotated-token-2" {
		t.Fatalf("credential rotation calls/configs = %d/%#v", reader.calls, client.configs)
	}
}

func TestSnapshotClearsAdapterOwnedRestConfigCredentialBuffers(t *testing.T) {
	t.Parallel()

	reader := credentialReaderFunc(func(context.Context, tenancy.WorkspaceID, string) (projectedCredential, error) {
		return projectedCredential{token: []byte("test-token"), ca: []byte("test-ca")}, nil
	})
	client := &fakeSnapshotClient{}
	adapter := testAdapter(t, reader, client, time.Now().UTC())
	var constructed *rest.Config
	adapter.clients = func(config *rest.Config) (snapshotClient, error) {
		constructed = config
		return client, nil
	}
	if _, err := adapter.Snapshot(context.Background(), "workspace-a", hubfleet.Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}); err != nil {
		t.Fatal(err)
	}
	if constructed == nil || constructed.BearerToken != "" || string(constructed.CAData) != "\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatal("snapshot retained adapter-owned rest-config credential material")
	}
}

func TestSnapshotDoesNotExposeDependencyCredentialDetails(t *testing.T) {
	t.Parallel()

	secret := "eyJnot-a-real-token"
	adapter := testAdapter(t, credentialReaderFunc(func(context.Context, tenancy.WorkspaceID, string) (projectedCredential, error) {
		return projectedCredential{}, errors.New(secret)
	}), &fakeSnapshotClient{}, time.Now().UTC())
	_, err := adapter.Snapshot(context.Background(), "workspace-a", hubfleet.Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("snapshot error leaked a dependency credential detail")
	}
}

func TestDialRejectsUnpinnedTargetsAndClosesTunnel(t *testing.T) {
	t.Parallel()

	factory := &fakeTunnelFactory{}
	adapter := testAdapter(t, credentialReaderFunc(func(context.Context, tenancy.WorkspaceID, string) (projectedCredential, error) {
		return projectedCredential{}, nil
	}), &fakeSnapshotClient{}, time.Now().UTC())
	adapter.tunnels = factory
	dial := adapter.dialContext(context.Background(), "spoke-a:443")
	if _, err := dial(context.Background(), "tcp", "other:443"); err == nil || factory.opens != 0 {
		t.Fatal("unpinned direct dial was accepted")
	}
	connection, err := dial(context.Background(), "tcp", "spoke-a:443")
	if err != nil {
		t.Fatal(err)
	}
	if factory.opens != 1 || factory.tunnel.target != "spoke-a:443" {
		t.Fatalf("tunnel open/dial = %d/%q", factory.opens, factory.tunnel.target)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-factory.tunnel.canceled:
	case <-time.After(time.Second):
		t.Fatal("closing direct connection did not close its single-use tunnel")
	}
}

func TestGRPCTunnelFactoryRejectsCanceledCreationContext(t *testing.T) {
	t.Parallel()

	proxyTLS := testConfig(nil).ProxyTLSConfig
	creationContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (grpcTunnelFactory{address: "127.0.0.1:1", tls: proxyTLS}).Open(creationContext, context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Open() error = %v, want context cancellation", err)
	}
}

func TestParseManagedClusterRefRejectsEndpointInjection(t *testing.T) {
	t.Parallel()

	for _, reference := range []string{"", "spoke-a", "ocm/https://spoke-a", "ocm/spoke-a/other", "ocm/Spoke-A"} {
		if _, err := parseManagedClusterRef(reference); err == nil {
			t.Fatalf("parseManagedClusterRef(%q) unexpectedly succeeded", reference)
		}
	}
}

func TestSnapshotFailsBeforeAnUnboundedFollowupList(t *testing.T) {
	t.Parallel()

	deployments := make([]appsv1.Deployment, maxResources)
	for index := range deployments {
		deployments[index] = appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name:      "deployment-" + strconv.Itoa(index),
			Namespace: "apps",
		}}
	}
	client := &fakeSnapshotClient{deployments: []*appsv1.DeploymentList{{Items: deployments}}}
	_, err := collectFacts(context.Background(), client, hubfleet.Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}, time.Now().UTC())
	if err == nil || client.podCalls != 0 || client.rolloutCalls != 0 {
		t.Fatal("snapshot did not fail before an unbounded follow-up resource list")
	}
}

type credentialReaderFunc func(context.Context, tenancy.WorkspaceID, string) (projectedCredential, error)

func (function credentialReaderFunc) Read(ctx context.Context, workspaceID tenancy.WorkspaceID, cluster string) (projectedCredential, error) {
	return function(ctx, workspaceID, cluster)
}

type rotatingCredentialReader struct{ calls int }

func (reader *rotatingCredentialReader) Read(_ context.Context, _ tenancy.WorkspaceID, _ string) (projectedCredential, error) {
	reader.calls++
	return projectedCredential{token: []byte("rotated-token-" + strconv.Itoa(reader.calls)), ca: []byte("rotated-ca-" + strconv.Itoa(reader.calls))}, nil
}

type fakeSnapshotClient struct {
	deployments  []*appsv1.DeploymentList
	pods         []*corev1.PodList
	rollouts     []*unstructured.UnstructuredList
	configs      []*rest.Config
	closed       int
	podCalls     int
	rolloutCalls int
}

func (client *fakeSnapshotClient) ListDeployments(_ context.Context, _ metav1.ListOptions) (*appsv1.DeploymentList, error) {
	if len(client.deployments) == 0 {
		return &appsv1.DeploymentList{}, nil
	}
	page := client.deployments[0]
	client.deployments = client.deployments[1:]
	return page, nil
}

func (client *fakeSnapshotClient) ListPods(_ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
	client.podCalls++
	if len(client.pods) == 0 {
		return &corev1.PodList{}, nil
	}
	page := client.pods[0]
	client.pods = client.pods[1:]
	return page, nil
}

func (client *fakeSnapshotClient) ListRollouts(_ context.Context, _ metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	client.rolloutCalls++
	if len(client.rollouts) == 0 {
		return &unstructured.UnstructuredList{}, nil
	}
	page := client.rollouts[0]
	client.rollouts = client.rollouts[1:]
	return page, nil
}

func (client *fakeSnapshotClient) Close() { client.closed++ }

type fakeTunnelFactory struct {
	opens  int
	tunnel *fakeTunnel
}

func (factory *fakeTunnelFactory) Open(_ context.Context, tunnelCtx context.Context) (konnectivity.Tunnel, error) {
	factory.opens++
	factory.tunnel = &fakeTunnel{canceled: tunnelCtx.Done()}
	return factory.tunnel, nil
}

type fakeTunnel struct {
	target   string
	canceled <-chan struct{}
}

func (tunnel *fakeTunnel) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	tunnel.target = address
	client, server := net.Pipe()
	go server.Close()
	return client, nil
}

func (tunnel *fakeTunnel) Done() <-chan struct{} { return tunnel.canceled }

func testConfig(reader CredentialReader) Config {
	return Config{
		CredentialReader: reader,
		ProxyAddress:     "proxy.example:8090",
		ProxyTLSConfig: &tls.Config{
			RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12, ServerName: "proxy.example",
			Certificates: []tls.Certificate{{Certificate: [][]byte{{1}}, PrivateKey: &rsa.PrivateKey{}}},
		},
		KubeAPIServerName: "kubernetes",
	}
}

func testAdapter(t *testing.T, reader CredentialReader, client *fakeSnapshotClient, now time.Time) *Adapter {
	t.Helper()
	config := testConfig(reader)
	config.Now = func() time.Time { return now }
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter.clients = func(config *rest.Config) (snapshotClient, error) {
		captured := rest.CopyConfig(config)
		captured.CAData = append([]byte(nil), config.CAData...)
		client.configs = append(client.configs, captured)
		return client, nil
	}
	return adapter
}

func pointer[T any](value T) *T { return &value }
