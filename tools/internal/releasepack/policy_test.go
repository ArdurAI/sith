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
		"persist-credentials: false",
		"release tag must point to a commit reachable from main",
		"release tag must be annotated",
		"release tag must carry a signature verified by GitHub",
		"cosign sign-blob --yes --bundle=dist/sith.rb.sigstore.json dist/sith.rb",
		`gh release edit "$GITHUB_REF_NAME" --draft=false --latest`,
	} {
		if !strings.Contains(release, want) {
			t.Errorf("release workflow does not enforce %q", want)
		}
	}
	if count := strings.Count(release, "uses: actions/attest@"); count != 5 {
		t.Errorf("release workflow has %d attestation steps, want one provenance and four SBOM attestations", count)
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
