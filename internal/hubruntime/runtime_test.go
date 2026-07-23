// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/hubserver"
)

type runtimeProbeChecker func(context.Context) error

func (checker runtimeProbeChecker) Ping(ctx context.Context) error { return checker(ctx) }

func TestServerServesTLSAndStopsWithContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS := runtimeTestTLS(t)
	server, err := NewServer(ServerConfig{
		Listener: listener,
		Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.TLS == nil {
				t.Fatal("plaintext request reached hub handler")
			}
			response.WriteHeader(http.StatusNoContent)
		}),
		TLSConfig: serverTLS,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	endpoint := "https://" + listener.Addr().String()
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, requestErr := client.Get(endpoint)
		if requestErr == nil {
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d", response.StatusCode)
			}
			_ = response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS request failed: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNewServerRejectsUnsafeConfiguration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverTLS, _ := runtimeTestTLS(t)
	for _, test := range []struct {
		name   string
		mutate func(*ServerConfig)
	}{
		{name: "missing handler", mutate: func(config *ServerConfig) { config.Handler = nil }},
		{name: "missing listener", mutate: func(config *ServerConfig) { config.Listener = nil }},
		{name: "weak TLS", mutate: func(config *ServerConfig) { config.TLSConfig.MinVersion = tls.VersionTLS11 }},
		{name: "dynamic certificate", mutate: func(config *ServerConfig) {
			config.TLSConfig.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }
		}},
		{name: "bad timeout", mutate: func(config *ServerConfig) { config.ShutdownTimeout = 500 * time.Millisecond }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ServerConfig{Listener: listener, Handler: http.NotFoundHandler(), TLSConfig: serverTLS.Clone()}
			test.mutate(&config)
			if _, err := NewServer(config); err == nil {
				t.Fatal("NewServer accepted unsafe configuration")
			}
		})
	}
}

func TestRuntimeMuxMountsProbesOutsideAuthenticatedFleetFallback(t *testing.T) {
	fallbackCalls := 0
	fallback := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fallbackCalls++
		response.WriteHeader(http.StatusUnauthorized)
	})
	auditCalls := 0
	audit := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		auditCalls++
		response.WriteHeader(http.StatusTeapot)
	})
	probes, err := hubserver.NewProbeHandler(hubserver.ProbeHandlerConfig{
		Checker: runtimeProbeChecker(func(context.Context) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux, err := newRuntimeMux(fallback, audit, probes)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		method string
		target string
		status int
		calls  int
	}{
		{method: http.MethodGet, target: hubserver.LivenessPath, status: http.StatusNoContent},
		{method: http.MethodGet, target: hubserver.ReadinessPath, status: http.StatusNoContent},
		{method: http.MethodHead, target: hubserver.LivenessPath, status: http.StatusNotFound},
		{method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export", status: http.StatusTeapot},
		{method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export/pages", status: http.StatusTeapot},
		{method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet", status: http.StatusUnauthorized, calls: 1},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))
		if response.Code != test.status || fallbackCalls != test.calls {
			t.Fatalf("%s %s = status %d/fallback calls %d/audit calls %d, want %d/%d", test.method, test.target, response.Code, fallbackCalls, auditCalls, test.status, test.calls)
		}
	}

	if auditCalls != 2 {
		t.Fatalf("audit route calls = %d, want 2", auditCalls)
	}
	if _, err := newRuntimeMux(nil, audit, probes); err == nil {
		t.Fatal("newRuntimeMux accepted a missing fleet handler")
	}
	if _, err := newRuntimeMux(fallback, nil, probes); err == nil {
		t.Fatal("newRuntimeMux accepted a missing audit export handler")
	}
	if _, err := newRuntimeMux(fallback, audit, nil); err == nil {
		t.Fatal("newRuntimeMux accepted a missing probe handler")
	}
}

func TestLoadDeploymentConfigRequiresEverySecurityInput(t *testing.T) {
	config, err := loadDeploymentConfig(func(string) (string, bool) { return "", false })
	if err == nil || config != (deploymentConfig{}) {
		t.Fatalf("config/error = %#v/%v", config, err)
	}
	values := deploymentConfigEnvironment()
	config, err = loadDeploymentConfig(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	if err != nil || config.listenAddress != "127.0.0.1:8443" || config.proxyAddress != "proxy.sith.test:8090" {
		t.Fatalf("config/error = %#v/%v", config, err)
	}
	values["SITH_HUB_LISTEN_ADDR"] = ":8443"
	if _, err := loadDeploymentConfig(func(name string) (string, bool) { value, ok := values[name]; return value, ok }); err == nil {
		t.Fatal("loadDeploymentConfig accepted an ambiguous listener")
	}
}

func TestLoadDeploymentConfigRequiresCompleteBrowserOIDCInputs(t *testing.T) {
	values := deploymentConfigEnvironment()
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	config, err := loadDeploymentConfig(lookup)
	if err != nil || config.browserOIDC != nil {
		t.Fatalf("default browser OIDC config/error = %#v/%v", config.browserOIDC, err)
	}
	values["SITH_HUB_BROWSER_OIDC_ISSUER"] = "https://idp.sith.test"
	if _, err := loadDeploymentConfig(lookup); err == nil {
		t.Fatal("partial browser OIDC configuration accepted")
	}
	values["SITH_HUB_BROWSER_OIDC_CLIENT_ID"] = "sith-browser"
	values["SITH_HUB_BROWSER_OIDC_REDIRECT_URI"] = "https://hub.sith.test/v1/console/oidc/callback"
	values["SITH_HUB_SESSION_PRIVATE_KEY_FILE"] = "/mnt/session/private.pem"
	config, err = loadDeploymentConfig(lookup)
	if err != nil || config.browserOIDC == nil || config.browserOIDC.clientID != "sith-browser" || config.browserOIDC.sessionPrivateKeyFile != "/mnt/session/private.pem" {
		t.Fatalf("browser OIDC config/error = %#v/%v", config.browserOIDC, err)
	}
}

func TestLoadDeploymentConfigAllowsOnlyExactLoopbackMetricsAddresses(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "disabled", value: "", valid: true},
		{name: "IPv4 loopback", value: "127.0.0.1:9464", valid: true},
		{name: "IPv6 loopback", value: "[::1]:9464", valid: true},
		{name: "hostname", value: "localhost:9464"},
		{name: "IPv4 wildcard", value: "0.0.0.0:9464"},
		{name: "IPv6 wildcard", value: "[::]:9464"},
		{name: "alternate loopback", value: "127.0.0.2:9464"},
		{name: "missing host", value: ":9464"},
		{name: "missing port", value: "127.0.0.1"},
		{name: "zero port", value: "127.0.0.1:0"},
		{name: "out of range port", value: "127.0.0.1:65536"},
		{name: "whitespace padded", value: " 127.0.0.1:9464"},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := deploymentConfigEnvironment()
			if test.value != "" {
				values["SITH_HUB_METRICS_LISTEN_ADDR"] = test.value
			}
			config, err := loadDeploymentConfig(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
			if test.valid {
				if err != nil || config.metricsListenAddress != test.value {
					t.Fatalf("config/error = %#v/%v", config, err)
				}
				return
			}
			if err == nil {
				t.Fatal("loadDeploymentConfig accepted unsafe loopback metrics listener")
			}
		})
	}
	values := deploymentConfigEnvironment()
	values["SITH_HUB_METRICS_LISTEN_ADDR"] = ""
	config, err := loadDeploymentConfig(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	if err != nil || config.metricsListenAddress != "" {
		t.Fatalf("explicitly empty metrics configuration/error = %#v/%v", config, err)
	}
}

func deploymentConfigEnvironment() map[string]string {
	return map[string]string{
		"SITH_HUB_LISTEN_ADDR":             "127.0.0.1:8443",
		"SITH_HUB_DATABASE_URL":            "postgres://sith@db/sith?sslmode=require",
		"SITH_HUB_SESSION_ISSUER":          "https://issuer.sith.test",
		"SITH_HUB_SESSION_AUDIENCE":        "https://hub.sith.test",
		"SITH_HUB_SESSION_KEY_ID":          "session-2026-07",
		"SITH_HUB_SESSION_PUBLIC_KEY_FILE": "/mnt/session/public.pem",
		"SITH_HUB_SERVER_TLS_CERT_FILE":    "/mnt/server/tls.crt",
		"SITH_HUB_SERVER_TLS_KEY_FILE":     "/mnt/server/tls.key",
		"SITH_HUB_PROXY_ADDRESS":           "proxy.sith.test:8090",
		"SITH_HUB_PROXY_SERVER_NAME":       "proxy.sith.test",
		"SITH_HUB_PROXY_CA_FILE":           "/mnt/proxy/ca.crt",
		"SITH_HUB_PROXY_CERT_FILE":         "/mnt/proxy/tls.crt",
		"SITH_HUB_PROXY_KEY_FILE":          "/mnt/proxy/tls.key",
		"SITH_HUB_KUBE_API_SERVER_NAME":    "kubernetes",
	}
}

func TestReadMountedFileRequiresReadOnlyRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mounted.pem")
	if err := os.WriteFile(path, []byte("mounted material"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMountedFile("test material", path, 1024); err == nil {
		t.Fatal("readMountedFile accepted a writable file")
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	contents, err := readMountedFile("test material", path, 1024)
	if err != nil || string(contents) != "mounted material" {
		t.Fatalf("contents/error = %q/%v", contents, err)
	}
	clear(contents)
	if _, err := readMountedFile("test directory", filepath.Dir(path), 1024); err == nil {
		t.Fatal("readMountedFile accepted a directory")
	}
}

func TestLoadSessionPrivateKeyRequiresReadOnlyPKCS8Ed25519(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session-private.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o400); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSessionPrivateKey(path)
	if err != nil || string(loaded) != string(privateKey) {
		t.Fatalf("private key/error = %x/%v", loaded, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSessionPrivateKey(path); err == nil {
		t.Fatal("non-PKCS8 private key accepted")
	}
}

func TestCopyAndClearSessionPrivateKeyTransfersOwnership(t *testing.T) {
	publicKey, parsedKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	expected := append(ed25519.PrivateKey(nil), parsedKey...)
	returned := copyAndClearSessionPrivateKey(parsedKey)
	t.Cleanup(func() {
		clear(expected)
		clear(returned)
	})

	if &parsedKey[0] == &returned[0] {
		t.Fatal("returned private key aliases the parser-owned key")
	}
	if !bytes.Equal(parsedKey, make([]byte, ed25519.PrivateKeySize)) {
		t.Fatal("parser-owned private key was not cleared")
	}
	if !bytes.Equal(returned, expected) {
		t.Fatal("returned private key changed during ownership transfer")
	}
	message := []byte("sith session key ownership transfer")
	if !ed25519.Verify(publicKey, message, ed25519.Sign(returned, message)) {
		t.Fatal("returned private key cannot produce a valid signature")
	}
}

func runtimeTestTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(&x509.Certificate{Raw: certificateDER})
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
}
