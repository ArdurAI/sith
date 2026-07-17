// SPDX-License-Identifier: Apache-2.0

package argocd

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

func TestProjectorHasNoNetworkOrMutationSeam(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read argocd package: %v", err)
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
			switch path {
			case "net/http", "os/exec", "k8s.io/client-go/dynamic", "k8s.io/client-go/rest":
				t.Fatalf("projector imports network or execution package %q", path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			declaration, ok := node.(*ast.FuncDecl)
			if !ok {
				return true
			}
			switch declaration.Name.Name {
			case "Plan", "Execute", "Verify", "Sync", "Rollback":
				t.Errorf("projector declares forbidden mutation seam %s", declaration.Name.Name)
			}
			return true
		})
	}
}
