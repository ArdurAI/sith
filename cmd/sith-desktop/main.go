// SPDX-License-Identifier: Apache-2.0

// Sith desktop starts the native macOS shell for the local fleet IDE.
package main

import (
	"os"

	"github.com/ArdurAI/sith/internal/cli"
)

func main() {
	os.Exit(cli.ExecuteDesktop())
}
