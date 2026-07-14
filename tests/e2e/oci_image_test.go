// SPDX-License-Identifier: Apache-2.0
//go:build e2e && oci

package e2e_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestOCIImageCrossPlatformContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	assertContainerfileContract(t, root)

	for _, architecture := range []string{"amd64", "arm64"} {
		architecture := architecture
		t.Run(architecture, func(t *testing.T) {
			tag := buildOCIImage(ctx, t, root, architecture)
			assertOCIImageContract(t, inspectOCIImage(ctx, t, tag), architecture)
			if architecture != runtime.GOARCH {
				return
			}
			version := exec.CommandContext(
				ctx,
				"docker", "run", "--rm", "--read-only", "--network", "none", "--cap-drop", "ALL",
				"--security-opt", "no-new-privileges", "--user", "65532:65532", tag, "version", "--output", "json",
			)
			output, err := version.CombinedOutput()
			if err != nil || !json.Valid(output) {
				t.Fatalf("run hardened native OCI image: %v\n%s", err, output)
			}

			shell := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "/bin/sh", tag)
			if output, err := shell.CombinedOutput(); err == nil {
				t.Fatalf("distroless OCI image unexpectedly started a shell: %s", output)
			}
		})
	}
}
