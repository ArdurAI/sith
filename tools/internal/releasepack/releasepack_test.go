// SPDX-License-Identifier: Apache-2.0

package releasepack

import (
	"strings"
	"testing"
)

func TestParseChecksums(t *testing.T) {
	t.Parallel()
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	checksums, err := ParseChecksums(strings.NewReader(digestA + "  a.tar.gz\n" + digestB + " *b.tar.gz\n"))
	if err != nil {
		t.Fatalf("ParseChecksums() error = %v", err)
	}
	if checksums["a.tar.gz"] != digestA || checksums["b.tar.gz"] != digestB {
		t.Fatalf("ParseChecksums() = %#v", checksums)
	}
}

func TestParseChecksumsRejectsUnsafeOrAmbiguousEntries(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	tests := []string{
		"",
		"not-a-digest  file",
		digest + "  ../file",
		digest + "  file\n" + digest + "  file",
	}
	for _, input := range tests {
		if _, err := ParseChecksums(strings.NewReader(input)); err == nil {
			t.Errorf("ParseChecksums(%q) unexpectedly succeeded", input)
		}
	}
}

func TestRenderHomebrewFormula(t *testing.T) {
	t.Parallel()
	version := "1.2.3"
	checksums := make(map[string]string)
	for index, target := range targets {
		checksums[archiveName(version, target.os, target.arch)] = strings.Repeat(string(rune('a'+index)), 64)
	}
	formula, err := RenderHomebrewFormula(version, "v"+version, checksums)
	if err != nil {
		t.Fatalf("RenderHomebrewFormula() error = %v", err)
	}
	for _, want := range []string{
		`class Sith < Formula`,
		`version "1.2.3"`,
		`releases/download/v1.2.3/sith_1.2.3_darwin_arm64.tar.gz`,
		`releases/download/v1.2.3/sith_1.2.3_linux_amd64.tar.gz`,
		`assert_match "\"version\":\"1.2.3\"", output`,
	} {
		if !strings.Contains(string(formula), want) {
			t.Errorf("formula does not contain %q:\n%s", want, formula)
		}
	}
}

func TestRenderHomebrewFormulaRejectsIncompleteRelease(t *testing.T) {
	t.Parallel()
	checksums := map[string]string{archiveName("1.2.3", "darwin", "arm64"): strings.Repeat("a", 64)}
	if _, err := RenderHomebrewFormula("1.2.3", "v1.2.3", checksums); err == nil {
		t.Fatal("RenderHomebrewFormula() unexpectedly accepted missing targets")
	}
	if _, err := RenderHomebrewFormula("1.2.3", "v9.9.9", checksums); err == nil {
		t.Fatal("RenderHomebrewFormula() unexpectedly accepted mismatched tag")
	}
}
