// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/hubdb"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/hubocm"
	"github.com/ArdurAI/sith/internal/hubserver"
	"github.com/ArdurAI/sith/internal/observability"
	"github.com/ArdurAI/sith/internal/pep"
)

const (
	maxMountedCertificateBytes = 256 * 1024
	maxMountedKeyBytes         = 64 * 1024
	maxMountedCABundleBytes    = 256 * 1024
	maxMountedPublicKeyBytes   = 16 * 1024
	maxMountedPrivateKeyBytes  = 16 * 1024
)

// Runtime owns the configured application database while the hub server runs.
type Runtime struct {
	server *Server
	close  func()
}

// NewFromEnvironment constructs the production hub only from complete, deployment-mounted
// configuration and in-cluster Kubernetes identity. It never loads a kubeconfig or persists a
// token, certificate, or database credential.
func NewFromEnvironment(ctx context.Context, logger *slog.Logger) (*Runtime, error) {
	if ctx == nil || logger == nil {
		return nil, fmt.Errorf("construct hub runtime: context and logger are required")
	}
	config, err := loadDeploymentConfig(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	serverTLS, err := loadServerTLS(config)
	if err != nil {
		return nil, err
	}
	proxyTLS, err := loadProxyTLS(config)
	if err != nil {
		return nil, err
	}
	publicKey, err := loadSessionPublicKey(config.sessionPublicKeyFile)
	if err != nil {
		return nil, err
	}
	verifier, err := hubauth.NewJWTVerifier(hubauth.JWTConfig{
		Issuer: config.sessionIssuer, Audience: config.sessionAudience, Keys: map[string]ed25519.PublicKey{config.sessionKeyID: publicKey},
	})
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: session verification configuration is invalid")
	}
	auditor, err := pep.NewSlogAuditor(logger)
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: policy audit configuration is invalid")
	}
	tracer, err := observability.NewSlogTraceObserver(logger)
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: trace configuration is invalid")
	}
	authObserver, err := observability.NewSlogAuthObserver(logger)
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: authentication observability configuration is invalid")
	}
	enforcer, err := pep.NewEnforcer(pep.Config{Hook: pep.AllowReadHook{}, Auditor: auditor, TraceObserver: tracer})
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: policy configuration is invalid")
	}

	inClusterConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: in-cluster Kubernetes identity is required")
	}
	kubeClient, err := kubernetes.NewForConfig(inClusterConfig)
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: Kubernetes client is unavailable")
	}
	credentialReader, err := hubocm.NewManagedServiceAccountReader(kubeClient.CoreV1())
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: scoped credential reader is unavailable")
	}
	transport, err := hubocm.New(hubocm.Config{
		CredentialReader:  credentialReader,
		ProxyAddress:      config.proxyAddress,
		ProxyTLSConfig:    proxyTLS,
		KubeAPIServerName: config.kubeAPIServerName,
	})
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: direct OCM transport configuration is invalid")
	}
	database, err := hubdb.OpenAppDB(ctx, hubdb.AppConfig{URL: config.databaseURL})
	if err != nil {
		return nil, fmt.Errorf("construct hub runtime: database is unavailable")
	}
	cleanup := database.Close
	collector, err := hubfleet.NewCollector(hubfleet.CollectorConfig{Store: database, Transport: transport, PEP: enforcer, TraceObserver: tracer})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("construct hub runtime: collector configuration is invalid")
	}
	imageSearcher, err := hubfleet.NewImageSearcher(hubfleet.ImageSearcherConfig{Querier: database, PEP: enforcer})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("construct hub runtime: image search configuration is invalid")
	}
	cveSearcher, err := hubfleet.NewCVESearcher(hubfleet.CVESearcherConfig{Querier: database, PEP: enforcer})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("construct hub runtime: CVE search configuration is invalid")
	}
	fleetHandler, err := hubserver.NewFleetHandler(hubserver.FleetHandlerConfig{
		Verifier: verifier, AuthObserver: authObserver, Collector: collector, Reader: database, ImageSearcher: imageSearcher, CVESearcher: cveSearcher, CVEIdentifierSearcher: cveSearcher, PEP: enforcer,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("construct hub runtime: HTTP handler configuration is invalid")
	}
	handler := fleetHandler
	if config.browserOIDC != nil {
		privateKey, err := loadSessionPrivateKey(config.browserOIDC.sessionPrivateKeyFile)
		if err != nil {
			cleanup()
			return nil, err
		}
		defer clear(privateKey)
		if !publicKey.Equal(privateKey.Public()) {
			cleanup()
			return nil, fmt.Errorf("construct hub runtime: session signing key does not match the configured public key")
		}
		sessionIssuer, err := hubauth.NewSessionIssuer(hubauth.SessionIssuerConfig{
			Issuer: config.sessionIssuer, Audience: config.sessionAudience, KeyID: config.sessionKeyID, PrivateKey: privateKey,
		})
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("construct hub runtime: browser session issuer configuration is invalid")
		}
		oidcService, err := hubauth.NewOIDCService(hubauth.OIDCServiceConfig{
			Providers: []hubauth.OIDCProviderConfig{{Issuer: config.browserOIDC.issuer, Audience: config.browserOIDC.clientID}},
			Store:     database, Issuer: sessionIssuer,
		})
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("construct hub runtime: browser OIDC configuration is invalid")
		}
		limiter, err := hubserver.NewAttemptLimiter(hubserver.AttemptLimiterConfig{Attempts: 20, Window: time.Minute, MaxKeys: 4096})
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("construct hub runtime: browser OIDC rate limiting is invalid")
		}
		browserHandler, err := hubserver.NewBrowserOIDCHandler(hubserver.BrowserOIDCHandlerConfig{
			Service: oidcService, ProviderIssuer: config.browserOIDC.issuer, ClientID: config.browserOIDC.clientID,
			RedirectURI: config.browserOIDC.redirectURI, Limiter: limiter,
		})
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("construct hub runtime: browser OIDC handler configuration is invalid")
		}
		mux := http.NewServeMux()
		mux.Handle("GET /v1/workspaces/{workspace}/console/login", http.HandlerFunc(browserHandler.Login))
		mux.Handle("GET "+browserHandler.CallbackPath(), http.HandlerFunc(browserHandler.Callback))
		mux.Handle("/", fleetHandler)
		handler = mux
	}
	listener, err := net.Listen("tcp", config.listenAddress)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("construct hub runtime: listener is unavailable")
	}
	server, err := NewServer(ServerConfig{Listener: listener, Handler: handler, TLSConfig: serverTLS})
	if err != nil {
		_ = listener.Close()
		cleanup()
		return nil, err
	}
	return &Runtime{server: server, close: cleanup}, nil
}

// Run serves the configured hub and releases its application database pool on exit.
func (runtime *Runtime) Run(ctx context.Context) error {
	if runtime == nil || runtime.server == nil || runtime.close == nil {
		return fmt.Errorf("run hub runtime: runtime is required")
	}
	defer runtime.close()
	return runtime.server.Run(ctx)
}

type deploymentConfig struct {
	listenAddress        string
	databaseURL          string
	sessionIssuer        string
	sessionAudience      string
	sessionKeyID         string
	sessionPublicKeyFile string
	serverCertFile       string
	serverKeyFile        string
	proxyAddress         string
	proxyServerName      string
	proxyCAFile          string
	proxyCertFile        string
	proxyKeyFile         string
	kubeAPIServerName    string
	browserOIDC          *browserOIDCDeploymentConfig
}

type browserOIDCDeploymentConfig struct {
	issuer                string
	clientID              string
	redirectURI           string
	sessionPrivateKeyFile string
}

func loadDeploymentConfig(lookup func(string) (string, bool)) (deploymentConfig, error) {
	if lookup == nil {
		return deploymentConfig{}, fmt.Errorf("load hub configuration: environment lookup is required")
	}
	config := deploymentConfig{}
	var err error
	for _, value := range []struct {
		name   string
		target *string
	}{
		{"SITH_HUB_LISTEN_ADDR", &config.listenAddress},
		{"SITH_HUB_DATABASE_URL", &config.databaseURL},
		{"SITH_HUB_SESSION_ISSUER", &config.sessionIssuer},
		{"SITH_HUB_SESSION_AUDIENCE", &config.sessionAudience},
		{"SITH_HUB_SESSION_KEY_ID", &config.sessionKeyID},
		{"SITH_HUB_SESSION_PUBLIC_KEY_FILE", &config.sessionPublicKeyFile},
		{"SITH_HUB_SERVER_TLS_CERT_FILE", &config.serverCertFile},
		{"SITH_HUB_SERVER_TLS_KEY_FILE", &config.serverKeyFile},
		{"SITH_HUB_PROXY_ADDRESS", &config.proxyAddress},
		{"SITH_HUB_PROXY_SERVER_NAME", &config.proxyServerName},
		{"SITH_HUB_PROXY_CA_FILE", &config.proxyCAFile},
		{"SITH_HUB_PROXY_CERT_FILE", &config.proxyCertFile},
		{"SITH_HUB_PROXY_KEY_FILE", &config.proxyKeyFile},
		{"SITH_HUB_KUBE_API_SERVER_NAME", &config.kubeAPIServerName},
	} {
		*value.target, err = requiredEnvironment(lookup, value.name)
		if err != nil {
			return deploymentConfig{}, err
		}
	}
	if err := validateListenAddress(config.listenAddress); err != nil {
		return deploymentConfig{}, fmt.Errorf("load hub configuration: listen address is invalid")
	}
	browserOIDC, err := loadBrowserOIDCDeploymentConfig(lookup)
	if err != nil {
		return deploymentConfig{}, err
	}
	config.browserOIDC = browserOIDC
	return config, nil
}

func loadBrowserOIDCDeploymentConfig(lookup func(string) (string, bool)) (*browserOIDCDeploymentConfig, error) {
	config := &browserOIDCDeploymentConfig{}
	fields := []struct {
		name   string
		target *string
	}{
		{"SITH_HUB_BROWSER_OIDC_ISSUER", &config.issuer},
		{"SITH_HUB_BROWSER_OIDC_CLIENT_ID", &config.clientID},
		{"SITH_HUB_BROWSER_OIDC_REDIRECT_URI", &config.redirectURI},
		{"SITH_HUB_SESSION_PRIVATE_KEY_FILE", &config.sessionPrivateKeyFile},
	}
	configured := 0
	for _, field := range fields {
		value, present := lookup(field.name)
		if present || value != "" {
			configured++
		}
	}
	if configured == 0 {
		return nil, nil
	}
	if configured != len(fields) {
		return nil, fmt.Errorf("load hub configuration: browser OIDC inputs must be set together")
	}
	for _, field := range fields {
		value, err := requiredEnvironment(lookup, field.name)
		if err != nil {
			return nil, err
		}
		*field.target = value
	}
	return config, nil
}

func requiredEnvironment(lookup func(string) (string, bool), name string) (string, error) {
	value, present := lookup(name)
	if !present || value == "" || strings.TrimSpace(value) != value || len(value) > 4096 {
		return "", fmt.Errorf("load hub configuration: %s is required", name)
	}
	return value, nil
}

func validateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("listen address must include host and port")
	}
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil || value == 0 {
		return fmt.Errorf("listen address must use a non-zero port")
	}
	return nil
}

func loadServerTLS(config deploymentConfig) (*tls.Config, error) {
	certificate, err := loadMountedCertificate("hub server certificate", config.serverCertFile, config.serverKeyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, nil
}

func loadProxyTLS(config deploymentConfig) (*tls.Config, error) {
	caPEM, err := readMountedFile("proxy CA bundle", config.proxyCAFile, maxMountedCABundleBytes)
	if err != nil {
		return nil, err
	}
	defer clear(caPEM)
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("load hub configuration: proxy CA bundle is invalid")
	}
	certificate, err := loadMountedCertificate("proxy client certificate", config.proxyCertFile, config.proxyKeyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		RootCAs: pool, MinVersion: tls.VersionTLS12, ServerName: config.proxyServerName, Certificates: []tls.Certificate{certificate},
	}, nil
}

func loadMountedCertificate(label, certificateFile, keyFile string) (tls.Certificate, error) {
	certificatePEM, err := readMountedFile(label, certificateFile, maxMountedCertificateBytes)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer clear(certificatePEM)
	keyPEM, err := readMountedFile(label+" key", keyFile, maxMountedKeyBytes)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer clear(keyPEM)
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil || len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
		return tls.Certificate{}, fmt.Errorf("load hub configuration: %s is invalid", label)
	}
	return certificate, nil
}

func loadSessionPublicKey(path string) (ed25519.PublicKey, error) {
	encoded, err := readMountedFile("session public key", path, maxMountedPublicKeyBytes)
	if err != nil {
		return nil, err
	}
	defer clear(encoded)
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("load hub configuration: session public key is invalid")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("load hub configuration: session public key is invalid")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("load hub configuration: session public key is not Ed25519")
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

func loadSessionPrivateKey(path string) (ed25519.PrivateKey, error) {
	encoded, err := readMountedFile("session private key", path, maxMountedPrivateKeyBytes)
	if err != nil {
		return nil, err
	}
	defer clear(encoded)
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("load hub configuration: session private key is invalid")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("load hub configuration: session private key is invalid")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("load hub configuration: session private key is not Ed25519")
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}

func readMountedFile(label, path string, maxBytes int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 {
		return nil, fmt.Errorf("load hub configuration: %s must be a read-only regular file", label)
	}
	// #nosec G304 -- path is a required deployment input, validated as a bounded read-only regular file immediately above.
	contents, err := os.ReadFile(path)
	if err != nil || len(contents) == 0 || len(contents) > maxBytes {
		clear(contents)
		return nil, fmt.Errorf("load hub configuration: %s is unavailable", label)
	}
	return contents, nil
}
