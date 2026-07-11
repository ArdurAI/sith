// SPDX-License-Identifier: Apache-2.0

package privacy

import "testing"

func TestLocalModeRequiresNoAccountAndEmitsNoTelemetry(t *testing.T) {
	t.Parallel()
	posture := LocalMode()
	if posture.AccountRequired || posture.Telemetry {
		t.Fatalf("local posture = %#v", posture)
	}
}
