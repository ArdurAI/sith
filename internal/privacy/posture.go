// SPDX-License-Identifier: Apache-2.0

// Package privacy defines the permanent local-mode trust posture.
package privacy

// Posture describes whether local mode requires an account or emits telemetry.
type Posture struct {
	AccountRequired bool `json:"account_required"`
	Telemetry       bool `json:"telemetry"`
}

// LocalMode returns Sith's locked Phase-L privacy posture.
func LocalMode() Posture {
	return Posture{AccountRequired: false, Telemetry: false}
}
