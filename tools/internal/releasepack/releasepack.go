// SPDX-License-Identifier: Apache-2.0

// Package releasepack validates release artifacts and renders the Homebrew formula.
package releasepack

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

var (
	checksumLine = regexp.MustCompile(`^([0-9a-f]{64}) [ *]([^/\\]+)$`)
	versionValue = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
)

var targets = []struct {
	os   string
	arch string
}{
	{os: "darwin", arch: "amd64"},
	{os: "darwin", arch: "arm64"},
	{os: "linux", arch: "amd64"},
	{os: "linux", arch: "arm64"},
}

// Metadata is the stable subset of GoReleaser metadata used by the verifier.
type Metadata struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// ParseChecksums parses a sha256sum-compatible manifest and rejects ambiguous paths.
func ParseChecksums(reader io.Reader) (map[string]string, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read checksum manifest: %w", err)
	}

	checksums := make(map[string]string)
	for number, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		match := checksumLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if match == nil {
			return nil, fmt.Errorf("checksum line %d is not sha256sum format", number+1)
		}
		name := match[2]
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate checksum entry %q", name)
		}
		checksums[name] = match[1]
	}
	if len(checksums) == 0 {
		return nil, errors.New("checksum manifest is empty")
	}
	return checksums, nil
}

// ArchiveDigests returns the deterministic digest manifest used for rebuild comparison.
func ArchiveDigests(dist string) ([]byte, error) {
	metadata, checksums, err := loadDistribution(dist)
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, len(targets))
	for _, target := range targets {
		name := archiveName(metadata.Version, target.os, target.arch)
		digest, ok := checksums[name]
		if !ok {
			return nil, fmt.Errorf("checksum manifest is missing %s", name)
		}
		lines = append(lines, digest+"  "+name)
	}
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// RenderHomebrewFormula renders a formula whose URLs and hashes are bound to one release.
func RenderHomebrewFormula(version, tag string, checksums map[string]string) ([]byte, error) {
	if !versionValue.MatchString(version) {
		return nil, fmt.Errorf("invalid release version %q", version)
	}
	if tag != "v"+version {
		return nil, fmt.Errorf("tag %q does not match version %q", tag, version)
	}

	digest := func(goos, goarch string) (string, error) {
		name := archiveName(version, goos, goarch)
		value, ok := checksums[name]
		if !ok {
			return "", fmt.Errorf("checksum manifest is missing %s", name)
		}
		return value, nil
	}

	darwinAMD64, err := digest("darwin", "amd64")
	if err != nil {
		return nil, err
	}
	darwinARM64, err := digest("darwin", "arm64")
	if err != nil {
		return nil, err
	}
	linuxAMD64, err := digest("linux", "amd64")
	if err != nil {
		return nil, err
	}
	linuxARM64, err := digest("linux", "arm64")
	if err != nil {
		return nil, err
	}

	formula := fmt.Sprintf(`class Sith < Formula
  desc "Local-first, account-free Kubernetes fleet tool"
  homepage "https://github.com/ArdurAI/sith"
  version %q
  license "Apache-2.0"

  on_macos do
    on_intel do
      url %q
      sha256 %q
    end
    on_arm do
      url %q
      sha256 %q
    end
  end

  on_linux do
    on_intel do
      url %q
      sha256 %q
    end
    on_arm do
      url %q
      sha256 %q
    end
  end

  def install
    bin.install "sith"
  end

  test do
    output = shell_output("#{bin}/sith version --output json")
    assert_match %q, output
  end
end
`, version,
		releaseURL(tag, archiveName(version, "darwin", "amd64")), darwinAMD64,
		releaseURL(tag, archiveName(version, "darwin", "arm64")), darwinARM64,
		releaseURL(tag, archiveName(version, "linux", "amd64")), linuxAMD64,
		releaseURL(tag, archiveName(version, "linux", "arm64")), linuxARM64,
		`"version":"`+version+`"`)
	return []byte(formula), nil
}

// VerifyDistribution verifies checksums, archive shape, SBOMs, and the native binary metadata.
func VerifyDistribution(dist string) error {
	metadata, checksums, err := loadDistribution(dist)
	if err != nil {
		return err
	}
	if !versionValue.MatchString(metadata.Version) {
		return fmt.Errorf("metadata has invalid version %q", metadata.Version)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(metadata.Commit) {
		return fmt.Errorf("metadata has invalid commit %q", metadata.Commit)
	}
	if len(checksums) != len(targets)*2 {
		return fmt.Errorf("checksum manifest has %d entries, want %d archives and SBOMs", len(checksums), len(targets)*2)
	}

	for name, want := range checksums {
		path := filepath.Join(dist, name)
		got, err := digestFile(path)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
		}
	}

	for _, target := range targets {
		archive := archiveName(metadata.Version, target.os, target.arch)
		sbom := archive + ".spdx.json"
		if _, ok := checksums[archive]; !ok {
			return fmt.Errorf("checksum manifest is missing %s", archive)
		}
		if _, ok := checksums[sbom]; !ok {
			return fmt.Errorf("checksum manifest is missing %s", sbom)
		}
		if err := verifyArchive(filepath.Join(dist, archive)); err != nil {
			return fmt.Errorf("verify %s: %w", archive, err)
		}
		if err := verifySBOM(filepath.Join(dist, sbom), archive); err != nil {
			return fmt.Errorf("verify %s: %w", sbom, err)
		}
		if target.os == runtime.GOOS && target.arch == runtime.GOARCH {
			if err := verifyNativeBinary(filepath.Join(dist, archive), metadata); err != nil {
				return fmt.Errorf("verify native binary: %w", err)
			}
		}
	}
	return nil
}

func loadDistribution(dist string) (Metadata, map[string]string, error) {
	metadataFile, err := os.Open(filepath.Join(dist, "metadata.json")) // #nosec G304 -- dist is an explicit local release directory.
	if err != nil {
		return Metadata{}, nil, fmt.Errorf("open release metadata: %w", err)
	}
	defer func() { _ = metadataFile.Close() }()

	var metadata Metadata
	decoder := json.NewDecoder(io.LimitReader(metadataFile, 1<<20))
	if err := decoder.Decode(&metadata); err != nil {
		return Metadata{}, nil, fmt.Errorf("decode release metadata: %w", err)
	}

	checksumFile, err := os.Open(filepath.Join(dist, "checksums.txt")) // #nosec G304 -- dist is an explicit local release directory.
	if err != nil {
		return Metadata{}, nil, fmt.Errorf("open checksum manifest: %w", err)
	}
	defer func() { _ = checksumFile.Close() }()
	checksums, err := ParseChecksums(checksumFile)
	if err != nil {
		return Metadata{}, nil, err
	}
	return metadata, checksums, nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- path is derived from a validated basename under the explicit dist directory.
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyArchive(path string) error {
	file, err := os.Open(path) // #nosec G304 -- path is a required archive under the explicit dist directory.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()

	wantModes := map[string]int64{"LICENSE": 0o644, "README.md": 0o644, "sith": 0o755}
	seen := make(map[string]bool, len(wantModes))
	reader := tar.NewReader(gzipReader)
	var timestamp time.Time
	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read tar: %w", readErr)
		}
		mode, ok := wantModes[header.Name]
		if !ok || seen[header.Name] {
			return fmt.Errorf("unexpected or duplicate archive entry %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg || header.Mode != mode {
			return fmt.Errorf("entry %s has type %d mode %#o, want regular %#o", header.Name, header.Typeflag, header.Mode, mode)
		}
		if header.Uid != 0 || header.Gid != 0 || header.Uname != "root" || header.Gname != "root" {
			return fmt.Errorf("entry %s does not have normalized root ownership", header.Name)
		}
		if header.ModTime.IsZero() {
			return fmt.Errorf("entry %s has zero modification time", header.Name)
		}
		if timestamp.IsZero() {
			timestamp = header.ModTime
		} else if !header.ModTime.Equal(timestamp) {
			return fmt.Errorf("entry %s timestamp differs from other entries", header.Name)
		}
		seen[header.Name] = true
	}
	if len(seen) != len(wantModes) {
		return fmt.Errorf("archive has %d expected entries, want %d", len(seen), len(wantModes))
	}
	return nil
}

func verifySBOM(path, archive string) error {
	file, err := os.Open(path) // #nosec G304 -- path is a required SBOM under the explicit dist directory.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var document struct {
		SPDXVersion       string `json:"spdxVersion"`
		Name              string `json:"name"`
		DocumentNamespace string `json:"documentNamespace"`
		CreationInfo      struct {
			Creators []string `json:"creators"`
		} `json:"creationInfo"`
		Packages []json.RawMessage `json:"packages"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 16<<20)).Decode(&document); err != nil {
		return fmt.Errorf("decode SPDX JSON: %w", err)
	}
	if document.SPDXVersion != "SPDX-2.3" || document.Name != archive || document.DocumentNamespace == "" {
		return fmt.Errorf("unexpected SPDX identity: version=%q name=%q namespace=%q", document.SPDXVersion, document.Name, document.DocumentNamespace)
	}
	if len(document.Packages) == 0 {
		return errors.New("SPDX document contains no packages")
	}
	for _, creator := range document.CreationInfo.Creators {
		if strings.HasPrefix(creator, "Tool: syft-") {
			return nil
		}
	}
	return errors.New("SPDX document was not created by Syft")
}

func verifyNativeBinary(archive string, metadata Metadata) error {
	directory, err := os.MkdirTemp("", "sith-release-verify-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	binary := filepath.Join(directory, "sith")
	if err := extractBinary(archive, binary); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// #nosec G204 -- executing the just-built native release binary is the verifier's purpose.
	output, err := exec.CommandContext(ctx, binary, "version", "--output", "json").Output()
	if err != nil {
		return fmt.Errorf("execute version command: %w", err)
	}
	var info struct {
		Version  string `json:"version"`
		Commit   string `json:"commit"`
		Date     string `json:"date"`
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		return fmt.Errorf("decode version output: %w", err)
	}
	if info.Version != metadata.Version || info.Commit != metadata.Commit || info.Date == "" || info.Date == "unknown" || info.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return fmt.Errorf("unexpected version metadata: %+v", info)
	}
	return nil
}

func extractBinary(archive, destination string) error {
	file, err := os.Open(archive) // #nosec G304 -- archive is a required file under the explicit dist directory.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = gzipReader.Close() }()
	reader := tar.NewReader(gzipReader)
	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			return errors.New("archive does not contain sith")
		}
		if readErr != nil {
			return readErr
		}
		if header.Name != "sith" {
			continue
		}
		if header.Size < 1 || header.Size > 256<<20 {
			return fmt.Errorf("binary size %d is outside the allowed range", header.Size)
		}
		// #nosec G302,G304 -- destination is a verifier-owned temp path and must be executable.
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		written, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if written != header.Size {
			return fmt.Errorf("extracted %d bytes, want %d", written, header.Size)
		}
		return closeErr
	}
}

func archiveName(version, goos, goarch string) string {
	return fmt.Sprintf("sith_%s_%s_%s.tar.gz", version, goos, goarch)
}

func releaseURL(tag, artifact string) string {
	return "https://github.com/ArdurAI/sith/releases/download/" + tag + "/" + artifact
}
