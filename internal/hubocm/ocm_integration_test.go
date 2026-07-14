// SPDX-License-Identifier: Apache-2.0
//go:build e2e && ocm

package hubocm

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ArdurAI/sith/internal/hubfleet"
)

const (
	m0WorkspaceID     = "workspace-m0"
	m0ProxyNamespace  = "open-cluster-management-addon"
	m0ProxyService    = "proxy-entrypoint"
	m0ProxyRemotePort = 8090
)

// TestDirectClusterProxyM0 proves the direct Konnectivity path against the retained
// M0 lab. The test deliberately never reads an admin kubeconfig for either spoke.
func TestDirectClusterProxyM0(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	hubConfig := m0HubConfig(t)
	hubClient, err := kubernetes.NewForConfig(hubConfig)
	if err != nil {
		t.Fatal("construct M0 hub client failed")
	}
	proxyAddress := startM0ProxyPortForward(ctx, t)
	proxyTLS := m0ProxyTLS(t, ctx, hubClient)
	reader, err := NewManagedServiceAccountReader(hubClient.CoreV1())
	if err != nil {
		t.Fatal("construct scoped MSA reader failed")
	}
	adapter, err := New(Config{
		CredentialReader:  reader,
		ProxyAddress:      proxyAddress,
		ProxyTLSConfig:    proxyTLS,
		KubeAPIServerName: "kubernetes",
	})
	if err != nil {
		t.Fatal("construct direct OCM transport failed")
	}

	for _, spoke := range []hubfleet.Spoke{
		{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"},
		{ID: "spoke-b", ManagedClusterRef: "ocm/spoke-b"},
	} {
		snapshot, err := adapter.Snapshot(ctx, m0WorkspaceID, spoke)
		if err != nil {
			t.Fatal("direct OCM snapshot failed")
		}
		if err := hubfleet.ValidateSnapshot(spoke, snapshot, time.Now().UTC()); err != nil {
			t.Fatal("direct OCM snapshot did not meet the fleet contract")
		}
		if !hasInventoryFor(snapshot, "Deployment") || !hasInventoryFor(snapshot, "Pod") {
			t.Fatal("direct OCM snapshot did not contain the scoped deployment and pod inventory")
		}
	}

	assertDirectSecretsForbidden(ctx, t, adapter, reader)
	assertMSARotation(ctx, t, hubClient, reader, adapter)
}

func m0HubConfig(t *testing.T) *rest.Config {
	t.Helper()
	kubeconfig := requiredM0Env(t, "SITH_OCM_HUB_KUBECONFIG")
	contextName := requiredM0Env(t, "SITH_OCM_HUB_CONTEXT")
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: contextName},
	)
	config, err := loader.ClientConfig()
	if err != nil {
		t.Fatal("load isolated M0 hub kubeconfig failed")
	}
	return config
}

func m0ProxyTLS(t *testing.T, ctx context.Context, client kubernetes.Interface) *tls.Config {
	t.Helper()
	caSecret, err := client.CoreV1().Secrets(m0ProxyNamespace).Get(ctx, "proxy-server-ca", metav1.GetOptions{})
	if err != nil {
		t.Fatal("read mounted proxy CA fixture failed")
	}
	clientSecret, err := client.CoreV1().Secrets(m0ProxyNamespace).Get(ctx, "proxy-client", metav1.GetOptions{})
	if err != nil {
		t.Fatal("read mounted proxy client fixture failed")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caSecret.Data["ca.crt"]) {
		t.Fatal("proxy CA fixture was invalid")
	}
	certificate, err := tls.X509KeyPair(clientSecret.Data["tls.crt"], clientSecret.Data["tls.key"])
	if err != nil {
		t.Fatal("proxy client mTLS fixture was invalid")
	}
	return &tls.Config{
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
		ServerName:   "localhost",
		Certificates: []tls.Certificate{certificate},
	}
}

func assertDirectSecretsForbidden(
	ctx context.Context,
	t *testing.T,
	adapter *Adapter,
	reader CredentialReader,
) {
	t.Helper()
	credential, err := reader.Read(ctx, m0WorkspaceID, "spoke-a")
	if err != nil {
		t.Fatal("read scoped MSA credential for negative control failed")
	}
	defer clearCredential(&credential)
	config := adapter.restConfig(ctx, "spoke-a", credential)
	transport, err := rest.TransportFor(config)
	if err != nil {
		t.Fatal("construct direct negative-control transport failed")
	}
	httpClient := &http.Client{Transport: transport}
	defer httpClient.CloseIdleConnections()
	directClient, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		t.Fatal("construct direct negative-control client failed")
	}
	if _, err := directClient.CoreV1().Secrets("").List(ctx, metav1.ListOptions{Limit: 1}); !apierrors.IsForbidden(err) {
		t.Fatal("direct MSA path did not fail closed for Secrets")
	}
}

func assertMSARotation(
	ctx context.Context,
	t *testing.T,
	hubClient kubernetes.Interface,
	reader CredentialReader,
	adapter *Adapter,
) {
	t.Helper()
	before, err := reader.Read(ctx, m0WorkspaceID, "spoke-a")
	if err != nil {
		t.Fatal("read MSA credential before rotation failed")
	}
	previousToken := append([]byte(nil), before.token...)
	clearCredential(&before)
	defer clear(previousToken)
	if err := hubClient.CoreV1().Secrets("spoke-a").Delete(ctx, managedServiceAccount, metav1.DeleteOptions{}); err != nil {
		t.Fatal("request MSA projection rotation failed")
	}

	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("MSA projection rotation exceeded the direct-test deadline")
		case <-deadline.C:
			t.Fatal("MSA projection rotation did not produce a new token")
		case <-ticker.C:
			next, err := reader.Read(ctx, m0WorkspaceID, "spoke-a")
			if err != nil {
				continue
			}
			rotated := subtle.ConstantTimeCompare(previousToken, next.token) != 1
			clearCredential(&next)
			if !rotated {
				continue
			}
			snapshot, err := adapter.Snapshot(ctx, m0WorkspaceID, hubfleet.Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"})
			if err != nil || len(snapshot.Facts) == 0 {
				t.Fatal("direct transport did not use the rotated MSA credential")
			}
			return
		}
	}
}

func startM0ProxyPortForward(ctx context.Context, t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve loopback port for direct M0 test failed")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal("release reserved loopback port failed")
	}
	kubectl, err := exec.LookPath(requiredM0Env(t, "KUBECTL_BIN"))
	if err != nil {
		t.Fatal("M0 kubectl binary was unavailable")
	}
	command := exec.CommandContext(ctx, kubectl,
		"--kubeconfig", requiredM0Env(t, "SITH_OCM_HUB_KUBECONFIG"),
		"--context", requiredM0Env(t, "SITH_OCM_HUB_CONTEXT"),
		"-n", m0ProxyNamespace,
		"port-forward", "--address", "127.0.0.1", "service/"+m0ProxyService,
		strconv.Itoa(port)+":"+strconv.Itoa(m0ProxyRemotePort),
	)
	command.Stdout = nil
	command.Stderr = nil
	if err := command.Start(); err != nil {
		t.Fatal("start direct M0 proxy port-forward failed")
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	})
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return address
		}
		select {
		case <-ctx.Done():
			t.Fatal("direct M0 proxy port-forward did not become reachable")
		case <-deadline.C:
			t.Fatal("direct M0 proxy port-forward did not become reachable")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func hasInventoryFor(snapshot hubfleet.Snapshot, kind string) bool {
	for _, fact := range snapshot.Facts {
		if fact.Kind == "inventory" && fact.Ref.Kind == kind {
			return true
		}
	}
	return false
}

func requiredM0Env(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("required M0 test environment %s is unset", name)
	}
	return value
}
