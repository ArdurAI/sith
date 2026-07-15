// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
)

// ExecuteDesktop runs the packaged macOS application entry point.
func ExecuteDesktop() int {
	adapter := kubeconfig.Default()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := runDesktop(ctx, adapter, adapter, ""); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
