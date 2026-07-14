// SPDX-License-Identifier: Apache-2.0
//go:build e2e && (oci || kind)

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const distrolessRuntimeImage = "gcr.io/distroless/static-debian12@sha256:b7bb25d9f7c31d2bdd1982feb4dafcaf137703c7075dbe2febb41c24212b946f"

var forbiddenContainerfileInstruction = regexp.MustCompile(`(?im)^[\t ]*(?:run|add)\b`)

func buildOCIImage(ctx context.Context, t *testing.T, root, architecture string) string {
	t.Helper()
	if architecture != "amd64" && architecture != "arm64" {
		t.Fatalf("unsupported OCI architecture %q", architecture)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("find docker: %v", err)
	}

	contextDir := t.TempDir()
	binary := filepath.Join(contextDir, "bin", "linux", architecture, "sith")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("create OCI build context: %v", err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-mod=readonly", "-o", binary, "./cmd/sith")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+architecture)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build linux/%s Sith binary: %v\n%s", architecture, err, output)
	}

	tag := fmt.Sprintf("sith-oci-test:%s-%d", architecture, time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "image", "rm", "--force", tag).Run()
	})
	buildImage := exec.CommandContext(
		ctx,
		"docker", "buildx", "build", "--platform", "linux/"+architecture, "--load", "--tag", tag,
		"--file", filepath.Join(root, "Containerfile"), contextDir,
	)
	if output, err := buildImage.CombinedOutput(); err != nil {
		t.Fatalf("build linux/%s OCI image: %v\n%s", architecture, err, output)
	}
	return tag
}

func assertContainerfileContract(t *testing.T, root string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "Containerfile"))
	if err != nil {
		t.Fatalf("read Containerfile: %v", err)
	}
	if err := validateContainerfileContract(string(contents)); err != nil {
		t.Fatal(err)
	}
}

func validateContainerfileContract(contract string) error {
	if forbiddenContainerfileInstruction.MatchString(contract) {
		return fmt.Errorf("Containerfile must not install packages or fetch build inputs")
	}
	expected := map[string]string{
		"ARG":        "ARG TARGETARCH",
		"COPY":       "COPY --chown=65532:65532 --chmod=0555 bin/linux/${TARGETARCH}/sith /usr/local/bin/sith",
		"ENTRYPOINT": "ENTRYPOINT [\"/usr/local/bin/sith\"]",
		"FROM":       "FROM " + distrolessRuntimeImage,
		"USER":       "USER 65532:65532",
	}
	found := make(map[string][]string, len(expected))
	for lineNumber, line := range strings.Split(contract, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		instruction := strings.Join(strings.Fields(line), " ")
		directive := strings.ToUpper(strings.Fields(instruction)[0])
		if _, permitted := expected[directive]; !permitted {
			return fmt.Errorf("Containerfile line %d uses forbidden instruction %q", lineNumber+1, directive)
		}
		found[directive] = append(found[directive], instruction)
	}
	for directive, required := range expected {
		if actual := found[directive]; len(actual) != 1 || actual[0] != required {
			return fmt.Errorf("Containerfile must contain exactly %q, got %q", required, actual)
		}
	}
	return nil
}

func TestContainerfileInstructionGuard(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{"RUN true", "run true", "RuN true", "ADD https://example.invalid/input /input"} {
		if !forbiddenContainerfileInstruction.MatchString(instruction) {
			t.Fatalf("instruction guard accepted %q", instruction)
		}
	}
	if forbiddenContainerfileInstruction.MatchString("# RUN is forbidden, but this comment is not an instruction") {
		t.Fatal("instruction guard rejected a comment")
	}
	valid := strings.Join([]string{
		"FROM " + distrolessRuntimeImage,
		"ARG TARGETARCH",
		"COPY --chown=65532:65532 --chmod=0555 bin/linux/${TARGETARCH}/sith /usr/local/bin/sith",
		"USER 65532:65532",
		"ENTRYPOINT [\"/usr/local/bin/sith\"]",
	}, "\n")
	if err := validateContainerfileContract(valid); err != nil {
		t.Fatalf("valid Containerfile rejected: %v", err)
	}
	for name, invalid := range map[string]string{
		"comment cannot satisfy requirement": strings.Replace(valid, "USER 65532:65532", "# USER 65532:65532", 1),
		"second from":                        valid + "\nFROM scratch",
		"second user":                        valid + "\nUSER root",
		"second entrypoint":                  valid + "\nENTRYPOINT [\"/bin/sh\"]",
		"unrecognized":                       valid + "\nENV PATH=/tmp",
	} {
		if err := validateContainerfileContract(invalid); err == nil {
			t.Fatalf("%s Containerfile accepted", name)
		}
	}
}
