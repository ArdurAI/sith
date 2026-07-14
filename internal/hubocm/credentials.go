// SPDX-License-Identifier: Apache-2.0

package hubocm

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	managedClusterRefPrefix = "ocm/"
	managedServiceAccount   = "sith-reader"
	maxTokenBytes           = 16 * 1024
	maxCABundleBytes        = 256 * 1024
)

// CredentialReader returns the current scoped managed-serviceaccount material for one
// already registered managed cluster. The returned value is adapter-internal only.
type CredentialReader interface {
	Read(context.Context, tenancy.WorkspaceID, string) (projectedCredential, error)
}

// ManagedServiceAccountReader reads exactly the projected sith-reader Secret. It does
// not use list or watch operations, and it intentionally has no configurable Secret name.
type ManagedServiceAccountReader struct {
	secrets corev1client.CoreV1Interface
}

// NewManagedServiceAccountReader constructs the narrow Secret reader used by the direct
// transport. The caller's Kubernetes RBAC must grant get on resourceName sith-reader only
// in each managed-cluster namespace.
func NewManagedServiceAccountReader(secrets corev1client.CoreV1Interface) (*ManagedServiceAccountReader, error) {
	if secrets == nil {
		return nil, fmt.Errorf("new managed-serviceaccount reader: Kubernetes core client is required")
	}
	return &ManagedServiceAccountReader{secrets: secrets}, nil
}

// Read obtains a fresh credential on every snapshot; it intentionally keeps no cache.
func (reader *ManagedServiceAccountReader) Read(
	ctx context.Context,
	workspaceID tenancy.WorkspaceID,
	managedCluster string,
) (projectedCredential, error) {
	if reader == nil || reader.secrets == nil || ctx == nil {
		return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: reader and context are required")
	}
	if err := tenancy.ValidateWorkspaceID(workspaceID); err != nil {
		return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: workspace is invalid")
	}
	if err := validateManagedClusterName(managedCluster); err != nil {
		return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: managed cluster is invalid")
	}
	secret, err := reader.secrets.Secrets(managedCluster).Get(ctx, managedServiceAccount, metav1.GetOptions{})
	if err != nil {
		return projectedCredential{}, contextOrGeneric(ctx, "read projected managed-serviceaccount credential")
	}
	if secret == nil || secret.Namespace != managedCluster {
		return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: projected Secret is invalid")
	}
	return credentialFromSecret(secret)
}

type projectedCredential struct {
	token []byte
	ca    []byte
}

func credentialFromSecret(secret *corev1.Secret) (projectedCredential, error) {
	if secret == nil || secret.Name != managedServiceAccount || secret.Namespace == "" ||
		len(validation.IsDNS1123Label(secret.Namespace)) != 0 {
		return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: projected Secret is invalid")
	}
	if len(secret.Data) != 2 {
		return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: projected Secret keys are invalid")
	}
	token, hasToken := secret.Data["token"]
	ca, hasCA := secret.Data["ca.crt"]
	if !hasToken || !hasCA || len(token) == 0 || len(token) > maxTokenBytes || len(ca) == 0 || len(ca) > maxCABundleBytes {
		return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: projected Secret payload is invalid")
	}
	for key := range secret.Data {
		if key != "token" && key != "ca.crt" {
			return projectedCredential{}, fmt.Errorf("read managed-serviceaccount credential: projected Secret keys are invalid")
		}
	}
	return projectedCredential{token: append([]byte(nil), token...), ca: append([]byte(nil), ca...)}, nil
}

func parseManagedClusterRef(reference string) (string, error) {
	if !strings.HasPrefix(reference, managedClusterRefPrefix) {
		return "", fmt.Errorf("managed cluster reference must use the %q prefix", managedClusterRefPrefix)
	}
	name := strings.TrimPrefix(reference, managedClusterRefPrefix)
	if err := validateManagedClusterName(name); err != nil {
		return "", err
	}
	return name, nil
}

func validateManagedClusterName(name string) error {
	if len(validation.IsDNS1123Label(name)) != 0 {
		return fmt.Errorf("managed cluster name is invalid")
	}
	return nil
}
