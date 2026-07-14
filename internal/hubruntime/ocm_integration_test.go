// SPDX-License-Identifier: Apache-2.0
//go:build e2e && ocm

package hubruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"k8s.io/client-go/kubernetes"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/hubocm"
	"github.com/ArdurAI/sith/internal/hubserver"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/tests/testutil/ocmlab"
)

const m0RuntimeWorkspaceID tenancy.WorkspaceID = "workspace-m0"

func TestHubRuntimeDirectClusterProxyM0(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	hubClient, err := kubernetes.NewForConfig(ocmlab.HubConfig(t))
	if err != nil {
		t.Fatal("construct M0 hub client failed")
	}
	credentialReader, err := hubocm.NewManagedServiceAccountReader(hubClient.CoreV1())
	if err != nil {
		t.Fatal("construct scoped MSA reader failed")
	}
	transport, err := hubocm.New(hubocm.Config{
		CredentialReader:  credentialReader,
		ProxyAddress:      ocmlab.StartProxyPortForward(ctx, t),
		ProxyTLSConfig:    ocmlab.ProxyTLS(ctx, t, hubClient),
		KubeAPIServerName: "kubernetes",
	})
	if err != nil {
		t.Fatal("construct direct OCM transport failed")
	}
	store := &m0RuntimeStore{
		spokes: []hubfleet.Spoke{
			{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"},
			{ID: "spoke-b", ManagedClusterRef: "ocm/spoke-b"},
		},
		snapshots: make(map[string]hubfleet.Snapshot), failures: make(map[string]hubfleet.FailureKind),
	}
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := hubfleet.NewCollector(hubfleet.CollectorConfig{Store: store, Transport: transport, PEP: enforcer})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	verifier, privateKey := m0RuntimeVerifier(t, now)
	handler, err := hubserver.NewFleetHandler(hubserver.FleetHandlerConfig{
		Verifier: verifier, Collector: collector, Reader: store, PEP: enforcer,
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS := runtimeTestTLS(t)
	server, err := NewServer(ServerConfig{Listener: listener, Handler: handler, TLSConfig: serverTLS})
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(serverCtx) }()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: 2 * time.Minute}
	defer client.CloseIdleConnections()
	endpoint := "https://" + listener.Addr().String() + "/v1/workspaces/workspace-m0"
	token := m0RuntimeToken(t, privateKey, now)
	refresh := m0RuntimeRequest(t, ctx, client, http.MethodPost, endpoint+"/fleet:refresh", token)
	defer refresh.Body.Close()
	if refresh.StatusCode != http.StatusOK {
		t.Fatalf("runtime refresh status = %d", refresh.StatusCode)
	}
	var coverage fleet.Coverage
	if err := json.NewDecoder(refresh.Body).Decode(&coverage); err != nil {
		t.Fatal(err)
	}
	if coverage.Requested != 2 || coverage.Reachable != 2 || len(coverage.Unreachable) != 0 || len(coverage.Stale) != 0 {
		t.Fatalf("runtime direct refresh coverage = %#v", coverage)
	}
	fleetResponse := m0RuntimeRequest(t, ctx, client, http.MethodGet, endpoint+"/fleet", token)
	defer fleetResponse.Body.Close()
	if fleetResponse.StatusCode != http.StatusOK {
		t.Fatalf("runtime fleet status = %d", fleetResponse.StatusCode)
	}
	var result fleet.FleetResult
	if err := json.NewDecoder(fleetResponse.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Clusters) != 2 || result.Coverage.Requested != 2 || result.Coverage.Reachable != 2 {
		t.Fatalf("runtime direct fleet = %#v", result)
	}
	stopServer()
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

type m0RuntimeStore struct {
	mu        sync.Mutex
	spokes    []hubfleet.Spoke
	snapshots map[string]hubfleet.Snapshot
	failures  map[string]hubfleet.FailureKind
}

func (store *m0RuntimeStore) RegisteredSpokes(_ context.Context, scope tenancy.Scope) ([]hubfleet.Spoke, error) {
	if err := scope.RequireWorkspace(m0RuntimeWorkspaceID); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]hubfleet.Spoke(nil), store.spokes...), nil
}

func (store *m0RuntimeStore) ReplaceSnapshot(
	_ context.Context,
	scope tenancy.Scope,
	spoke hubfleet.Spoke,
	snapshot hubfleet.Snapshot,
	_ time.Time,
) error {
	if err := scope.RequireWorkspace(m0RuntimeWorkspaceID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshots[spoke.ID] = snapshot
	delete(store.failures, spoke.ID)
	return nil
}

func (store *m0RuntimeStore) MarkSnapshotFailure(
	_ context.Context,
	scope tenancy.Scope,
	spoke hubfleet.Spoke,
	failure hubfleet.FailureKind,
	_ time.Time,
) (bool, error) {
	if err := scope.RequireWorkspace(m0RuntimeWorkspaceID); err != nil {
		return false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	_, retained := store.snapshots[spoke.ID]
	store.failures[spoke.ID] = failure
	return retained, nil
}

func (store *m0RuntimeStore) ReadFleet(
	_ context.Context,
	scope tenancy.Scope,
	_ time.Duration,
	_ time.Time,
) (fleet.FleetResult, error) {
	if err := scope.RequireWorkspace(m0RuntimeWorkspaceID); err != nil {
		return fleet.FleetResult{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result := fleet.FleetResult{Clusters: make([]fleet.Cluster, 0, len(store.spokes)), Coverage: fleet.Coverage{Requested: len(store.spokes)}}
	for _, spoke := range store.spokes {
		snapshot, exists := store.snapshots[spoke.ID]
		failure := store.failures[spoke.ID]
		reachable := exists && failure == ""
		if reachable {
			result.Coverage.Reachable++
		} else {
			result.Coverage.Unreachable = append(result.Coverage.Unreachable, spoke.ID)
			if exists {
				result.Coverage.Stale = append(result.Coverage.Stale, spoke.ID)
			}
		}
		result.Clusters = append(result.Clusters, fleet.Cluster{
			Name: spoke.ID, Context: spoke.ManagedClusterRef, SourceKind: hubfleet.SourceKind, Reachable: reachable, ObservedAt: snapshot.ObservedAt,
		})
	}
	return result, nil
}

type m0RuntimeClaims struct {
	Memberships map[string]tenancy.Role `json:"memberships"`
	jwt.RegisteredClaims
}

func m0RuntimeVerifier(t *testing.T, now time.Time) (*hubauth.JWTVerifier, ed25519.PrivateKey) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	verifier, err := hubauth.NewJWTVerifier(hubauth.JWTConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test", Keys: map[string]ed25519.PublicKey{"m0-session": publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return verifier, privateKey
}

func m0RuntimeToken(t *testing.T, privateKey ed25519.PrivateKey, now time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, m0RuntimeClaims{
		Memberships: map[string]tenancy.Role{string(m0RuntimeWorkspaceID): tenancy.RoleReader},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "https://issuer.sith.test", Subject: "user:m0", Audience: jwt.ClaimStrings{"https://hub.sith.test"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), ID: "m0-session-1",
		},
	})
	token.Header["typ"] = "sith-session+jwt"
	token.Header["kid"] = "m0-session"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func m0RuntimeRequest(t *testing.T, ctx context.Context, client *http.Client, method, endpoint, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
