// SPDX-License-Identifier: Apache-2.0

package fleet

import "context"

// Source is the read seam implemented by every fleet snapshot backend.
type Source interface {
	// Kind identifies the adapter, such as stub, local-kubeconfig, or ocm-spoke.
	Kind() string
	// Fleet returns the current normalized fleet snapshot for this source.
	Fleet(ctx context.Context) (FleetResult, error)
}
