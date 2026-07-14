// SPDX-License-Identifier: Apache-2.0

// Package hubocm implements the pinned, direct OCM ClusterProxy read adapter.
//
// It deliberately keeps the OCM reverse tunnel and managed-serviceaccount identity
// substrate outside Sith's persistence model. The adapter reads one fixed projected
// credential per registered managed cluster, opens short-lived Konnectivity tunnels
// to that exact cluster, and returns only normalized inventory and health facts.
//
// Proxy client mTLS material belongs in a read-only deployment mount. It is supplied
// as a TLS configuration at construction time and is never read from, or persisted to,
// Sith-managed storage.
package hubocm
