// SPDX-License-Identifier: Apache-2.0

// Package ocmlab provides the retained disposable M0 lab connection fixture for integration tests.
package ocmlab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// ProxyNamespace is the fixed OCM ClusterProxy namespace created by the M0 harness.
	ProxyNamespace = "open-cluster-management-addon"
	// ProxyService is the fixed ClusterProxy service exposed by the M0 harness.
	ProxyService = "proxy-entrypoint"
	// ProxyRemotePort is the fixed ClusterProxy service port used by the M0 harness.
	ProxyRemotePort = 8090
)

// HubConfig loads the isolated M0 hub kubeconfig supplied by the falsification harness.
func HubConfig(t testing.TB) *rest.Config {
	t.Helper()
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: RequiredEnv(t, "SITH_OCM_HUB_KUBECONFIG")},
		&clientcmd.ConfigOverrides{CurrentContext: RequiredEnv(t, "SITH_OCM_HUB_CONTEXT")},
	)
	config, err := loader.ClientConfig()
	if err != nil {
		t.Fatal("load isolated M0 hub kubeconfig failed")
	}
	return config
}

// ProxyTLS reads the lab's proxy credentials, mirroring the mounted material used by production.
func ProxyTLS(ctx context.Context, t testing.TB, client kubernetes.Interface) *tls.Config {
	t.Helper()
	caSecret, err := client.CoreV1().Secrets(ProxyNamespace).Get(ctx, "proxy-server-ca", metav1.GetOptions{})
	if err != nil {
		t.Fatal("read M0 proxy CA fixture failed")
	}
	clientSecret, err := client.CoreV1().Secrets(ProxyNamespace).Get(ctx, "proxy-client", metav1.GetOptions{})
	if err != nil {
		t.Fatal("read M0 proxy client fixture failed")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caSecret.Data["ca.crt"]) {
		t.Fatal("M0 proxy CA fixture was invalid")
	}
	certificate, err := tls.X509KeyPair(clientSecret.Data["tls.crt"], clientSecret.Data["tls.key"])
	if err != nil {
		t.Fatal("M0 proxy client fixture was invalid")
	}
	return &tls.Config{
		RootCAs: pool, MinVersion: tls.VersionTLS12, ServerName: "localhost", Certificates: []tls.Certificate{certificate},
	}
}

// StartProxyPortForward opens one loopback-only path to the M0 ClusterProxy service.
func StartProxyPortForward(ctx context.Context, t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve loopback port for M0 proxy test failed")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal("release reserved loopback port failed")
	}
	kubectl, err := exec.LookPath(RequiredEnv(t, "KUBECTL_BIN"))
	if err != nil {
		t.Fatal("M0 kubectl binary was unavailable")
	}
	// #nosec G204 -- kubectl is resolved from the harness path and all arguments are fixed M0 fixture inputs.
	command := exec.CommandContext(ctx, kubectl,
		"--kubeconfig", RequiredEnv(t, "SITH_OCM_HUB_KUBECONFIG"),
		"--context", RequiredEnv(t, "SITH_OCM_HUB_CONTEXT"),
		"-n", ProxyNamespace,
		"port-forward", "--address", "127.0.0.1", "service/"+ProxyService,
		strconv.Itoa(port)+":"+strconv.Itoa(ProxyRemotePort),
	)
	command.Stdout = nil
	command.Stderr = nil
	if err := command.Start(); err != nil {
		t.Fatal("start M0 proxy port-forward failed")
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
			t.Fatal("M0 proxy port-forward did not become reachable")
		case <-deadline.C:
			t.Fatal("M0 proxy port-forward did not become reachable")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// RequiredEnv returns one mandatory harness input without echoing its value.
func RequiredEnv(t testing.TB, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("required M0 test environment %s is unset", name)
	}
	return value
}
