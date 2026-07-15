//go:build !darwin

// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/localops"
)

func runDesktop(context.Context, connector.Reader, localops.Client, string) error {
	return fmt.Errorf("local fleet desktop is currently available only on macOS")
}
