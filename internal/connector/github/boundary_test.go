// SPDX-License-Identifier: Apache-2.0

package github

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProjectorHasNoIOCredentialPersistenceOrMutationSeam(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read github package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import: %v", err)
			}
			if path == "os" || path == "os/exec" || path == "syscall" || path == "plugin" ||
				path == "database/sql" || path == "io/fs" || strings.HasPrefix(path, "net/") || path == "net" ||
				strings.HasPrefix(path, "google.golang.org/grpc") || strings.HasPrefix(path, "k8s.io/client-go") {
				t.Fatalf("projector imports I/O, credential, or persistence package %q", path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			declaration, ok := node.(*ast.FuncDecl)
			if !ok {
				return true
			}
			switch declaration.Name.Name {
			case "Plan", "Execute", "Verify", "Write", "Delete", "Update", "Create", "Merge", "Persist", "Save", "Load", "Dial":
				t.Errorf("projector declares forbidden I/O or mutation seam %s", declaration.Name.Name)
			}
			return true
		})
	}
}
