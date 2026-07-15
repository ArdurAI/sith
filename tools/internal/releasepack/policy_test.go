// SPDX-License-Identifier: Apache-2.0

package releasepack

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestReleasePolicyIsFailClosed(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	release := readRepositoryFile(t, root, ".github/workflows/release.yml")
	config := readRepositoryFile(t, root, ".goreleaser.yaml")

	for _, want := range []string{
		"id-token: write",
		"attestations: write",
		"packages: write",
		"persist-credentials: false",
		"release tag must point to a commit reachable from main",
		"release tag must be annotated",
		"release tag must carry a signature verified by GitHub",
		"cosign sign-blob --yes --bundle=dist/sith.rb.sigstore.json dist/sith.rb",
		"HUB_IMAGE: ghcr.io/ardurai/sith-hub",
		"tags: ${{ env.HUB_IMAGE }}:${{ github.ref_name }}",
		"cosign sign --yes \"$image\"",
		`gh release edit "$GITHUB_REF_NAME" --draft=false --latest`,
	} {
		if !strings.Contains(release, want) {
			t.Errorf("release workflow does not enforce %q", want)
		}
	}
	releaseJob := releaseWorkflowJob(release, "release")
	if !strings.Contains(releaseJob, "packages: write") {
		t.Error("release job does not grant the package publication permission")
	}
	guardStep := releaseWorkflowStep(release, "Guard hub image tag against overwrite")
	for _, want := range []string{
		`HUB_TAG: ${{ env.HUB_IMAGE }}:${{ github.ref_name }}`,
		`docker manifest inspect "$HUB_TAG"`,
		"hub image tag already exists; immutable release tags cannot be overwritten",
		"could not establish whether the hub image tag exists",
	} {
		if !strings.Contains(guardStep, want) {
			t.Errorf("hub image overwrite guard does not enforce %q", want)
		}
	}
	signingStep := releaseWorkflowStep(release, "Sign and verify published hub image")
	for _, want := range []string{
		"HUB_DIGEST: ${{ steps.hub_image.outputs.digest }}",
		`image="${HUB_IMAGE}@${HUB_DIGEST}"`,
		`cosign sign --yes "$image"`,
	} {
		if !strings.Contains(signingStep, want) {
			t.Errorf("hub image signing step does not enforce %q", want)
		}
	}
	if count := strings.Count(release, "uses: actions/attest@"); count != 7 {
		t.Errorf("release workflow has %d attestation steps, want archive provenance, four archive SBOMs, image provenance, and image SBOM", count)
	}
	for _, name := range []string{"Attest hub image build provenance", "Attest hub image SBOM"} {
		step := releaseWorkflowStep(release, name)
		for _, want := range []string{
			"uses: actions/attest@",
			"subject-name: ${{ env.HUB_IMAGE }}",
			"subject-digest: ${{ steps.hub_image.outputs.digest }}",
			"push-to-registry: true",
		} {
			if !strings.Contains(step, want) {
				t.Errorf("%s does not enforce %q", name, want)
			}
		}
	}
	for _, forbidden := range []string{"pull_request_target:", "workflow_run:", "HOMEBREW_TAP_TOKEN", "PERSONAL_AUTH_TOKEN"} {
		if strings.Contains(release, forbidden) {
			t.Errorf("release workflow contains forbidden trust expansion %q", forbidden)
		}
	}

	for _, want := range []string{
		"-buildvcs=false",
		"-mod=readonly",
		"GOPROXY=off",
		"Date={{ .CommitDate }}",
		`mod_timestamp: "{{ .CommitTimestamp }}"`,
		"artifacts: archive",
		"artifacts: sbom",
		"artifacts: checksum",
		"draft: true",
		"replace_existing_draft: true",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("GoReleaser configuration does not enforce %q", want)
		}
	}
}

func TestWorkflowActionsUseImmutableRefs(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	immutable := regexp.MustCompile(`^[0-9a-f]{40}$`)
	use := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*[^@\s]+@([^\s#]+)`)
	for _, name := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		contents := readRepositoryFile(t, root, name)
		matches := use.FindAllStringSubmatch(contents, -1)
		if len(matches) == 0 {
			t.Fatalf("%s contains no action references", name)
		}
		for _, match := range matches {
			if !immutable.MatchString(match[1]) {
				t.Errorf("%s uses mutable action ref %q", name, match[1])
			}
		}
	}
}

func TestReleaseGuidePinsSPDXPredicateVersion(t *testing.T) {
	t.Parallel()
	guide := readRepositoryFile(t, repositoryRoot(t), "docs/RELEASE.md")
	if !strings.Contains(guide, "--predicate-type https://spdx.dev/Document/v2.3") {
		t.Fatal("release guide does not pin the SPDX 2.3 attestation predicate URI")
	}
	if strings.Contains(guide, "--predicate-type https://spdx.dev/Document \\") {
		t.Fatal("release guide contains the unversioned SPDX predicate URI")
	}
}

func TestReleaseGuideRequiresImmutableHubImageVerification(t *testing.T) {
	t.Parallel()
	guide := readRepositoryFile(t, repositoryRoot(t), "docs/RELEASE.md")
	if !strings.Contains(guide, `case "$image" in ghcr.io/ardurai/sith-hub@sha256:*)`) {
		t.Fatal("release guide does not require a complete immutable hub image digest")
	}
	verification := markdownCodeBlockAfter(guide, "identity=\"https://github.com/ArdurAI/sith/.github/workflows/release.yml")
	for _, want := range []string{
		"cosign verify",
		`gh attestation verify "oci://$image"`,
		"--signer-workflow ArdurAI/sith/.github/workflows/release.yml",
	} {
		if !strings.Contains(verification, want) {
			t.Errorf("release guide does not require immutable hub image verification %q", want)
		}
	}
	if count := strings.Count(verification, "$image"); count != 3 {
		t.Errorf("release guide uses the immutable image reference %d times, want cosign plus two attestations", count)
	}
	if strings.Contains(guide, "ghcr.io/ardurai/sith-hub:latest") {
		t.Error("release guide permits a mutable hub image tag")
	}
}

func TestInstallDocsUseFormulaScopedHomebrewTrust(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	for _, name := range []string{"README.md", "docs/RELEASE.md"} {
		contents := readRepositoryFile(t, root, name)
		if !strings.Contains(contents, "brew trust --formula ArdurAI/tap/sith") {
			t.Errorf("%s does not require formula-scoped Homebrew trust", name)
		}
		if strings.Contains(contents, "brew trust ArdurAI/tap") {
			t.Errorf("%s broadens trust to the entire Homebrew tap", name)
		}
	}
}

func releaseWorkflowJob(workflow, name string) string {
	marker := "\n  " + name + ":\n"
	start := strings.Index(workflow, marker)
	if start < 0 {
		return ""
	}
	job := workflow[start:]
	headers := regexp.MustCompile(`(?m)^  [[:alnum:]_-]+:\n`).FindAllStringIndex(job, -1)
	if len(headers) > 1 {
		return job[:headers[1][0]]
	}
	return job
}

func releaseWorkflowStep(workflow, name string) string {
	marker := "\n      - name: " + name + "\n"
	start := strings.Index(workflow, marker)
	if start < 0 {
		return ""
	}
	step := workflow[start:]
	if next := strings.Index(step[len(marker):], "\n      - name: "); next >= 0 {
		return step[:len(marker)+next]
	}
	return step
}

func markdownCodeBlockAfter(contents, marker string) string {
	start := strings.Index(contents, marker)
	if start < 0 {
		return ""
	}
	block := contents[start:]
	if end := strings.Index(block, "\n```"); end >= 0 {
		return block[:end]
	}
	return block
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate release policy test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
}

func readRepositoryFile(t *testing.T, root, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, name)) // #nosec G304 -- test paths are fixed relative to this source file.
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(contents)
}
