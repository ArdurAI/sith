// SPDX-License-Identifier: Apache-2.0
//go:build e2e && oci

package e2e_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
)

type ociImageInspection struct {
	Architecture string `json:"Architecture"`
	OS           string `json:"Os"`
	Config       struct {
		Entrypoint []string `json:"Entrypoint"`
		User       string   `json:"User"`
	} `json:"Config"`
}

func inspectOCIImage(ctx context.Context, t *testing.T, tag string) ociImageInspection {
	t.Helper()
	inspect := exec.CommandContext(ctx, "docker", "image", "inspect", tag)
	output, err := inspect.Output()
	if err != nil {
		t.Fatalf("inspect OCI image %s: %v", tag, err)
	}
	var images []ociImageInspection
	if err := json.Unmarshal(output, &images); err != nil || len(images) != 1 {
		t.Fatalf("decode OCI image %s inspection: %#v / %v", tag, images, err)
	}
	return images[0]
}

func assertOCIImageContract(t *testing.T, image ociImageInspection, architecture string) {
	t.Helper()
	if image.OS != "linux" || image.Architecture != architecture {
		t.Fatalf("OCI image platform = %s/%s, want linux/%s", image.OS, image.Architecture, architecture)
	}
	if image.Config.User != "65532:65532" {
		t.Fatalf("OCI image user = %q, want 65532:65532", image.Config.User)
	}
	if len(image.Config.Entrypoint) != 1 || image.Config.Entrypoint[0] != "/usr/local/bin/sith" {
		t.Fatalf("OCI image entrypoint = %#v, want Sith binary", image.Config.Entrypoint)
	}
}
