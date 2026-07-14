// SPDX-License-Identifier: Apache-2.0
//go:build e2e && ocm

package hubocm

import (
	"context"
	"crypto/subtle"
	"net/http"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/tests/testutil/ocmlab"
)

const (
	m0WorkspaceID = "workspace-m0"
)

// TestDirectClusterProxyM0 proves the direct Konnectivity path against the retained
// M0 lab. The test deliberately never reads an admin kubeconfig for either spoke.
func TestDirectClusterProxyM0(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	hubConfig := ocmlab.HubConfig(t)
	hubClient, err := kubernetes.NewForConfig(hubConfig)
	if err != nil {
		t.Fatal("construct M0 hub client failed")
	}
	proxyAddress := ocmlab.StartProxyPortForward(ctx, t)
	proxyTLS := ocmlab.ProxyTLS(ctx, t, hubClient)
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
		if !hasInventoryFor(snapshot, "Deployment") || !hasInventoryFor(snapshot, "Pod") || !hasCVEFor(snapshot) {
			t.Fatal("direct OCM snapshot did not contain the scoped inventory and runtime-proven CVE evidence")
		}
	}

	assertDirectSecretsForbidden(ctx, t, adapter, reader)
	assertMSARotation(ctx, t, hubClient, reader, adapter)
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

func hasInventoryFor(snapshot hubfleet.Snapshot, kind string) bool {
	for _, fact := range snapshot.Facts {
		if fact.Kind == "inventory" && fact.Ref.Kind == kind {
			return true
		}
	}
	return false
}

func hasCVEFor(snapshot hubfleet.Snapshot) bool {
	for _, fact := range snapshot.Facts {
		if fact.Kind == "cve" && fact.Ref.Kind == "Image" && len(fact.Observed) != 0 {
			return true
		}
	}
	return false
}
