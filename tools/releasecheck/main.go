// SPDX-License-Identifier: Apache-2.0

// Command releasecheck verifies a distribution or renders its Homebrew formula.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ArdurAI/sith/tools/internal/releasepack"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "releasecheck: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: releasecheck <verify|digests|formula> [flags]")
	}
	switch args[0] {
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		dist := flags.String("dist", "dist", "GoReleaser distribution directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("verify accepts no positional arguments")
		}
		if err := releasepack.VerifyDistribution(*dist); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "release distribution verified")
		return err
	case "digests":
		flags := flag.NewFlagSet("digests", flag.ContinueOnError)
		dist := flags.String("dist", "dist", "GoReleaser distribution directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("digests accepts no positional arguments")
		}
		digests, err := releasepack.ArchiveDigests(*dist)
		if err != nil {
			return err
		}
		_, err = stdout.Write(digests)
		return err
	case "formula":
		flags := flag.NewFlagSet("formula", flag.ContinueOnError)
		dist := flags.String("dist", "dist", "GoReleaser distribution directory")
		tag := flags.String("tag", "", "release tag, including v prefix")
		output := flags.String("output", "", "formula output path; stdout when empty")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("formula accepts no positional arguments")
		}
		metadata, checksums, err := loadInputs(*dist)
		if err != nil {
			return err
		}
		if *tag == "" {
			*tag = "v" + metadata.Version
		}
		formula, err := releasepack.RenderHomebrewFormula(metadata.Version, *tag, checksums)
		if err != nil {
			return err
		}
		if *output == "" {
			_, err = stdout.Write(formula)
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*output), 0o750); err != nil {
			return fmt.Errorf("create formula directory: %w", err)
		}
		if err := os.WriteFile(*output, formula, 0o600); err != nil {
			return fmt.Errorf("write formula: %w", err)
		}
		_, err = fmt.Fprintf(stdout, "wrote %s\n", *output)
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func loadInputs(dist string) (releasepack.Metadata, map[string]string, error) {
	metadataFile, err := os.Open(filepath.Join(dist, "metadata.json")) // #nosec G304 -- dist is an explicit local release directory.
	if err != nil {
		return releasepack.Metadata{}, nil, err
	}
	defer func() { _ = metadataFile.Close() }()
	var metadata releasepack.Metadata
	if err := json.NewDecoder(io.LimitReader(metadataFile, 1<<20)).Decode(&metadata); err != nil {
		return releasepack.Metadata{}, nil, fmt.Errorf("decode release metadata: %w", err)
	}
	checksumFile, err := os.Open(filepath.Join(dist, "checksums.txt")) // #nosec G304 -- dist is an explicit local release directory.
	if err != nil {
		return releasepack.Metadata{}, nil, err
	}
	defer func() { _ = checksumFile.Close() }()
	checksums, err := releasepack.ParseChecksums(checksumFile)
	return metadata, checksums, err
}
