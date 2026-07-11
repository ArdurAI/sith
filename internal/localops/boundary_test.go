// SPDX-License-Identifier: Apache-2.0

package localops_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLocalOperationPathsDoNotImportGovernedConnectorPath(t *testing.T) {
	t.Parallel()
	patterns := []string{
		"*.go",
		filepath.Join("..", "cli", "local.go"),
		filepath.Join("..", "tui", "local_actions.go"),
		filepath.Join("..", "connector", "kubeconfig", "local_*.go"),
	}
	files := make([]string, 0)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob local-operation path %q: %v", pattern, err)
		}
		files = append(files, matches...)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imported := range parsed.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", path, err)
			}
			if value == "github.com/ArdurAI/sith/internal/connector" ||
				strings.Contains(value, "github.com/ArdurAI/sith/internal/intent") ||
				strings.Contains(value, "github.com/ArdurAI/sith/internal/pep") {
				t.Fatalf("%s imports governed connector package", path)
			}
		}
	}
}
