// SPDX-License-Identifier: Apache-2.0

// Package pep provides the hub policy-enforcement boundary for tenant-scoped reads and future
// typed intents. Local kubeconfig operations deliberately do not import this package: they use the
// operator's own identity and are outside the governed hub path.
package pep
