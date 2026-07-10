// SPDX-License-Identifier: Apache-2.0

// Package main is the process entrypoint for the Sith binary.
package main

import (
	"os"

	"github.com/ArdurAI/sith/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
